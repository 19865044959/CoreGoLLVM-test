// L08: 手写 amd64 SSE2 子串查找（bytes.Index 语义），
// 16 字节块筛首字节候选后整段校验；覆盖真/假候选、块边界、空 needle 与 GC。
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

func indexAsm(hay *byte, n int, needle *byte, m int) int

func callIndex(hay, needle []byte) int {
	var hp, np *byte
	if len(hay) > 0 {
		hp = &hay[0]
	}
	if len(needle) > 0 {
		np = &needle[0]
	}
	return indexAsm(hp, len(hay), np, len(needle))
}

func check(hay, needle []byte, label string) {
	want := bytes.Index(hay, needle)
	got := callIndex(hay, needle)
	ck(got == want, fmt.Sprintf("%s hay=%d needle=%d got=%d want=%d", label, len(hay), len(needle), got, want))
	// 源数据不得被修改。
	hcopy := append([]byte(nil), hay...)
	ncpy := append([]byte(nil), needle...)
	gcStress()
	got2 := callIndex(hay, needle)
	ck(got2 == want, fmt.Sprintf("%s after GC hay=%d needle=%d got=%d want=%d", label, len(hay), len(needle), got2, want))
	ck(bytes.Equal(hay, hcopy), fmt.Sprintf("%s hay mutated", label))
	ck(bytes.Equal(needle, ncpy), fmt.Sprintf("%s needle mutated", label))
	if len(hay) > 4 {
		gotSub := callIndex(hay[1:len(hay)-2], needle)
		wantSub := bytes.Index(hay[1:len(hay)-2], needle)
		ck(gotSub == wantSub, fmt.Sprintf("%s sub-slice", label))
	}
	runtime.KeepAlive(hay)
	runtime.KeepAlive(needle)
}

func filler(n, seed int) []byte {
	b := make([]byte, n)
	x := uint32(seed)*2654435761 + 1
	for i := range b {
		x = x*1664525 + 1013904223
		b[i] = byte(x >> 24)
	}
	return b
}

func main() {
	empty := []byte{}
	check(nil, empty, "nil-empty")
	check(empty, empty, "empty-empty")
	check([]byte("abc"), empty, "nonempty-empty")
	check(empty, []byte("ab"), "empty-nonempty")

	sizes := []int{1, 2, 3, 15, 16, 17, 31, 32, 33, 64, 65, 127, 257}
	for si, n := range sizes {
		hay := filler(n, si+1)
		for _, m := range []int{1, 2, 3, 4, 5, 7, 8, 15, 16, 17} {
			if m > n {
				check(hay, filler(m, si+31), "needle-longer")
				continue
			}
			// 命中位置：首位、中位、末位。
			pos := []int{0, n / 2, n - m}
			seenPos := map[int]bool{}
			for pi, p := range pos {
				if p < 0 || p+m > n || seenPos[p] {
					continue
				}
				seenPos[p] = true
				needle := append([]byte(nil), hay[p:p+m]...)
				check(hay, needle, fmt.Sprintf("hit size=%d m=%d pos=%d/%d", n, m, p, pi))
				// 破坏 needle 最后一字节，制造首字节命中但整段不匹配。
				if m > 1 {
					bad := append([]byte(nil), needle...)
					bad[m-1] ^= 0xff
					check(hay, bad, fmt.Sprintf("false-positive size=%d m=%d pos=%d", n, m, p))
				}
			}
		}
	}

	// 大量同首字节的假候选，压在 16 字节块边界上。
	for _, n := range []int{15, 16, 17, 31, 32, 33, 63, 64, 65} {
		hay := bytes.Repeat([]byte{'A'}, n)
		check(hay, []byte("AAB"), fmt.Sprintf("aaa-miss n=%d", n))
		if n >= 5 {
			hay[n-3] = 'B'
			check(hay, []byte("AAB"), fmt.Sprintf("aaa-boundary-hit n=%d", n))
			check(hay, []byte("AABB"), fmt.Sprintf("aaa-boundary-miss n=%d", n))
		}
	}

	// 含 0x00 的 needle 与 hay。
	zeroHay := make([]byte, 40)
	copy(zeroHay[17:], []byte{0, 0, 9, 0, 0})
	check(zeroHay, []byte{0, 9, 0}, "zero-needle")

	finish("l08_asm_bytes_index")
}
