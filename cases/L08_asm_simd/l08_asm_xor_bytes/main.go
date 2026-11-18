// L08: 手写 amd64 SSE2 双缓冲异或（PXOR），模拟加密/XOR 混淆热路径；
// 覆盖块边界、容量哨兵、源不可变、原地别名与 GC。
package main

import (
	"bytes"
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

func xorAsm(dst, src *byte, n int)

const canary = 0x5c

func mkBuf(n, seed int) []byte {
	b := make([]byte, n)
	x := uint32(seed)*2246822519 + 3266489917
	for i := range b {
		x = x*1664525 + 1013904223
		b[i] = byte(x >> 19)
	}
	return b
}

func check(a, b []byte, label string) {
	n := len(a)
	dst := make([]byte, n+8)
	copy(dst, a)
	for i := n; i < len(dst); i++ {
		dst[i] = canary
	}
	src := append([]byte(nil), b...)
	want := append([]byte(nil), a...)
	for i := 0; i < n; i++ {
		want[i] ^= b[i]
	}
	var dp, sp *byte
	if n > 0 {
		dp = &dst[0]
		sp = &src[0]
	}
	xorAsm(dp, sp, n)
	ck(bytes.Equal(dst[:n], want), fmt.Sprintf("%s n=%d result", label, n))
	for i := n; i < len(dst); i++ {
		ck(dst[i] == canary, fmt.Sprintf("%s canary off=%d n=%d", label, i, n))
	}
	ck(bytes.Equal(src, b), fmt.Sprintf("%s src mutated n=%d", label, n))

	gcStress()
	dst2 := make([]byte, n)
	copy(dst2, a)
	var d2, s2 *byte
	if n > 0 {
		d2 = &dst2[0]
		s2 = &src[0]
	}
	xorAsm(d2, s2, n)
	ck(bytes.Equal(dst2, want), fmt.Sprintf("%s after GC n=%d", label, n))

	if n > 6 {
		// 子切片偏移，互不重叠。
		sub := make([]byte, n-5)
		copy(sub, a[3:])
		var dp3, sp3 *byte
		if len(sub) > 0 {
			dp3 = &sub[0]
			sp3 = &src[2]
		}
		xorAsm(dp3, sp3, len(sub))
		wantSub := append([]byte(nil), a[3:3+len(sub)]...)
		for i := range wantSub {
			wantSub[i] ^= src[2+i]
		}
		ck(bytes.Equal(sub, wantSub), fmt.Sprintf("%s sub-slice n=%d", label, n))
	}
	runtime.KeepAlive(dst)
	runtime.KeepAlive(src)
}

func main() {
	for _, n := range []int{0, 1, 2, 7, 8, 15, 16, 17, 31, 32, 33, 63, 64, 65, 255, 256, 1024} {
		for seed := 1; seed <= 3; seed++ {
			check(mkBuf(n, seed), mkBuf(n, seed*37+5), fmt.Sprintf("pair-%d-%d", n, seed))
		}
		check(mkBuf(n, 9), bytes.Repeat([]byte{0xff}, n), fmt.Sprintf("all-ff-%d", n))
		check(bytes.Repeat([]byte{0x00}, n), mkBuf(n, 11), fmt.Sprintf("zero-dst-%d", n))
	}

	// 原地别名：dst == src 时结果必须全零。
	for _, n := range []int{0, 1, 15, 16, 17, 255} {
		buf := mkBuf(n, 21)
		var p *byte
		if n > 0 {
			p = &buf[0]
		}
		xorAsm(p, p, n)
		ck(bytes.Equal(buf, make([]byte, n)), fmt.Sprintf("alias n=%d", n))
	}

	// 两次异或恢复原文（对合性）。
	orig := mkBuf(1000, 77)
	stream := mkBuf(1000, 78)
	buf := append([]byte(nil), orig...)
	xorAsm(&buf[0], &stream[0], len(buf))
	ck(!bytes.Equal(buf, orig), "xor changed data")
	xorAsm(&buf[0], &stream[0], len(buf))
	ck(bytes.Equal(buf, orig), "xor involution")

	xorAsm(nil, nil, 0)
	finish("l08_asm_xor_bytes")
}
