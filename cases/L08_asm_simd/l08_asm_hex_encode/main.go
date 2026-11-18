// L08: 手写 amd64 汇编小写十六进制编码（nibble 分派，无查表），
// 与 encoding/hex 对照；覆盖哨兵越界写、子切片、源不可变与 GC。
package main

import (
	"bytes"
	"encoding/hex"
	"fmt"
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

func hexEncodeAsm(dst, src *byte, n int)

const canary = 0xa5

func encode(src []byte) []byte {
	n := len(src)
	dst := make([]byte, 2*n+8)
	for i := 2 * n; i < len(dst); i++ {
		dst[i] = canary
	}
	var sp, dp *byte
	if n > 0 {
		sp = &src[0]
		dp = &dst[0]
	}
	hexEncodeAsm(dp, sp, n)
	runtime.KeepAlive(src)
	runtime.KeepAlive(dst)
	return dst
}

func check(src []byte, label string) {
	orig := append([]byte(nil), src...)
	dst := encode(src)
	want := hex.EncodeToString(src)
	got := string(dst[:2*len(src)])
	ck(got == want, fmt.Sprintf("%s n=%d got=%q want=%q", label, len(src), got, want))
	for i := 2 * len(src); i < len(dst); i++ {
		ck(dst[i] == canary, fmt.Sprintf("%s canary offset=%d n=%d", label, i, len(src)))
	}
	ck(bytes.Equal(src, orig), fmt.Sprintf("%s src mutated n=%d", label, len(src)))

	gcStress()
	dst2 := encode(src)
	ck(string(dst2[:2*len(src)]) == want, fmt.Sprintf("%s after GC n=%d", label, len(src)))

	if len(src) > 5 {
		sub := src[2 : len(src)-1]
		dstSub := encode(sub)
		ck(string(dstSub[:2*len(sub)]) == hex.EncodeToString(sub), fmt.Sprintf("%s sub-slice n=%d", label, len(src)))
	}
}

func pattern(n int, f func(i int) byte) []byte {
	b := make([]byte, n)
	for i := range b {
		b[i] = f(i)
	}
	return b
}

func main() {
	for _, n := range []int{0, 1, 2, 15, 16, 17, 31, 32, 33, 255, 256, 1024} {
		check(nil, "nil")
		check([]byte{}, "empty")
		check(pattern(n, func(int) byte { return 0x00 }), fmt.Sprintf("zero-%d", n))
		check(pattern(n, func(int) byte { return 0xff }), fmt.Sprintf("ff-%d", n))
		check(pattern(n, func(i int) byte {
			if i%2 == 0 {
				return 0x0f
			}
			return 0xf0
		}), fmt.Sprintf("nibble-%d", n))
		check(pattern(n, func(i int) byte { return byte(i) }), fmt.Sprintf("seq-%d", n))
		check(pattern(n, func(i int) byte { return byte((i*73 + 11) & 0xff) }), fmt.Sprintf("mix-%d", n))
	}

	// 固定边界向量，确保 0..9 / a..f 两段分派都覆盖。
	fixed := []byte{0x00, 0x09, 0x0a, 0x0f, 0x10, 0x7f, 0x80, 0xab, 0xcd, 0xef, 0xf0, 0xff}
	check(fixed, "fixed")
	if got := encode(fixed); string(got[:2*len(fixed)]) != "00090a0f107f80abcdeff0ff" {
		ck(false, fmt.Sprintf("fixed golden got=%q", string(got[:2*len(fixed)])))
	}

	finish("l08_asm_hex_encode")
}
