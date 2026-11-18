// L09: C 函数指针表：C 静态表中保存 add/sub/mul/xor/min 五个函数指针，
// 按索引分发；Go 侧校验每个算子与非法索引，并跨多轮 GC 重复调用。
package main

import (
	"fmt"
	"math"
	"os"
	"runtime"
	"sync/atomic"
)

/*
#include <stdint.h>
#include <stddef.h>

typedef int64_t (*op_t)(int64_t, int64_t);

static int64_t op_add(int64_t a, int64_t b) { return (int64_t)((uint64_t)a + (uint64_t)b); }
static int64_t op_sub(int64_t a, int64_t b) { return (int64_t)((uint64_t)a - (uint64_t)b); }
static int64_t op_mul(int64_t a, int64_t b) { return (int64_t)((uint64_t)a * (uint64_t)b); }
static int64_t op_xor(int64_t a, int64_t b) { return a ^ b; }
static int64_t op_min(int64_t a, int64_t b) { return a < b ? a : b; }

static op_t c_ops[5] = {op_add, op_sub, op_mul, op_xor, op_min};

static int32_t c_op_count(void) { return 5; }

static int64_t c_dispatch(int32_t idx, int64_t a, int64_t b) {
	if (idx < 0 || idx >= 5) return INT64_MIN;
	return c_ops[idx](a, b);
}

static int32_t c_table_distinct(void) {
	int32_t n = 0;
	for (int32_t i = 0; i < 5; i++) {
		int32_t seen = 0;
		for (int32_t j = 0; j < i; j++) {
			if (c_ops[i] == c_ops[j]) { seen = 1; break; }
		}
		if (!seen) n++;
	}
	return n;
}
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

func opGo(idx int, a, b int64) int64 {
	switch idx {
	case 0:
		return a + b
	case 1:
		return a - b
	case 2:
		return a * b
	case 3:
		return a ^ b
	case 4:
		if a < b {
			return a
		}
		return b
	}
	return math.MinInt64
}

func main() {
	ck(int32(C.c_op_count()) == 5, "C function table has 5 entries")
	ck(int32(C.c_table_distinct()) == 5, "all function pointers distinct")

	pairs := [][2]int64{
		{0, 0}, {7, 3}, {-9, 4}, {123456789, -987654321},
		{1 << 40, 3}, {math.MaxInt64, 1}, {math.MinInt64, -1},
		{0x0f0f0f0f, 0x3333}, {-1, -1},
	}
	for idx := 0; idx < 5; idx++ {
		for pi, p := range pairs {
			got := int64(C.c_dispatch(C.int32_t(idx), C.int64_t(p[0]), C.int64_t(p[1])))
			want := opGo(idx, p[0], p[1])
			ck(got == want, fmt.Sprintf("dispatch op=%d pair=%d got=%d want=%d", idx, pi, got, want))
		}
	}

	for _, idx := range []int{-1, 5, 6, 100, math.MaxInt32} {
		got := int64(C.c_dispatch(C.int32_t(idx), 1, 2))
		ck(got == math.MinInt64, fmt.Sprintf("invalid index=%d sentinel", idx))
	}

	gcStress()
	for round := 0; round < 100; round++ {
		a := int64(round)*3 - 150
		b := int64(round % 11)
		for idx := 0; idx < 5; idx++ {
			ck(int64(C.c_dispatch(C.int32_t(idx), C.int64_t(a), C.int64_t(b))) == opGo(idx, a, b),
				fmt.Sprintf("post-GC round=%d op=%d", round, idx))
		}
		if round%25 == 24 {
			gcStress()
		}
	}

	finish("l09_cgo_fnptr_table")
}
