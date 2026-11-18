// L08: 手写 amd64 汇编移位/旋转：SHLQ、SARQ 与 32 位 RORL。
// x86 按计数低 6 位（64 位移位）或低 5 位（32 位旋转）取模，
// Go 参考实现显式按同样规则取模后逐例对照。
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

func shlAsm(x uint64, k uint) uint64
func sarAsm(x int64, k uint) int64
func ror32Asm(x uint32, k uint) uint32

func main() {
	xs := []uint64{
		0, 1, 2, 3, 0x8000000000000000, 0xffffffffffffffff,
		0x0123456789abcdef, 0xfedcba9876543210,
	}
	for i := 0; i < 200; i++ {
		xs = append(xs, uint64(i)*0x9e3779b97f4a7c15+0x1234)
	}
	for _, k := range []uint{0, 1, 7, 31, 32, 63, 64, 65, 127, 200} {
		km := k & 63
		for i, x := range xs {
			want := x << km
			ck(shlAsm(x, k) == want, fmt.Sprintf("shl x=%d k=%d", i, k))

			swant := int64(x)
			if km != 0 {
				swant >>= km // Go 的有符号右移为算术右移
			}
			ck(sarAsm(int64(x), k) == swant, fmt.Sprintf("sar x=%d k=%d", i, k))
		}
	}

	ys := []uint32{0, 1, 2, 0x80000000, 0xffffffff, 0x12345678, 0xdeadbeef}
	for i := 0; i < 200; i++ {
		ys = append(ys, uint32(i)*2654435761+7)
	}
	for _, k := range []uint{0, 1, 5, 8, 16, 31, 32, 33, 63, 100} {
		for i, y := range ys {
			want := bits.RotateLeft32(y, -int(k&31))
			ck(ror32Asm(y, k) == want, fmt.Sprintf("ror32 y=%d k=%d", i, k))
		}
	}

	gcStress()
	for i, x := range xs[:64] {
		ck(shlAsm(x, 13) == x<<13, fmt.Sprintf("shl after GC x=%d", i))
		ck(sarAsm(int64(x), 9) == int64(x)>>9, fmt.Sprintf("sar after GC x=%d", i))
	}
	for i, y := range ys[:64] {
		ck(ror32Asm(y, 11) == bits.RotateLeft32(y, -11), fmt.Sprintf("ror after GC y=%d", i))
	}

	finish("l08_asm_shift_rot")
}
