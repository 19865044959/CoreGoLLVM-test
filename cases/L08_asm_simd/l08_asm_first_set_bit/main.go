// L08: 手写 amd64 汇编在 []uint64 中查找首个置位（BSFQ + 字下标换算），
// 与 Go 参考实现对照；覆盖全零字前缀、位 63、子切片、空输入与 GC。
package main

import (
	"fmt"
	"math/bits"
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

func firstSetBitAsm(p *uint64, n int) int64

func firstSetBitGo(s []uint64) int64 {
	for i, w := range s {
		if w != 0 {
			return int64(i*64 + bits.TrailingZeros64(w))
		}
	}
	return -1
}

func call(s []uint64) int64 {
	var p *uint64
	if len(s) > 0 {
		p = &s[0]
	}
	return firstSetBitAsm(p, len(s))
}

func check(s []uint64, label string) {
	want := firstSetBitGo(s)
	got := call(s)
	ck(got == want, fmt.Sprintf("%s n=%d got=%d want=%d", label, len(s), got, want))
	scopy := append([]uint64(nil), s...)
	gcStress()
	got2 := call(s)
	ck(got2 == want, fmt.Sprintf("%s after GC n=%d got=%d want=%d", label, len(s), got2, want))
	for i := range s {
		ck(s[i] == scopy[i], fmt.Sprintf("%s mutated i=%d", label, i))
	}
	if len(s) > 4 {
		gotSub := call(s[1 : len(s)-1])
		wantSub := firstSetBitGo(s[1 : len(s)-1])
		ck(gotSub == wantSub, fmt.Sprintf("%s sub-slice n=%d", label, len(s)))
	}
	runtime.KeepAlive(s)
}

func main() {
	check(nil, "nil")
	check([]uint64{}, "empty")
	check([]uint64{0}, "single-zero")
	check([]uint64{1}, "single-one")
	check([]uint64{0, 0, 0}, "multi-zero")

	// 零字前缀 + 单个置位，覆盖每个字位置。
	for w := 0; w < 8; w++ {
		for _, bit := range []int{0, 1, 31, 32, 62, 63} {
			s := make([]uint64, w+3)
			s[w] = uint64(1) << uint(bit)
			check(s, fmt.Sprintf("word=%d bit=%d", w, bit))
		}
	}

	// 首字含高位，确认不越过首个非零字。
	for bit := 0; bit < 64; bit++ {
		s := []uint64{uint64(1) << uint(bit), 0xffffffffffffffff}
		check(s, fmt.Sprintf("first-word-bit=%d", bit))
	}

	// 尾部字才出现置位。
	for w := 1; w <= 17; w++ {
		s := make([]uint64, w+1)
		s[w] = 0x8000000000000000
		check(s, fmt.Sprintf("tail-word=%d", w))
	}

	// 伪随机字，随机清零部分位。
	x := uint64(0xdeadbeefcafebabe)
	for r := 0; r < 40; r++ {
		s := make([]uint64, 1+r%13)
		for i := range s {
			x ^= x << 13
			x ^= x >> 7
			x ^= x << 17
			s[i] = x
			if r%3 == 0 {
				s[i] = 0
			}
		}
		check(s, fmt.Sprintf("random=%d", r))
	}

	finish("l08_asm_first_set_bit")
}
