// L09: C union 64 位读写：cgo 不能直接访问 union 字段，
// 通过 C 辅助函数验证 u/d/b[8]/lo-hi 四种视图的别名、大小端位模式、
// 符号位与跨 GC 稳定性。
package main

import (
	"fmt"
	"math"
	"os"
	"runtime"
	"sync/atomic"
	"unsafe"
)

/*
#include <stdint.h>
#include <stddef.h>

typedef union {
	uint64_t u;
	double d;
	uint8_t b[8];
	struct { uint32_t lo, hi; } parts;
} u64_t;

static void c_u64_set_u(u64_t *p, uint64_t v) { p->u = v; }
static uint64_t c_u64_get_u(const u64_t *p) { return p->u; }
static void c_u64_set_d(u64_t *p, double v) { p->d = v; }
static double c_u64_get_d(const u64_t *p) { return p->d; }
static void c_u64_set_b(u64_t *p, int i, uint8_t v) { p->b[i] = v; }
static uint8_t c_u64_get_b(const u64_t *p, int i) { return p->b[i]; }
static uint32_t c_u64_get_lo(const u64_t *p) { return p->parts.lo; }
static uint32_t c_u64_get_hi(const u64_t *p) { return p->parts.hi; }
static void c_u64_set_lo(u64_t *p, uint32_t v) { p->parts.lo = v; }
static void c_u64_set_hi(u64_t *p, uint32_t v) { p->parts.hi = v; }
static size_t c_u64_size(void) { return sizeof(u64_t); }
static size_t c_u64_align(void) { return _Alignof(u64_t); }
*/
import "C"

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

func main() {
	ck(uint64(unsafe.Sizeof(C.u64_t{})) == uint64(C.c_u64_size()), "sizeof union")
	ck(uint64(unsafe.Sizeof(C.u64_t{})) == 8, "union is 8 bytes")
	ck(uint64(C.c_u64_align()) == 8, "C alignof union is 8")

	var w C.u64_t
	C.c_u64_set_u(&w, C.uint64_t(0x1122334455667788))
	ck(uint64(C.c_u64_get_u(&w)) == 0x1122334455667788, "get u")
	for i, want := range []uint32{0x88, 0x77, 0x66, 0x55, 0x44, 0x33, 0x22, 0x11} {
		ck(uint32(C.c_u64_get_b(&w, C.int(i))) == want, fmt.Sprintf("b[%d] little-endian", i))
	}
	ck(uint32(C.c_u64_get_lo(&w)) == 0x55667788, "parts.lo")
	ck(uint32(C.c_u64_get_hi(&w)) == 0x11223344, "parts.hi")

	// 写字节视图别名回 u。
	C.c_u64_set_b(&w, 7, 0xAA)
	ck(uint64(C.c_u64_get_u(&w)) == 0xAA22334455667788, "write b[7] aliases u")

	// 写 lo/hi 视图。
	C.c_u64_set_lo(&w, 0xdeadbeef)
	ck(uint64(C.c_u64_get_u(&w)) == 0xAA223344deadbeef, "write lo aliases u")
	C.c_u64_set_hi(&w, 0x01020304)
	ck(uint32(C.c_u64_get_hi(&w)) == 0x01020304, "hi readback")
	ck(uint32(C.c_u64_get_lo(&w)) == 0xdeadbeef, "lo unaffected")

	// double 位模式。
	C.c_u64_set_d(&w, C.double(1.5))
	ck(uint64(C.c_u64_get_u(&w)) == math.Float64bits(1.5), "double 1.5 bits")
	ck(float64(C.c_u64_get_d(&w)) == 1.5, "get d")
	negZero := math.Copysign(0, -1)
	C.c_u64_set_d(&w, C.double(negZero))
	ck(uint64(C.c_u64_get_u(&w)) == math.Float64bits(negZero), "negative zero sign bit")

	gcStress()
	ck(uint64(C.c_u64_get_u(&w)) == 0x8000000000000000, "union after GC")
	ck(float64(C.c_u64_get_d(&w)) == 0.0 && math.Signbit(float64(C.c_u64_get_d(&w))), "negative zero after GC")

	// 重复读写 + GC，确认别名视图稳定。
	for i := 0; i < 200; i++ {
		v := uint64(i)*0x0101010101010101 + 0x1234
		C.c_u64_set_u(&w, C.uint64_t(v))
		ck(uint64(C.c_u64_get_u(&w)) == v, fmt.Sprintf("roundtrip i=%d", i))
		if i%32 == 31 {
			gcStress()
		}
	}

	finish("l09_cgo_union64")
}
