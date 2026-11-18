// L08: 手写 amd64 SSE2 字节扫描（memchr 语义），与 bytes.IndexByte 对照，
// 覆盖 16 字节块边界、命中位置、未命中、空输入与 GC 后源字节稳定。
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

func memchrAsm(p *byte, n int, c int64) int

func mkBuf(n, seed int) []byte {
	b := make([]byte, n)
	for i := range b {
		b[i] = byte((i*7 + seed) & 0x3f)
	}
	return b
}

func checkAt(n, pos, seed int) {
	b := mkBuf(n, seed)
	if pos >= 0 {
		b[pos] = 'Q'
	}
	want := bytes.IndexByte(b, 'Q')
	var p *byte
	if n > 0 {
		p = &b[0]
	}
	got := memchrAsm(p, n, int64('Q'))
	ck(got == want, fmt.Sprintf("memchr n=%d pos=%d got=%d want=%d", n, pos, got, want))
	gcStress()
	ck(memchrAsm(p, n, int64('Q')) == want, fmt.Sprintf("memchr after GC n=%d pos=%d", n, pos))
	wantZ := bytes.IndexByte(b, 'Z')
	ck(memchrAsm(p, n, int64('Z')) == wantZ, fmt.Sprintf("memchr absent n=%d", n))
	runtime.KeepAlive(b)
}

func main() {
	sizes := []int{0, 1, 15, 16, 17, 31, 32, 33, 64, 65, 257}
	for si, n := range sizes {
		checkAt(n, -1, si+1)
		if n > 0 {
			seen := map[int]bool{}
			for _, pos := range []int{0, n / 2, n - 1} {
				if seen[pos] {
					continue
				}
				seen[pos] = true
				checkAt(n, pos, si+1)
			}
		}
	}
	ck(memchrAsm(nil, 0, int64('x')) == -1, "memchr nil/0")
	finish("l08_asm_memchr")
}
