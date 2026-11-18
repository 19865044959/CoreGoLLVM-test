// L08: 手写 amd64 汇编位串行 CRC-32（IEEE，反射多项式），
// 与 hash/crc32.Update 对照；覆盖标准向量、初始 crc 续算、分块链式更新与 GC。
package main

import (
	"bytes"
	"fmt"
	"hash/crc32"
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

func crc32Asm(p *byte, n int, crc uint32) uint32

func callCRC(b []byte, crc uint32) uint32 {
	var p *byte
	if len(b) > 0 {
		p = &b[0]
	}
	return crc32Asm(p, len(b), crc)
}

func mkBuf(n, seed int) []byte {
	b := make([]byte, n)
	x := uint32(seed)*2862933559 + 3037000499
	for i := range b {
		x = x*1664525 + 1013904223
		b[i] = byte(x >> 17)
	}
	return b
}

func check(b []byte, crc uint32, label string) {
	want := crc32.Update(crc, crc32.IEEETable, b)
	got := callCRC(b, crc)
	ck(got == want, fmt.Sprintf("%s n=%d crc=%08x got=%08x want=%08x", label, len(b), crc, got, want))
	bcopy := append([]byte(nil), b...)
	gcStress()
	got2 := callCRC(b, crc)
	ck(got2 == want, fmt.Sprintf("%s after GC n=%d crc=%08x got=%08x", label, len(b), crc, got2))
	ck(bytes.Equal(b, bcopy), fmt.Sprintf("%s src mutated n=%d", label, len(b)))
	runtime.KeepAlive(b)
}

func main() {
	// 标准向量：CRC-32("123456789") = 0xcbf43926。
	std := []byte("123456789")
	ck(callCRC(std, 0) == 0xcbf43926, fmt.Sprintf("standard vector got=%08x", callCRC(std, 0)))
	check(std, 0, "standard")
	check(std, 0xffffffff, "standard-ff")
	check(std, 0x12345678, "standard-seed")
	check(bytes.Repeat([]byte("a"), 1000000), 0, "million-a")

	seeds := []uint32{0, 1, 0xffffffff, 0x12345678, 0xdeadbeef, 0x80000000}
	sizes := []int{0, 1, 2, 3, 15, 16, 17, 31, 32, 33, 255, 256, 1024, 4096}
	for si, n := range sizes {
		b := mkBuf(n, si+1)
		for _, seed := range seeds {
			check(b, seed, fmt.Sprintf("buf-%d", n))
		}
		check(bytes.Repeat([]byte{0x00}, n), 0, fmt.Sprintf("zeros-%d", n))
		check(bytes.Repeat([]byte{0xff}, n), 0xffffffff, fmt.Sprintf("ones-%d", n))
	}

	// 分块链式续算：每段都以前一段结果作为初始 crc。
	full := mkBuf(4096, 99)
	want := crc32.Update(0, crc32.IEEETable, full)
	got := uint32(0)
	off := 0
	for _, step := range []int{1, 2, 7, 16, 33, 128, 509, 1024, 2376} {
		if off+step > len(full) {
			break
		}
		got = callCRC(full[off:off+step], got)
		off += step
	}
	ck(off == len(full), fmt.Sprintf("chunk coverage off=%d", off))
	ck(got == want, fmt.Sprintf("chunk chained got=%08x want=%08x", got, want))
	gcStress()
	ck(callCRC(full, 0) == want, "chunk recheck after GC")

	// 空输入必须原样返回初始 crc。
	for _, seed := range seeds {
		ck(callCRC(nil, seed) == seed, fmt.Sprintf("empty seed=%08x got=%08x", seed, callCRC(nil, seed)))
	}

	finish("l08_asm_crc32_bitwise")
}
