// L09: C tagged union：union val_u { int64/double/point/raw[8] } + tag，
// cgo 不能直接访问 union 成员，全部经 C 辅助函数读写；校验 tag 分派、
// 别名 raw 视图、offset/sizeof、跨 GC 稳定。C 不保留 Go 指针。
package main

/*
#include <stddef.h>
#include <stdint.h>

typedef enum {
	VAL_INT = 1,
	VAL_DBL = 2,
	VAL_PT = 3,
	VAL_RAW = 4
} val_tag_t;

typedef union {
	int64_t i;
	double d;
	struct {
		int32_t x;
		int32_t y;
	} pt;
	uint8_t raw[8];
} val_u;

typedef struct {
	uint32_t tag;
	val_u u;
} val_t;

static size_t c_val_size(void) { return sizeof(val_t); }
static size_t c_val_u_offset(void) { return offsetof(val_t, u); }
static int32_t c_is_little_endian(void) {
	uint16_t x = 1;
	return *(const uint8_t *)&x == 1;
}
static void c_val_set_int(val_t *v, int64_t x) {
	v->tag = VAL_INT;
	v->u.i = x;
}
static void c_val_set_double(val_t *v, double x) {
	v->tag = VAL_DBL;
	v->u.d = x;
}
static void c_val_set_point(val_t *v, int32_t x, int32_t y) {
	v->tag = VAL_PT;
	v->u.pt.x = x;
	v->u.pt.y = y;
}
static void c_val_set_raw(val_t *v, int32_t seed) {
	v->tag = VAL_RAW;
	for (int32_t j = 0; j < 8; j++) {
		v->u.raw[j] = (uint8_t)(seed + j * 7);
	}
}
static void c_vals_fill(val_t *v, int32_t n) {
	for (int32_t i = 0; i < n; i++) {
		switch (i % 4) {
		case 0:
			c_val_set_int(&v[i], (int64_t)i * 1000 - 3);
			break;
		case 1:
			c_val_set_double(&v[i], (double)i * 1.5 + 0.25);
			break;
		case 2:
			c_val_set_point(&v[i], i * 2 - 5, -(i * 3));
			break;
		default:
			v[i].tag = VAL_RAW;
			for (int32_t j = 0; j < 8; j++) {
				v[i].u.raw[j] = (uint8_t)(i + j * 7);
			}
			break;
		}
	}
}
static int32_t c_val_get_tag(const val_t *v) { return (int32_t)v->tag; }
static int64_t c_val_get_int(const val_t *v) { return v->u.i; }
static double c_val_get_double(const val_t *v) { return v->u.d; }
static void c_val_get_point(const val_t *v, int32_t *x, int32_t *y) {
	*x = v->u.pt.x;
	*y = v->u.pt.y;
}
static int32_t c_val_raw(const val_t *v, int32_t k) {
	if (k < 0 || k >= 8) {
		return -1;
	}
	return (int32_t)v->u.raw[k];
}
static int32_t c_vals_count_tag(const val_t *v, int32_t n, int32_t tag) {
	int32_t c = 0;
	for (int32_t i = 0; i < n; i++) {
		if ((int32_t)v[i].tag == tag) {
			c++;
		}
	}
	return c;
}
static int64_t c_vals_sum(const val_t *v, int32_t n) {
	int64_t s = 0;
	for (int32_t i = 0; i < n; i++) {
		switch ((int32_t)v[i].tag) {
		case VAL_INT:
			s += v[i].u.i;
			break;
		case VAL_DBL:
			s += (int64_t)v[i].u.d;
			break;
		case VAL_PT:
			s += (int64_t)v[i].u.pt.x + (int64_t)v[i].u.pt.y;
			break;
		case VAL_RAW:
			for (int32_t j = 0; j < 8; j++) {
				s += (int64_t)v[i].u.raw[j];
			}
			break;
		default:
			break;
		}
	}
	return s;
}
*/
import "C"

import (
	"encoding/binary"
	"fmt"
	"math"
	"os"
	"runtime"
	"sync/atomic"
	"unsafe"
)

var (
	failed int32
	gcSink [][]byte
)

func ck(ok bool, msg string) {
	if !ok {
		atomic.AddInt32(&failed, 1)
		fmt.Println("FAIL:", msg)
	}
}

func gcStress() {
	for i := 0; i < 16; i++ {
		b := make([]byte, 4096)
		b[0] = byte(i)
		gcSink = append(gcSink[:0], b)
	}
	gcSink = gcSink[:0]
	runtime.GC()
	runtime.GC()
}

func finish(name string) {
	if n := atomic.LoadInt32(&failed); n != 0 {
		fmt.Printf("FAILED %s (%d failed checks)\n", name, n)
		os.Exit(1)
	}
	fmt.Println("ALL PASS " + name)
}

func wantTag(i int) int32 { return int32(i%4) + 1 }

func wantSumAt(i int) int64 {
	switch i % 4 {
	case 0:
		return int64(i)*1000 - 3
	case 1:
		return int64(float64(i)*1.5 + 0.25)
	case 2:
		return int64(i*2-5) + int64(-(i * 3))
	default:
		var s int64
		for j := 0; j < 8; j++ {
			s += int64(uint8(i + j*7))
		}
		return s
	}
}

