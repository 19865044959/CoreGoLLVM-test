// L08: 手写 amd64 汇编位运算：SWAR popcount 与 ROLQ 循环移位，
// 与 math/bits 全量对照，覆盖 0、全 1、交替位、伪随机值与 k>64。
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

func popcountAsm(x uint64) int
func rotlAsm(x uint64, k uint) uint64

func main() {
	vals := []uint64{
		0, 1, 2, 3, 1<<63 - 1, 1 << 63, ^uint64(0),
		0x5555555555555555, 0xaaaaaaaaaaaaaaaa,
		0x0f0f0f0f0f0f0f0f, 0xf0f0f0f0f0f0f0f0,
	}
	x := uint64(0x9e3779b97f4a7c15)
	for i := 0; i < 2000; i++ {
		x = x*6364136223846793005 + 1442695040888963407
		vals = append(vals, x)
	}

	for i, v := range vals {
		ck(popcountAsm(v) == bits.OnesCount64(v), fmt.Sprintf("popcount i=%d x=%#x", i, v))
	}
	for k := uint(0); k <= 130; k++ {
		for i, v := range vals[:64] {
			ck(rotlAsm(v, k) == bits.RotateLeft64(v, int(k)),
				fmt.Sprintf("rotl k=%d i=%d x=%#x", k, i, v))
		}
	}
	gcStress()
	for i := 0; i < 64; i++ {
		v := vals[i]
		ck(popcountAsm(v) == bits.OnesCount64(v), fmt.Sprintf("popcount after GC i=%d", i))
		ck(rotlAsm(v, uint(i)) == bits.RotateLeft64(v, i), fmt.Sprintf("rotl after GC i=%d", i))
	}
	finish("l08_asm_bitops")
}
