// L08: 手写 amd64 汇编 clamp（CMPQ + CMOVQ 无分支），
// 覆盖区间内/外、边界相等、lo==hi 及 int64 极值。
package main

import (
	"fmt"
	"math"
	"os"
	"runtime"
	"sync/atomic"
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

func clampAsm(v, lo, hi int64) int64

func clampGo(v, lo, hi int64) int64 {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

func main() {
	ranges := [][2]int64{
		{0, 100},
		{-100, 100},
		{math.MinInt64, math.MaxInt64},
		{math.MinInt64, 0},
		{0, math.MaxInt64},
		{7, 7},
		{-1 << 40, 1 << 40},
	}
	vals := []int64{
		0, 1, -1, 7, 50, 100, 101, -100, -101,
		math.MinInt64, math.MinInt64 + 1, math.MaxInt64, math.MaxInt64 - 1,
	}
	for i := 0; i < 500; i++ {
		vals = append(vals, int64((i*2654435761)%2001)-1000)
	}
	for _, r := range ranges {
		for _, v := range vals {
			got := clampAsm(v, r[0], r[1])
			want := clampGo(v, r[0], r[1])
			ck(got == want, fmt.Sprintf("clamp v=%d lo=%d hi=%d got=%d want=%d", v, r[0], r[1], got, want))
			ck(got >= r[0] && got <= r[1], fmt.Sprintf("clamp range v=%d", v))
		}
	}

	gcStress()
	for i, v := range vals[:64] {
		ck(clampAsm(v, -10, 10) == clampGo(v, -10, 10), fmt.Sprintf("clamp after GC i=%d", i))
	}

	finish("l08_asm_clamp_i64")
}