func checkAtIndex(v *C.val_t, i int) {
	tag := int32(C.c_val_get_tag(v))
	ck(tag == wantTag(i), fmt.Sprintf("tag[%d]", i))
	switch i % 4 {
	case 0:
		want := int64(i)*1000 - 3
		ck(int64(C.c_val_get_int(v)) == want, fmt.Sprintf("int[%d]", i))
		if int32(C.c_is_little_endian()) == 1 {
			var b [8]byte
			binary.LittleEndian.PutUint64(b[:], uint64(want))
			for k := 0; k < 8; k++ {
				ck(int32(C.c_val_raw(v, C.int32_t(k))) == int32(b[k]), fmt.Sprintf("int raw[%d][%d]", i, k))
			}
		}
	case 1:
		want := float64(i)*1.5 + 0.25
		ck(float64(C.c_val_get_double(v)) == want, fmt.Sprintf("double[%d]", i))
		if int32(C.c_is_little_endian()) == 1 {
			var b [8]byte
			binary.LittleEndian.PutUint64(b[:], math.Float64bits(want))
			for k := 0; k < 8; k++ {
				ck(int32(C.c_val_raw(v, C.int32_t(k))) == int32(b[k]), fmt.Sprintf("double raw[%d][%d]", i, k))
			}
		}
	case 2:
		var x, y C.int32_t
		C.c_val_get_point(v, &x, &y)
		ck(int32(x) == int32(i*2-5) && int32(y) == int32(-(i*3)), fmt.Sprintf("point[%d]", i))
		ck(int32(C.c_val_raw(v, C.int32_t(-1))) == -1, "raw negative index")
		ck(int32(C.c_val_raw(v, 8)) == -1, "raw out of range")
	default:
		for k := 0; k < 8; k++ {
			ck(int32(C.c_val_raw(v, C.int32_t(k))) == int32(uint8(i+k*7)), fmt.Sprintf("raw[%d][%d]", i, k))
		}
	}
}

func main() {
	ck(uint64(unsafe.Sizeof(C.val_t{})) == uint64(C.c_val_size()), "sizeof val_t")
	ck(uint64(unsafe.Offsetof(C.val_t{}.u)) == uint64(C.c_val_u_offset()), "offsetof val_t.u")
	ck(unsafe.Sizeof(C.val_t{}) >= 12, "tagged union size sane")

	// C 填充的 tagged union 数组。
	const n = 12
	vals := make([]C.val_t, n)
	C.c_vals_fill(&vals[0], C.int32_t(n))
	var wantTotal int64
	for i := 0; i < n; i++ {
		checkAtIndex(&vals[i], i)
		ck(uint32(vals[i].tag) == uint32(wantTag(i)), fmt.Sprintf("direct tag field[%d]", i))
		wantTotal += wantSumAt(i)
	}
	ck(int64(C.c_vals_sum(&vals[0], C.int32_t(n))) == wantTotal, "C tagged union sum")
	ck(int32(C.c_vals_count_tag(&vals[0], C.int32_t(n), C.int32_t(1))) == 3, "count tag INT")
	ck(int32(C.c_vals_count_tag(&vals[0], C.int32_t(n), C.int32_t(2))) == 3, "count tag DBL")
	ck(int32(C.c_vals_count_tag(&vals[0], C.int32_t(n), C.int32_t(3))) == 3, "count tag PT")
	ck(int32(C.c_vals_count_tag(&vals[0], C.int32_t(n), C.int32_t(4))) == 3, "count tag RAW")

	// Go 构造 tagged union 数组（全部经 C setter），C 求和。
	gvals := make([]C.val_t, 8)
	var gTotal int64
	for i := range gvals {
		switch i % 4 {
		case 0:
			C.c_val_set_int(&gvals[i], C.int64_t(int64(i)*1000-3))
		case 1:
			C.c_val_set_double(&gvals[i], C.double(float64(i)*1.5+0.25))
		case 2:
			C.c_val_set_point(&gvals[i], C.int32_t(i*2-5), C.int32_t(-(i * 3)))
		default:
			C.c_val_set_raw(&gvals[i], C.int32_t(i))
		}
		gTotal += wantSumAt(i)
	}
	ck(int64(C.c_vals_sum(&gvals[0], C.int32_t(len(gvals)))) == gTotal, "Go-constructed tagged union sum")
	for i := range gvals {
		checkAtIndex(&gvals[i], i)
	}

	// 切换活动成员时 tag 必须随之变化。
	C.c_val_set_double(&gvals[0], C.double(2.5))
	ck(int32(C.c_val_get_tag(&gvals[0])) == int32(C.VAL_DBL), "tag switches to DBL")
	ck(float64(C.c_val_get_double(&gvals[0])) == 2.5, "switched double value")
	C.c_val_set_int(&gvals[0], C.int64_t(-99))
	ck(int32(C.c_val_get_tag(&gvals[0])) == int32(C.VAL_INT), "tag switches back to INT")
	ck(int64(C.c_val_get_int(&gvals[0])) == -99, "switched int value")

	// 跨 GC 稳定。
	for round := 0; round < 8; round++ {
		gcStress()
		ck(int64(C.c_vals_sum(&vals[0], C.int32_t(n))) == wantTotal, fmt.Sprintf("C sum round=%d", round))
		for i := 0; i < n; i += 3 {
			checkAtIndex(&vals[i], i)
		}
		ck(int32(C.c_val_get_tag(&gvals[0])) == int32(C.VAL_INT), fmt.Sprintf("gvals tag round=%d", round))
		ck(int64(C.c_val_get_int(&gvals[0])) == -99, fmt.Sprintf("gvals value round=%d", round))
		runtime.KeepAlive(vals)
		runtime.KeepAlive(gvals)
	}

	finish("l09_cgo_union_tagged")
}
