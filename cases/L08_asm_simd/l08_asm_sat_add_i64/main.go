// L08: 手写 amd64 汇编有符号饱和加法（溢出检测 + 极值钳制），
// 与 Go 参考实现对照；覆盖 MinInt64/MaxInt64 边界、伪随机对、交换律与 GC。
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

func satAddAsm(a, b int64) int64

func satAddGo(a, b int64) int64 {
	if b > 0 && a > math.MaxInt64-b {
		return math.MaxInt64
	}
	if b < 0 && a < math.MinInt64-b {
		return math.MinInt64
	}
	return a + b
}

func lcg(seed uint64) func() int64 {
	x := seed
	return func() int64 {
		x = x*6364136223846793005 + 1442695040888963407
		return int64(x)
	}
}

func check(a, b int64) {
	got := satAddAsm(a, b)
	want := satAddGo(a, b)
	ck(got == want, fmt.Sprintf("satAdd a=%d b=%d got=%d want=%d", a, b, got, want))
	ck(got >= math.MinInt64 && got <= math.MaxInt64, fmt.Sprintf("range a=%d b=%d", a, b))
	// 交换律。
	ck(satAddAsm(b, a) == got, fmt.Sprintf("commutative a=%d b=%d", a, b))
}

func main() {
	bounds := []int64{
		math.MinInt64, math.MinInt64 + 1, math.MinInt64 + 2,
		-1 << 40, -2, -1, 0, 1, 2, 1 << 40,
		math.MaxInt64 - 2, math.MaxInt64 - 1, math.MaxInt64,
	}
	for _, a := range bounds {
		for _, b := range bounds {
			check(a, b)
		}
	}

	next := lcg(20261118)
	for i := 0; i < 2000; i++ {
		a := next()
		b := next()
		check(a, b)
		// 构造必然溢出的近边界对。
		check(math.MaxInt64-int64(i&0xff), int64(i&0xff)+1)
		check(math.MinInt64+int64(i&0xff), -(int64(i&0xff) + 1))
	}

	// 不溢出时必须等于普通加法，且饱和只在溢出时发生。
	for i := int64(-100); i <= 100; i++ {
		for j := int64(-100); j <= 100; j++ {
			if satAddAsm(i, j) != i+j {
				ck(false, fmt.Sprintf("non-overflow i=%d j=%d", i, j))
			}
		}
	}

	gcStress()
	for _, a := range bounds {
		for _, b := range bounds {
			ck(satAddAsm(a, b) == satAddGo(a, b), fmt.Sprintf("after GC a=%d b=%d", a, b))
		}
	}

	finish("l08_asm_sat_add_i64")
}
