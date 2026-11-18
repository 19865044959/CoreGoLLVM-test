// L08: 手写 amd64 汇编求 int64 切片最大/最小值，覆盖极值、升/降序、
// 定种子伪随机序列、空输入与 GC 后重复计算。
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

func maxI64Asm(p *int64, n int) int64
func minI64Asm(p *int64, n int) int64

func maxminGo(s []int64) (int64, int64) {
	if len(s) == 0 {
		return 0, 0
	}
	ma, mi := s[0], s[0]
	for _, v := range s[1:] {
		if v > ma {
			ma = v
		}
		if v < mi {
			mi = v
		}
	}
	return ma, mi
}

func lcg(seed uint64) func() int64 {
	x := seed
	return func() int64 {
		x = x*6364136223846793005 + 1442695040888963407
		return int64(x)
	}
}

func main() {
	cases := [][]int64{
		nil,
		{7},
		{-7},
		{1, 2, 3, 4, 5},
		{5, 4, 3, 2, 1},
		{3, 3, 3, 3},
		{math.MinInt64, math.MaxInt64, 0, -1, 42},
		{math.MaxInt64, math.MinInt64},
		{0, -1, -2, -3},
	}
	next := lcg(20261118)
	for r := 0; r < 8; r++ {
		s := make([]int64, 1+r*137)
		for i := range s {
			s[i] = next()
		}
		cases = append(cases, s)
	}

	for ci, s := range cases {
		wantMax, wantMin := maxminGo(s)
		var p *int64
		if len(s) > 0 {
			p = &s[0]
		}
		ck(maxI64Asm(p, len(s)) == wantMax, fmt.Sprintf("max case=%d", ci))
		ck(minI64Asm(p, len(s)) == wantMin, fmt.Sprintf("min case=%d", ci))

		gcStress()
		ck(maxI64Asm(p, len(s)) == wantMax, fmt.Sprintf("max after GC case=%d", ci))
		ck(minI64Asm(p, len(s)) == wantMin, fmt.Sprintf("min after GC case=%d", ci))
		if len(s) > 4 {
			sm, sn := maxminGo(s[1 : len(s)-1])
			ck(maxI64Asm(&s[1], len(s)-2) == sm, "max sub-slice")
			ck(minI64Asm(&s[1], len(s)-2) == sn, "min sub-slice")
		}
		runtime.KeepAlive(s)
	}
	finish("l08_asm_maxmin")
}
