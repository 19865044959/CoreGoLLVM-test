// L08: 手写 amd64 汇编循环对字节切片做 FNV-1a，与纯 Go 实现逐字节对照。
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

func hashAsm(p *byte, n int) uint64

func fnv1a(b []byte) uint64 {
	h := uint64(14695981039346656037)
	for _, c := range b {
		h ^= uint64(c)
		h *= 1099511628211
	}
	return h
}

func makeBytes(n int, seed int) []byte {
	b := make([]byte, n)
	x := uint32(seed*2654435761 + 1)
	for i := range b {
		x ^= x << 13
		x ^= x >> 17
		x ^= x << 5
		b[i] = byte(x >> 11)
	}
	return b
}

func main() {
	sizes := []int{0, 1, 2, 7, 15, 64, 255, 1000, 65536}
	for si, n := range sizes {
		b := makeBytes(n, si+1)
		var p *byte
		if len(b) > 0 {
			p = &b[0]
		}
		want := fnv1a(b)
		got := hashAsm(p, n)
		ck(got == want, fmt.Sprintf("asm fnv size=%d", n))

		gcStress()
		ck(fnv1a(b) == want, "source bytes after GC")
		ck(hashAsm(p, n) == want, fmt.Sprintf("asm fnv after GC size=%d", n))
		runtime.KeepAlive(b)
	}

	// 空输入：不得解引用 nil，必须返回 offset basis。
	ck(hashAsm(nil, 0) == 14695981039346656037, "asm fnv nil/0")
	finish("l08_asm_hash_loop")
}
