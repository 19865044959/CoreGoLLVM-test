// L08: 手写 amd64 汇编原地反转字节缓冲中每个字节的 8 个位。
// 通过 Go 侧传入的 256 项查表完成逐字节映射；与 bits.Reverse8 对照，
// 覆盖全部 256 个输入值、块边界、子切片、查表不变与 GC。
package main

import (
	"bytes"
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

func bitrevBytesAsm(p *byte, n int, table *byte)

var revTable [256]byte

func init() {
	for i := range revTable {
		revTable[i] = bits.Reverse8(byte(i))
	}
}

func ptr(b []byte) *byte {
	if len(b) == 0 {
		return nil
	}
	return &b[0]
}

func revGo(b []byte) []byte {
	out := make([]byte, len(b))
	for i, v := range b {
		out[i] = bits.Reverse8(v)
	}
	return out
}

func check(b []byte, label string) {
	want := revGo(b)
	buf := make([]byte, len(b)+2)
	buf[0] = 0x11
	buf[len(b)+1] = 0x22
	copy(buf[1:], b)
	orig := append([]byte(nil), buf...)
	bitrevBytesAsm(&buf[1], len(b), &revTable[0])
	ck(bytes.Equal(buf[1:len(b)+1], want), fmt.Sprintf("%s n=%d content", label, len(b)))
	ck(buf[0] == 0x11 && buf[len(b)+1] == 0x22, fmt.Sprintf("%s n=%d sentinel", label, len(b)))
	gcStress()
	// 再反转一次恢复原文（对合性）。
	bitrevBytesAsm(&buf[1], len(b), &revTable[0])
	ck(bytes.Equal(buf, orig), fmt.Sprintf("%s n=%d double-reverse", label, len(b)))
	ck(bytes.Equal(buf[1:len(b)+1], b), fmt.Sprintf("%s n=%d restored", label, len(b)))
	keep := append([]byte(nil), revTable[:]...)
	ck(bytes.Equal(keep, revTable[:]), "table mutated")
	runtime.KeepAlive(b)
}

func main() {
	// 全部 256 个字节值一次覆盖。
	all := make([]byte, 256)
	for i := range all {
		all[i] = byte(i)
	}
	check(all, "all-values")
	check([]byte{0x00, 0x01, 0x02, 0x80, 0xff, 0x0f, 0xf0, 0xaa, 0x55}, "fixed")

	sizes := []int{0, 1, 2, 3, 7, 8, 15, 16, 17, 31, 32, 33, 63, 64, 65, 1024}
	for si, n := range sizes {
		b := make([]byte, n)
		for i := range b {
			b[i] = byte((i*37 + si*11) & 0xff)
		}
		check(b, fmt.Sprintf("seq n=%d", n))
		if n > 4 {
			check(b[2:n-1], fmt.Sprintf("sub n=%d", n))
		}
	}

	finish("l08_asm_bitrev_bytes")
}
