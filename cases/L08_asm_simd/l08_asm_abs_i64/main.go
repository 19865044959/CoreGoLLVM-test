// L08: 手写 amd64 汇编 int64 绝对值（NEGQ + CMOVQLT），
// 覆盖 MinInt64 回绕、极值、伪随机输入、幂等性与 GC。
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

func absI64Asm(x int64) int64

func absGo(x int64) int64 {
	if x < 0 {
		return -x
	}
	return x
}

func lcg(seed uint64) func() int64 {
	x := seed
	return func() int64 {
		x = x*6364136223846793005 + 1442695040888963407
		return int64(x)
	}
}

func main() {
	vals := []int64{
		0, 1, -1, 2, -2, 7, -7, 42, -42,
		1 << 62, -(1 << 62), math.MaxInt64, math.MinInt64,
		math.MaxInt64 - 1, math.MinInt64 + 1,
	}
	next := lcg(0x123456789abcdef)
	for i := 0; i < 500; i++ {
		vals = append(vals, next())
	}

	for i, x := range vals {
		got := absI64Asm(x)
		want := absGo(x)
		ck(got == want, fmt.Sprintf("abs i=%d x=%d got=%d want=%d", i, x, got, want))
		if x != math.MinInt64 {
			ck(got >= 0, fmt.Sprintf("non-negative i=%d x=%d got=%d", i, x, got))
		} else {
			ck(got == math.MinInt64, fmt.Sprintf("minint special got=%d", got))
			ck(got == -got, "minint negation wraps")
		}
		ck(absI64Asm(got) == got, fmt.Sprintf("idempotent i=%d x=%d", i, x))
	}

	// 对称性：abs(x) == abs(-x)，MinInt64 除外。
	for _, x := range vals {
		if x == math.MinInt64 {
			continue
		}
		ck(absI64Asm(x) == absI64Asm(-x), fmt.Sprintf("symmetric x=%d", x))
	}

	// 负值区间逐值检查。
	for x := int64(-4096); x <= 0; x++ {
		if absI64Asm(x) != -x {
			ck(false, fmt.Sprintf("negative sweep x=%d got=%d", x, absI64Asm(x)))
		}
	}

	gcStress()
	for i := 0; i < 256; i++ {
		ck(absI64Asm(vals[i]) == absGo(vals[i]), fmt.Sprintf("abs after GC i=%d", i))
	}

	finish("l08_asm_abs_i64")
}
