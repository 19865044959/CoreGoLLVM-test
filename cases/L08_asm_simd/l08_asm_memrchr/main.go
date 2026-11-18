// L08: 手写 amd64 SSE2 反向字节扫描（memrchr/LastIndexByte 语义），
// 与 bytes.LastIndexByte 对照；覆盖多次出现、块边界、子切片与 GC。
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

func memrchrAsm(p *byte, n int, c int64) int

func callMemrchr(b []byte, c byte) int {
	var p *byte
	if len(b) > 0 {
		p = &b[0]
	}
	return memrchrAsm(p, len(b), int64(c))
}

func mkBuf(n, seed int) []byte {
	b := make([]byte, n)
	for i := range b {
		b[i] = byte((i*29 + seed*7) & 0x7f)
	}
	return b
}

func check(b []byte, c byte, label string) {
	want := bytes.LastIndexByte(b, c)
	got := callMemrchr(b, c)
	ck(got == want, fmt.Sprintf("%s n=%d c=%02x got=%d want=%d", label, len(b), c, got, want))
	bcopy := append([]byte(nil), b...)
	gcStress()
	got2 := callMemrchr(b, c)
	ck(got2 == want, fmt.Sprintf("%s after GC n=%d c=%02x got=%d want=%d", label, len(b), c, got2, want))
	ck(bytes.Equal(b, bcopy), fmt.Sprintf("%s mutated n=%d", label, len(b)))
	if len(b) > 5 {
		sub := callMemrchr(b[2:len(b)-1], c)
		subWant := bytes.LastIndexByte(b[2:len(b)-1], c)
		ck(sub == subWant, fmt.Sprintf("%s sub-slice n=%d c=%02x", label, len(b), c))
	}
	runtime.KeepAlive(b)
}

func main() {
	// 多次出现：最后一次必须命中。
	hay := bytes.Repeat([]byte("abXcdXefX"), 20)
	check(hay, 'X', "multi")
	check(hay, 'a', "multi-first")
	check(hay, 'f', "multi-last")
	check(hay, 'Z', "absent")

	for _, n := range []int{0, 1, 2, 15, 16, 17, 31, 32, 33, 64, 65, 257} {
		b := mkBuf(n, n+3)
		check(b, 0x00, fmt.Sprintf("plain-%d", n))
		check(b, 0x7f, fmt.Sprintf("plain-hi-%d", n))
		check(b, 0xee, fmt.Sprintf("absent-%d", n))
		if n >= 1 {
			// 首、中、末各放一个目标字节。
			for _, pos := range []int{0, n / 2, n - 1} {
				bb := append([]byte(nil), b...)
				bb[pos] = 0x5a
				check(bb, 0x5a, fmt.Sprintf("hit-%d-pos=%d", n, pos))
			}
			// 全同字节。
			check(bytes.Repeat([]byte{0xff}, n), 0xff, fmt.Sprintf("all-ff-%d", n))
			// 只有第一个字节命中。
			bb := append([]byte(nil), b...)
			bb[0] = 0x01
			if n > 1 {
				for i := 1; i < n; i++ {
					if bb[i] == 0x01 {
						bb[i] = 0x02
					}
				}
				check(bb, 0x01, fmt.Sprintf("first-only-%d", n))
			}
		}
	}

	ck(memrchrAsm(nil, 0, int64('x')) == -1, "nil/0")
	finish("l08_asm_memrchr")
}
