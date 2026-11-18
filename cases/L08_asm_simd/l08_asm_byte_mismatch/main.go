// L08: 手写 amd64 SSE2 统计两个字节缓冲的差异字节数（字节比较），
// 与纯 Go 逐字节比较对照；覆盖 16 字节块边界、差异位置、子切片与 GC。
package main

import (
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

func mismatchAsm(a, b *byte, n int) int

func mk(n, seed int) []byte {
	s := make([]byte, n)
	for i := range s {
		s[i] = byte((i*13 + seed*7) & 0xff)
	}
	return s
}

func mismatchGo(a, b []byte) int {
	d := 0
	for i := range a {
		if a[i] != b[i] {
			d++
		}
	}
	return d
}

func ptr(b []byte) *byte {
	if len(b) == 0 {
		return nil
	}
	return &b[0]
}

func check(n int, flips []int, seed int) {
	a := mk(n, seed)
	b := append([]byte(nil), a...)
	for _, p := range flips {
		if p >= 0 && p < n {
			b[p] ^= 0x5a
		}
	}
	want := mismatchGo(a, b)
	got := mismatchAsm(ptr(a), ptr(b), n)
	ck(got == want, fmt.Sprintf("mismatch n=%d flips=%v got=%d want=%d", n, flips, got, want))

	gcStress()
	ck(mismatchAsm(ptr(a), ptr(b), n) == want, fmt.Sprintf("mismatch after GC n=%d", n))
	ck(mismatchGo(a, b) == want, fmt.Sprintf("buffers after GC n=%d", n))

	if n > 4 {
		sub := mismatchAsm(&a[1], &b[1], n-3)
		subWant := mismatchGo(a[1:n-2], b[1:n-2])
		ck(sub == subWant, fmt.Sprintf("mismatch sub-slice n=%d", n))
	}
	runtime.KeepAlive(a)
	runtime.KeepAlive(b)
}

func main() {
	sizes := []int{0, 1, 2, 15, 16, 17, 31, 32, 33, 63, 64, 65, 255, 1000}
	for si, n := range sizes {
		check(n, nil, si+1)
		if n == 0 {
			continue
		}
		seen := map[int]bool{}
		var flips []int
		for _, p := range []int{0, n / 2, n - 1} {
			if !seen[p] {
				seen[p] = true
				flips = append(flips, p)
			}
		}
		check(n, flips, si+1)
		if n > 8 {
			check(n, []int{1, 2, 5, n/2 + 1, n - 3}, si+1)
		}
	}
	ck(mismatchAsm(nil, nil, 0) == 0, "mismatch nil/0")
	finish("l08_asm_byte_mismatch")
}
