// L08: 手写 amd64 汇编把字节缓冲编码为大写十六进制字符串。
// 通过 Go 侧传入的 16 字节查表做半字节映射，逐字节写两个字符；
// 与 strings.ToUpper(hex.EncodeToString) 对照，覆盖哨兵、往返解码与 GC。
package main

import (
	"bytes"
	"encoding/hex"
	"fmt"
	"os"
	"runtime"
	"strings"
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

func hexUpperAsm(src, dst, table *byte, n int)

var upperTable = []byte("0123456789ABCDEF")

func ptr(b []byte) *byte {
	if len(b) == 0 {
		return nil
	}
	return &b[0]
}

func filler(n, seed int) []byte {
	b := make([]byte, n)
	x := uint32(seed)*2654435761 + 11
	for i := range b {
		x = x*1664525 + 1013904223
		b[i] = byte(x >> 24)
	}
	return b
}

func check(src []byte, label string) {
	want := strings.ToUpper(hex.EncodeToString(src))
	dst := make([]byte, len(want)+2)
	dst[len(want)] = 0xcc
	dst[len(want)+1] = 0xdd
	hexUpperAsm(ptr(src), ptr(dst), &upperTable[0], len(src))
	got := dst[:len(want)]
	ck(string(got) == want, fmt.Sprintf("%s n=%d got=%q want=%q", label, len(src), got, want))
	ck(dst[len(want)] == 0xcc && dst[len(want)+1] == 0xdd,
		fmt.Sprintf("%s tail sentinel n=%d", label, len(src)))
	// 大写十六进制必须能解码回原文。
	dec, err := hex.DecodeString(string(got))
	ck(err == nil && bytes.Equal(dec, src), fmt.Sprintf("%s decode roundtrip n=%d err=%v", label, len(src), err))
	scopy := append([]byte(nil), src...)
	gcStress()
	dst2 := make([]byte, len(want))
	hexUpperAsm(ptr(src), ptr(dst2), &upperTable[0], len(src))
	ck(string(dst2) == want, fmt.Sprintf("%s after GC n=%d", label, len(src)))
	ck(bytes.Equal(src, scopy), fmt.Sprintf("%s src mutated", label))
	ck(string(upperTable) == "0123456789ABCDEF", "table mutated")
	runtime.KeepAlive(src)
	runtime.KeepAlive(dst)
}

func main() {
	// nil/空输入不写任何字节。
	var empty []byte
	dst := []byte{0xaa}
	hexUpperAsm(ptr(empty), ptr(dst), &upperTable[0], 0)
	ck(dst[0] == 0xaa, "empty input wrote dst")

	// 固定向量：覆盖 0x00、数字段、字母段与 0xff。
	fixed := []byte{0x00, 0x09, 0x0a, 0x0f, 0x10, 0x7f, 0x80, 0xab, 0xcd, 0xef, 0xf0, 0xff}
	check(fixed, "fixed")
	check([]byte{0xde, 0xad, 0xbe, 0xef}, "deadbeef")
	check([]byte{0x01, 0x23, 0x45, 0x67, 0x89, 0xab, 0xcd, 0xef}, "pattern")

	sizes := []int{0, 1, 2, 3, 15, 16, 17, 31, 32, 33, 63, 64, 65, 255, 256, 1024}
	for si, n := range sizes {
		src := filler(n, si+1)
		check(src, fmt.Sprintf("seq n=%d", n))
		if n > 4 {
			check(src[2:n-1], fmt.Sprintf("sub n=%d", n))
		}
	}

	// 全 0xff / 全 0x00。
	check(bytes.Repeat([]byte{0xff}, 33), "all-ff")
	check(make([]byte, 33), "all-zero")

	finish("l08_asm_hex_encode_upper")
}
