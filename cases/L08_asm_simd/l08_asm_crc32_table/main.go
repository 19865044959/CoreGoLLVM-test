// L08: 手写 amd64 汇编实现查表法 CRC-32 IEEE（每字节一次表查找）。
// 与 hash/crc32.Update 对照，覆盖任意初始 crc 续算、标准向量、分块、
// 大输入与 GC；查表法区别于同目录的位串行 CRC 实现。
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

func crc32TableAsm(p *byte, n int, crc uint32, table *uint32) uint32

func ptr(b []byte) *byte {
	if len(b) == 0 {
		return nil
	}
	return &b[0]
}

func filler(n, seed int) []byte {
	b := make([]byte, n)
	x := uint32(seed)*2654435761 + 19
	for i := range b {
		x = x*1664525 + 1013904223
		b[i] = byte(x >> 24)
	}
	return b
}

func main() {
	tab := crc32.MakeTable(crc32.IEEE)
	tp := &tab[0]
	seeds := []uint32{0, 1, 0xffffffff, 0x12345678, 0xdeadbeef, 0x80000000}

	check := func(b []byte, seed uint32, label string) {
		want := crc32.Update(seed, tab, b)
		got := crc32TableAsm(ptr(b), len(b), seed, tp)
		ck(got == want, fmt.Sprintf("%s n=%d seed=%#x got=%#x want=%#x", label, len(b), seed, got, want))
		copyB := append([]byte(nil), b...)
		gcStress()
		got2 := crc32TableAsm(ptr(b), len(b), seed, tp)
		ck(got2 == want, fmt.Sprintf("%s after GC n=%d seed=%#x", label, len(b), seed))
		ck(bytes.Equal(b, copyB), fmt.Sprintf("%s source mutated", label))
		runtime.KeepAlive(b)
	}

	// 标准向量。
	std := []byte("123456789")
	ck(crc32TableAsm(ptr(std), len(std), 0, tp) == 0xcbf43926, "standard vector")
	ck(crc32.Update(0, tab, std) == 0xcbf43926, "stdlib standard vector")

	sizes := []int{0, 1, 2, 3, 15, 16, 17, 31, 32, 33, 255, 256, 1024, 4096}
	for si, n := range sizes {
		b := filler(n, si+1)
		for _, seed := range seeds {
			check(b, seed, "seq")
		}
		if n > 4 {
			check(b[2:n-1], 0x9e3779b9, "sub")
		}
	}
	check(bytes.Repeat([]byte{'a'}, 1000000), 0, "million-a")
	check(make([]byte, 65537), 0xffffffff, "zero-64k")

	// 分块续算：每段以上段结果作为初始 crc，最终值等于整段一次计算。
	full := filler(2376, 77)
	want := crc32.Update(0, tab, full)
	for _, step := range []int{1, 2, 7, 16, 33, 128, 509, 1024} {
		crc := uint32(0)
		for off := 0; off < len(full); off += step {
			end := off + step
			if end > len(full) {
				end = len(full)
			}
			chunk := full[off:end]
			crc = crc32TableAsm(ptr(chunk), len(chunk), crc, tp)
		}
		ck(crc == want, fmt.Sprintf("chunked step=%d got=%#x want=%#x", step, crc, want))
		ck(crc == crc32.Update(0, tab, full), fmt.Sprintf("chunked vs stdlib step=%d", step))
	}

	finish("l08_asm_crc32_table")
}
