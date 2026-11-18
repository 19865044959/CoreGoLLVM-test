// L08: 手写 amd64 汇编在 []uint64 位图中查找“>= 起始位的首个置位”。
// 首字用可变移位掩掉起始位之前的位，之后逐字扫描，BSFQ 定位最低置位；
// 与 Go 逐位参考实现对照，覆盖 63/64 边界、末尾、全零与越界起点。
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

func firstSetFromAsm(p *uint64, n int, start int) int

func firstSetFromGo(words []uint64, start int) int {
	if start < 0 {
		start = 0
	}
	total := len(words) * 64
	if start >= total {
		return -1
	}
	for i := start; i < total; i++ {
		if (words[i>>6]>>uint(i&63))&1 == 1 {
			return i
		}
	}
	return -1
}

func ptr(w []uint64) *uint64 {
	if len(w) == 0 {
		return nil
	}
	return &w[0]
}

func check(words []uint64, label string) {
	total := len(words) * 64
	starts := []int{-70, -1, 0, 1, 31, 32, 33, 62, 63, 64, 65, 127, 128, 129}
	if total > 0 {
		starts = append(starts, total-2, total-1, total, total+1, total+64)
	}
	for _, s := range starts {
		want := firstSetFromGo(words, s)
		got := firstSetFromAsm(ptr(words), len(words), s)
		ck(got == want, fmt.Sprintf("%s n=%d start=%d got=%d want=%d", label, len(words), s, got, want))
	}
	copyW := append([]uint64(nil), words...)
	gcStress()
	for _, s := range []int{0, 63, 64, total - 1} {
		if s < 0 {
			continue
		}
		ck(firstSetFromAsm(ptr(words), len(words), s) == firstSetFromGo(words, s),
			fmt.Sprintf("%s after GC start=%d", label, s))
	}
	ck(fmt.Sprint(words) == fmt.Sprint(copyW), fmt.Sprintf("%s source mutated", label))
	runtime.KeepAlive(words)
}

func main() {
	check(nil, "nil")
	check([]uint64{}, "empty")
	check([]uint64{0}, "single-zero")
	check([]uint64{0, 0, 0}, "all-zero")
	check([]uint64{^uint64(0)}, "single-full")

	// 单个置位位于各个字/位位置：含字边界 63/64、末尾 bit。
	for _, nw := range []int{1, 2, 3, 5} {
		total := nw * 64
		for _, bit := range []int{0, 1, 31, 62, 63, 64, 65, 127, total - 2, total - 1} {
			if bit < 0 || bit >= total {
				continue
			}
			w := make([]uint64, nw)
			w[bit>>6] = uint64(1) << uint(bit&63)
			check(w, fmt.Sprintf("single nw=%d bit=%d", nw, bit))
		}
	}

	// 混合位图：交替、稀疏、末尾全 1。
	for _, nw := range []int{2, 4, 8, 9} {
		w := make([]uint64, nw)
		for i := range w {
			w[i] = 0xaaaaaaaaaaaaaaaa
		}
		check(w, fmt.Sprintf("alt nw=%d", nw))
		w[nw-1] = 1 << 63
		check(w, fmt.Sprintf("tailhigh nw=%d", nw))
		for i := range w {
			w[i] = 0
		}
		w[0] = 1 << 63
		w[nw-1] = 1
		check(w, fmt.Sprintf("sparse nw=%d", nw))
	}

	// 定种子伪随机位图。
	x := uint64(0x243f6a8885a308d3)
	for gi := 0; gi < 50; gi++ {
		nw := 1 + gi%7
		w := make([]uint64, nw)
		for i := range w {
			x = x*6364136223846793005 + 1442695040888963407
			w[i] = x
		}
		check(w, fmt.Sprintf("rand gi=%d", gi))
	}

	finish("l08_asm_first_set_from")
}
