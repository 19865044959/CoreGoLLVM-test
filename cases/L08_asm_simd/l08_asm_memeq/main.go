// L08: 手写 amd64 SSE2 等长字节比较，与 bytes.Equal 对照；
// 覆盖首/中/末字节差异、16 字节块边界、空输入以及 GC 后源数据稳定。
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

func memeqAsm(a, b *byte, n int) bool

func mk(n, seed int) []byte {
	s := make([]byte, n)
	for i := range s {
		s[i] = byte((i*13 + seed) & 0xff)
	}
	return s
}

func ptr(b []byte) *byte {
	if len(b) == 0 {
		return nil
	}
	return &b[0]
}

func main() {
	sizes := []int{0, 1, 7, 15, 16, 17, 32, 33, 100}
	for si, n := range sizes {
		a := mk(n, si+1)
		b := append([]byte(nil), a...)
		ck(memeqAsm(ptr(a), ptr(b), n), fmt.Sprintf("equal n=%d", n))
		ck(bytes.Equal(a, b), "go equal sanity")

		if n > 0 {
			seen := map[int]bool{}
			for _, pos := range []int{0, n / 2, n - 1} {
				if seen[pos] {
					continue
				}
				seen[pos] = true
				b[pos] ^= 0x5a
				ck(!memeqAsm(ptr(a), ptr(b), n), fmt.Sprintf("differ n=%d pos=%d", n, pos))
				ck(bytes.Equal(a, b) == false, "go differ sanity")
				b[pos] ^= 0x5a
			}
		}

		c := mk(n, si+2)
		ck(bytes.Equal(a, c) == memeqAsm(ptr(a), ptr(c), n), fmt.Sprintf("vs go n=%d", n))

		gcStress()
		ck(memeqAsm(ptr(a), ptr(b), n), fmt.Sprintf("equal after GC n=%d", n))
		ck(bytes.Equal(a, b), "go equal after GC")
		runtime.KeepAlive(a)
		runtime.KeepAlive(b)
	}
	ck(memeqAsm(nil, nil, 0), "empty nil equal")
	finish("l08_asm_memeq")
}
