// L08: 手写 amd64 汇编乘法：MULQ 返回 128 位无符号乘积（hi,lo），
// IMULQ 做有符号低 64 位乘法；与 math/bits.Mul64 及 Go 乘法对照。
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

func mul128Asm(a, b uint64) (hi, lo uint64)
func imulAsm(a, b int64) int64

func main() {
	us := []uint64{
		0, 1, 2, 3, 0xffffffff, 1 << 32, (1 << 32) - 1,
		0x0123456789abcdef, 0xfedcba9876543210, ^uint64(0),
	}
	for i := 0; i < 300; i++ {
		us = append(us, uint64(i)*0x9e3779b97f4a7c15+uint64(i*i)*37+1)
	}
	for i := 0; i < 120; i++ {
		a := us[i]
		for _, j := range []int{0, 1, 2, 3, 17, 63, 64, 100, 299} {
			b := us[j]
			hi, lo := mul128Asm(a, b)
			whi, wlo := bits.Mul64(a, b)
			ck(hi == whi && lo == wlo,
				fmt.Sprintf("mul128 a=%d b=%d hi=%#x lo=%#x", i, j, hi, lo))
		}
	}

	ss := []int64{0, 1, -1, 2, -2, 1<<31 - 1, -(1 << 31), 1<<62 - 1, -1 << 62}
	for i := 0; i < 100; i++ {
		ss = append(ss, int64(i*i*2654435761-999999))
	}
	for i := 0; i < 80; i++ {
		for _, j := range []int{1, 2, 3, 4, 5, 50, 108} {
			a, b := ss[i], ss[j]
			ck(imulAsm(a, b) == a*b, fmt.Sprintf("imul a=%d b=%d", i, j))
		}
	}

	gcStress()
	a, b := uint64(0xdeadbeefcafebabe), uint64(0x123456789abcdef0)
	hi, lo := mul128Asm(a, b)
	whi, wlo := bits.Mul64(a, b)
	ck(hi == whi && lo == wlo, "mul128 after GC")
	ck(imulAsm(-123456789, 987654321) == -123456789*987654321, "imul after GC")

	finish("l08_asm_mul128")
}
