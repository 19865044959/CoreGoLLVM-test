// L08: 手写 amd64 汇编同时计算 uint64 的前导零与尾随零个数。
// BSRQ/BSFQ 定位最高/最低置位，零输入显式返回 64/64；
// 与 math/bits.LeadingZeros64/TrailingZeros64 全量对照。
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

func clzCtzAsm(x uint64) (clz, ctz int)

func check(x uint64) {
	wl, wt := bits.LeadingZeros64(x), bits.TrailingZeros64(x)
	gl, gt := clzCtzAsm(x)
	ck(gl == wl && gt == wt,
		fmt.Sprintf("x=%#016x got=(%d,%d) want=(%d,%d)", x, gl, gt, wl, wt))
}

func main() {
	vals := []uint64{
		0, 1, 2, 3, 4, 7, 8, 15, 16, 17, 31, 32, 33, 63, 64, 127, 128, 255, 256,
		1<<31 - 1, 1 << 31, 1<<32 - 1, 1 << 32, 1<<63 - 1, 1 << 63,
		^uint64(0), ^uint64(0) >> 1, ^uint64(1),
		0x8000000000000001, 0xffffffff00000000, 0x00000000ffffffff,
		0xaaaaaaaaaaaaaaaa, 0x5555555555555555,
		0x0f0f0f0f0f0f0f0f, 0xf0f0f0f0f0f0f0f0,
	}
	for _, v := range vals {
		check(v)
	}
	for k := 0; k < 64; k++ {
		check(uint64(1) << uint(k))
		check(^uint64(0) >> uint(k))
		check(^uint64(0) << uint(k))
	}
	x := uint64(0x9e3779b97f4a7c15)
	for i := 0; i < 5000; i++ {
		x = x*6364136223846793005 + 1442695040888963407
		check(x)
	}
	gcStress()
	for i, v := range vals {
		wl, wt := bits.LeadingZeros64(v), bits.TrailingZeros64(v)
		gl, gt := clzCtzAsm(v)
		ck(gl == wl && gt == wt, fmt.Sprintf("after GC i=%d v=%#x", i, v))
	}
	finish("l08_asm_clz_ctz_u64")
}
