// L09: C union 与位域：cgo 无法直接访问 union/bit-field 字段，
// 通过 C 辅助函数读写，校验别名（u/b/f）与位域打包布局；跨 GC 复核。
package main

/*
#include <stdint.h>
#include <string.h>

typedef union {
	uint32_t u;
	uint8_t b[4];
	float f;
} word_t;

typedef struct {
	uint32_t a : 3;
	uint32_t b : 5;
	uint32_t c : 8;
	uint32_t d : 16;
} flags_t;

static void c_word_set_u(word_t *w, uint32_t v) { w->u = v; }
static uint32_t c_word_get_u(const word_t *w) { return w->u; }
static uint32_t c_word_get_b(const word_t *w, int i) { return w->b[i]; }
static void c_word_set_b(word_t *w, int i, uint32_t v) { w->b[i] = (uint8_t)v; }
static void c_word_set_f(word_t *w, float v) { w->f = v; }
static float c_word_get_f(const word_t *w) { return w->f; }
static size_t c_word_size(void) { return sizeof(word_t); }

static void c_flags_set(flags_t *p, uint32_t a, uint32_t b, uint32_t c, uint32_t d) {
	p->a = a; p->b = b; p->c = c; p->d = d;
}
static uint32_t c_flags_a(const flags_t *p) { return p->a; }
static uint32_t c_flags_b(const flags_t *p) { return p->b; }
static uint32_t c_flags_c(const flags_t *p) { return p->c; }
static uint32_t c_flags_d(const flags_t *p) { return p->d; }
static uint32_t c_flags_packed(const flags_t *p) {
	uint32_t v;
	memcpy(&v, p, sizeof v);
	return v;
}
static size_t c_flags_size(void) { return sizeof(flags_t); }
*/
import "C"

import (
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

func main() {
	ck(uint64(unsafe.Sizeof(C.word_t{})) == uint64(C.c_word_size()), "sizeof union")
	ck(uint64(unsafe.Sizeof(C.flags_t{})) == uint64(C.c_flags_size()), "sizeof bitfield struct")
	ck(uint64(unsafe.Sizeof(C.word_t{})) == 4, "union is 4 bytes")

	// union：u 与 b[4] 互为别名（linux/amd64 小端）。
	var w C.word_t
	C.c_word_set_u(&w, 0x11223344)
	ck(uint32(C.c_word_get_u(&w)) == 0x11223344, "union get u")
	for i, want := range []uint32{0x44, 0x33, 0x22, 0x11} {
		ck(uint32(C.c_word_get_b(&w, C.int(i))) == want, fmt.Sprintf("union b[%d] little-endian", i))
	}

	C.c_word_set_b(&w, 2, 0xAA)
	ck(uint32(C.c_word_get_u(&w)) == 0x11AA3344, "write b[2] aliases u")

	// union：float 位模式。
	C.c_word_set_f(&w, C.float(1.5))
	ck(uint32(C.c_word_get_u(&w)) == math.Float32bits(1.5), "float bits in union")
	ck(float32(C.c_word_get_f(&w)) == 1.5, "union get f")

	gcStress()
	ck(uint32(C.c_word_get_u(&w)) == math.Float32bits(1.5), "union after GC")

	// 位域：a:3 / b:5 / c:8 / d:16，gcc/clang x86-64 小端从低位开始打包。
	var f C.flags_t
	C.c_flags_set(&f, 5, 17, 200, 40000)
	ck(uint32(C.c_flags_a(&f)) == 5, "bitfield a")
	ck(uint32(C.c_flags_b(&f)) == 17, "bitfield b")
	ck(uint32(C.c_flags_c(&f)) == 200, "bitfield c")
	ck(uint32(C.c_flags_d(&f)) == 40000, "bitfield d")
	wantPacked := uint32(5) | uint32(17)<<3 | uint32(200)<<8 | uint32(40000)<<16
	ck(uint32(C.c_flags_packed(&f)) == wantPacked, "bitfield packed layout")

	gcStress()
	ck(uint32(C.c_flags_packed(&f)) == wantPacked, "bitfield packed after GC")

	C.c_flags_set(&f, 7, 31, 255, 65535)
	wantPacked = uint32(7) | uint32(31)<<3 | uint32(255)<<8 | uint32(65535)<<16
	ck(uint32(C.c_flags_packed(&f)) == wantPacked, "bitfield all-ones packed")
	ck(uint32(C.c_flags_a(&f)) == 7 && uint32(C.c_flags_d(&f)) == 65535, "bitfield all-ones fields")
	finish("l09_cgo_union_bitfield")
}
