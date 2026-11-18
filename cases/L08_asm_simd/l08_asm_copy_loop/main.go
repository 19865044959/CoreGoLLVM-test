// L08: 手写 amd64 汇编正向拷贝循环（16B/8B/1B 分层），对照 bytes.Equal；
// 使用首尾哨兵检测越界写，并在 GC 后复核源/目标内容。
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

func copyAsm(dst, src *byte, n int) int

func mk(n, seed int) []byte {
	s := make([]byte, n)
	for i := range s {
		s[i] = byte((i*29 + seed) & 0xff)
	}
	return s
}

func main() {
	const sentinel = 0xEE
	for n := 0; n <= 70; n++ {
		src := mk(n, n+1)
		dst := make([]byte, n+2)
		for i := range dst {
			dst[i] = sentinel
		}
		var sp *byte
		if n > 0 {
			sp = &src[0]
		}
		got := copyAsm(&dst[1], sp, n)
		ck(got == n, fmt.Sprintf("copy return n=%d", n))
		ck(dst[0] == sentinel && dst[n+1] == sentinel, fmt.Sprintf("copy sentinel n=%d", n))
		ck(bytes.Equal(dst[1:1+n], src), fmt.Sprintf("copy content n=%d", n))

		gcStress()
		ck(bytes.Equal(dst[1:1+n], src), fmt.Sprintf("copy after GC n=%d", n))
		runtime.KeepAlive(src)
		runtime.KeepAlive(dst)
	}

	// dst == src：原地拷贝必须保持内容不变。
	for _, n := range []int{0, 1, 15, 16, 17, 64} {
		s := mk(n, 99)
		want := append([]byte(nil), s...)
		var p *byte
		if n > 0 {
			p = &s[0]
		}
		copyAsm(p, p, n)
		ck(bytes.Equal(s, want), fmt.Sprintf("self copy n=%d", n))
	}
	finish("l08_asm_copy_loop")
}
