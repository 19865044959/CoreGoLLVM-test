// L08: 手写 amd64 SSE2 实现 bytes.LastIndex 语义：从最后一个可能起点倒扫，
// 用 PCMPEQB 筛出 needle 首字节候选，再用 BSRL 取块内最右候选并整段校验。
// 与 bytes.LastIndex 对照，覆盖块边界、重复子串、空 needle 与 GC。
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

func lastIndexSubAsm(hay *byte, n int, needle *byte, m int) int

func ptr(b []byte) *byte {
	if len(b) == 0 {
		return nil
	}
	return &b[0]
}

func filler(n int, seed int) []byte {
	b := make([]byte, n)
	x := uint32(seed)*2654435761 + 3
	for i := range b {
		x = x*1664525 + 1013904223
		b[i] = byte(x >> 24)
	}
	return b
}

func check(hay, needle []byte, label string) {
	want := bytes.LastIndex(hay, needle)
	got := lastIndexSubAsm(ptr(hay), len(hay), ptr(needle), len(needle))
	ck(got == want, fmt.Sprintf("%s hay=%d needle=%d got=%d want=%d", label, len(hay), len(needle), got, want))
	hcopy := append([]byte(nil), hay...)
	ncpy := append([]byte(nil), needle...)
	gcStress()
	got2 := lastIndexSubAsm(ptr(hay), len(hay), ptr(needle), len(needle))
	ck(got2 == want, fmt.Sprintf("%s after GC hay=%d needle=%d got=%d want=%d", label, len(hay), len(needle), got2, want))
	ck(bytes.Equal(hay, hcopy), fmt.Sprintf("%s hay mutated", label))
	ck(bytes.Equal(needle, ncpy), fmt.Sprintf("%s needle mutated", label))
	if len(hay) > 6 {
		sub := hay[2 : len(hay)-3]
		ck(lastIndexSubAsm(ptr(sub), len(sub), ptr(needle), len(needle)) == bytes.LastIndex(sub, needle),
			fmt.Sprintf("%s sub-slice", label))
	}
	runtime.KeepAlive(hay)
	runtime.KeepAlive(needle)
}

func main() {
	empty := []byte{}
	check(nil, empty, "nil-empty")
	check(empty, empty, "empty-empty")
	check([]byte("abc"), empty, "nonempty-empty")
	check(empty, []byte("ab"), "empty-nonempty")

	// 长度组合：needle 短于/等于/长于 hay，块边界附近。
	sizes := []int{1, 2, 3, 15, 16, 17, 31, 32, 33, 63, 64, 65, 127, 128, 257}
	for si, n := range sizes {
		hay := filler(n, si+1)
		for _, m := range []int{1, 2, 3, 5, 7, 8, 15, 16, 17} {
			if m > n {
				check(hay, filler(m, si+31), "needle-longer")
				continue
			}
			// 多次出现：首位/中位/末位都放入同样的 needle，LastIndex 必须取最后。
			needle := append([]byte(nil), hay[n-m:]...)
			pos := []int{0, n / 2, n - m}
			for _, p := range pos {
				if p+m > n {
					continue
				}
				copy(hay[p:], needle)
			}
			check(hay, needle, fmt.Sprintf("multi n=%d m=%d", n, m))
			// 最后一个候选失败、更早候选成功：制造尾部假候选。
			if n >= 2*m+1 {
				bad := append([]byte(nil), hay...)
				copy(bad[n-m:], needle)
				bad[n-1] ^= 0xff
				check(bad, needle, fmt.Sprintf("false-tail n=%d m=%d", n, m))
			}
		}
	}

	// 大量同首字节，压在候选块边界上。
	for _, n := range []int{15, 16, 17, 31, 32, 33, 63, 64, 65, 129} {
		hay := bytes.Repeat([]byte{'A'}, n)
		check(hay, []byte("AAB"), fmt.Sprintf("aaa-miss n=%d", n))
		if n >= 5 {
			copy(hay[n-3:], []byte("AAB"))
			check(hay, []byte("AAB"), fmt.Sprintf("aaa-hit n=%d", n))
		}
	}

	// 含 0x00 的 hay/needle。
	zero := make([]byte, 80)
	copy(zero[61:], []byte{0, 0, 9, 0, 0})
	check(zero, []byte{0, 9, 0}, "zero-needle")
	check(zero, []byte{0, 0, 9, 0, 0}, "zero-full")

	finish("l08_asm_last_index_sub")
}
