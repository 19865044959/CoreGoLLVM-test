// L08: 手写 amd64 汇编 64 位位反转（SWAR 位交换 + BSWAPQ），
// 与 math/bits.Reverse64 全量对照；覆盖边界值、伪随机值、对合性与 GC。
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

func bitrevAsm(x uint64) uint64

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
		0, 1, 2, 3, 0x8000000000000000, 0xffffffffffffffff,
		0x0102030405060708, 0x1122334455667788, 0xaaaaaaaaaaaaaaaa,
		0x5555555555555555, 0xf0f0f0f0f0f0f0f0, 0x0f0f0f0f0f0f0f0f,
		1 << 63, 1 << 32, 1 << 7, 0xff,
	}
	next := lcg(0x9e3779b97f4a7c15)
	for i := 0; i < 300; i++ {
		vals = append(vals, next())
	}

	for i, x := range vals {
		got := bitrevAsm(x)
		want := bits.Reverse64(x)
		ck(got == want, fmt.Sprintf("bitrev i=%d x=%016x got=%016x want=%016x", i, x, got, want))
		ck(bitrevAsm(got) == x, fmt.Sprintf("involution i=%d x=%016x", i, x))
	}

	// 位位置语义：bit k -> bit 63-k。
	for k := 0; k < 64; k++ {
		x := uint64(1) << uint(k)
		want := uint64(1) << uint(63-k)
		ck(bitrevAsm(x) == want, fmt.Sprintf("single bit k=%d got=%016x want=%016x", k, bitrevAsm(x), want))
	}

	// 每个字节内 0x01 应变为 0x80，且 byte 顺序整体反转。
	ck(bitrevAsm(0x0102030405060708) == 0x10e060a020c04080, "byte pattern")
	// 逐字节位反转再反转字节序，与整体位反转等价。
	byteRev := uint64(0)
	for i := 0; i < 8; i++ {
		b := byte(bitrevAsm(uint64((0x0102030405060708>>(8*uint(i)))&0xff)) >> 56)
		byteRev |= uint64(b) << (8 * uint(7-i))
	}
	ck(byteRev == bitrevAsm(0x0102030405060708), "bytewise equivalence")

	gcStress()
	for i := 0; i < 128 && i < len(vals); i++ {
		ck(bitrevAsm(vals[i]) == bits.Reverse64(vals[i]), fmt.Sprintf("bitrev after GC i=%d", i))
	}

	finish("l08_asm_bitrev_u64")
}
