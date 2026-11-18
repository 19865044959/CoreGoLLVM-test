// L08: 手写 amd64 汇编前导零计数（BSRQ + 零值特判），
// 与 math/bits.LeadingZeros64 对照；覆盖 2 的幂、全 1、零值与 GC。
package main

import (
	"fmt"
	"math/bits"
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

func clzAsm(x uint64) int

func lcg(seed uint64) func() uint64 {
	x := seed
	return func() uint64 {
		x ^= x << 13
		x ^= x >> 7
		x ^= x << 17
		return x
	}
}

func main() {
	vals := []uint64{
		0, 1, 2, 3, 4, 5, 6, 7, 8, 15, 16, 17,
		1 << 31, 1<<32 - 1, 1 << 32, 1<<63 - 1, 1 << 63,
		0xffffffffffffffff, 0x8000000000000001, 0x00ffffffffffffff,
	}
	next := lcg(0x243f6a8885a308d3)
	for i := 0; i < 512; i++ {
		vals = append(vals, next())
	}

	for i, x := range vals {
		got := clzAsm(x)
		want := bits.LeadingZeros64(x)
		ck(got == want, fmt.Sprintf("clz i=%d x=%016x got=%d want=%d", i, x, got, want))
		ck(got >= 0 && got <= 64, fmt.Sprintf("range i=%d got=%d", i, got))
	}

	// 单个 1 落在每个位置。
	for k := 0; k < 64; k++ {
		x := uint64(1) << uint(k)
		ck(clzAsm(x) == 63-k, fmt.Sprintf("single bit k=%d got=%d want=%d", k, clzAsm(x), 63-k))
	}

	// 最高位为 1 时前导零恒为 0；补 0 位不改变结果。
	for k := 0; k < 64; k++ {
		hi := ^uint64(0) >> uint(k)
		ck(clzAsm(hi) == k, fmt.Sprintf("top-zero k=%d got=%d want=%d", k, clzAsm(hi), k))
	}

	gcStress()
	for i := 0; i < 128; i++ {
		ck(clzAsm(vals[i]) == bits.LeadingZeros64(vals[i]), fmt.Sprintf("clz after GC i=%d", i))
	}

	finish("l08_asm_clz_u64")
}
