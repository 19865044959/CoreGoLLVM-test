// L08: 手写 amd64 SSE2 在字节缓冲中查找首个等于两个分隔字节之一的位置
// （HTTP header/JSON 空白扫描语义）；与 bytes.IndexByte 两次取较小值对照，
// 覆盖块边界、两字节相同、命中首位/末位、缺失、子切片与 GC。
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

func indexAny2Asm(p *byte, n int, b0, b1 byte) int

func indexAny2Go(b []byte, b0, b1 byte) int {
	i := bytes.IndexByte(b, b0)
	j := bytes.IndexByte(b, b1)
	if i < 0 {
		return j
	}
	if j < 0 || i < j {
		return i
	}
	return j
}

func ptr(b []byte) *byte {
	if len(b) == 0 {
		return nil
	}
	return &b[0]
}

func filler(n int, seed int) []byte {
	b := make([]byte, n)
	x := uint32(seed)*2654435761 + 7
	for i := range b {
		x = x*1664525 + 1013904223
		b[i] = byte(x>>24)&0x7f | 0x80 // 避开常见分隔字节
	}
	return b
}

func check(b []byte, b0, b1 byte, label string) {
	want := indexAny2Go(b, b0, b1)
	got := indexAny2Asm(ptr(b), len(b), b0, b1)
	ck(got == want, fmt.Sprintf("%s n=%d b0=%#x b1=%#x got=%d want=%d", label, len(b), b0, b1, got, want))
	buf := append([]byte(nil), b...)
	gcStress()
	got2 := indexAny2Asm(ptr(b), len(b), b0, b1)
	ck(got2 == want, fmt.Sprintf("%s after GC n=%d got=%d want=%d", label, len(b), got2, want))
	ck(bytes.Equal(b, buf), fmt.Sprintf("%s source mutated", label))
	if len(b) > 5 {
		sub := b[2 : len(b)-2]
		ck(indexAny2Asm(ptr(sub), len(sub), b0, b1) == indexAny2Go(sub, b0, b1),
			fmt.Sprintf("%s sub-slice n=%d", label, len(b)))
	}
	runtime.KeepAlive(b)
}

func main() {
	// 空输入与单字节。
	check(nil, '\r', '\n', "nil")
	check([]byte{}, 0, 0xff, "empty")
	check([]byte{'\r'}, '\r', '\n', "one-hit0")
	check([]byte{'\n'}, '\r', '\n', "one-hit1")
	check([]byte{'x'}, '\r', '\n', "one-miss")
	check([]byte{'x'}, 'x', 'x', "same-byte")

	// 两个分隔字节分别位于首位/中位/末位，覆盖 16 字节块与尾部。
	sizes := []int{1, 2, 3, 15, 16, 17, 31, 32, 33, 63, 64, 65, 127, 128, 255}
	for si, n := range sizes {
		base := filler(n, si+1)
		for _, p := range []int{0, n / 2, n - 1} {
			for vi, delim := range [][2]byte{{'\r', '\n'}, {0, 0xff}, {'x', 'x'}} {
				b := append([]byte(nil), base...)
				b[p] = delim[0]
				check(b, delim[0], delim[1], fmt.Sprintf("hit n=%d pos=%d v=%d", n, p, vi))
				if delim[0] != delim[1] && p > 0 {
					b[p] = delim[1]
					check(b, delim[0], delim[1], fmt.Sprintf("hit1 n=%d pos=%d v=%d", n, p, vi))
				}
			}
		}
		check(base, '\r', '\n', fmt.Sprintf("miss n=%d", n))
	}

	// 两个分隔字节同时出现：返回较小下标；b0/b1 传参顺序互换结果不变。
	b := filler(257, 99)
	b[3] = '\n'
	b[200] = '\r'
	check(b, '\r', '\n', "both-1")
	check(b, '\n', '\r', "both-2")
	b[3] = '\r'
	b[200] = '\n'
	check(b, '\r', '\n', "both-3")

	// 全为分隔字节时返回 0。
	for _, n := range []int{15, 16, 17, 64, 65} {
		all := bytes.Repeat([]byte{'\r'}, n)
		check(all, '\r', '\n', fmt.Sprintf("all-delim n=%d", n))
	}

	// 含 0x00 的分隔字节。
	zero := make([]byte, 40)
	zero[17] = 0
	check(zero, 0x01, 0x00, "zero-delim")

	finish("l08_asm_index_any2")
}
