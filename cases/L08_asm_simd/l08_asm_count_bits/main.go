// L08: 手写 amd64 汇编统计 uint64 切片的总置位数（BSFQ + x&(x-1) 清最低位），
// 与 math/bits.OnesCount64 累加对照；覆盖全 1/全 0/交替/稀疏/伪随机与 GC。
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

func countBitsAsm(p *uint64, n int) int64

func mk(n, seed int) []uint64 {
	s := make([]uint64, n)
	x := uint64(seed)*0x9e3779b97f4a7c15 + 1
	for i := range s {
		x ^= x << 13
		x ^= x >> 7
		x ^= x << 17
		v := x
		switch {
		case i%5 == 0:
			v = ^uint64(0)
		case i%7 == 0:
			v = 0
		case i%11 == 0:
			v = 0xaaaaaaaaaaaaaaaa
		case i%13 == 0:
			v = uint64(1) << uint(i%64)
		}
		s[i] = v
	}
	return s
}

func countGo(s []uint64) int64 {
	var t int64
	for _, v := range s {
		t += int64(bits.OnesCount64(v))
	}
	return t
}

func ptr(s []uint64) *uint64 {
	if len(s) == 0 {
		return nil
	}
	return &s[0]
}

func check(s []uint64, label string) {
	want := countGo(s)
	got := countBitsAsm(ptr(s), len(s))
	ck(got == want, fmt.Sprintf("%s n=%d got=%d want=%d", label, len(s), got, want))

	gcStress()
	ck(countBitsAsm(ptr(s), len(s)) == want, fmt.Sprintf("%s after GC n=%d", label, len(s)))
	ck(countGo(s) == want, fmt.Sprintf("%s source after GC n=%d", label, len(s)))

	if len(s) > 4 {
		sub := countBitsAsm(&s[1], len(s)-3)
		subWant := countGo(s[1 : len(s)-2])
		ck(sub == subWant, fmt.Sprintf("%s sub-slice n=%d", label, len(s)))
	}
	runtime.KeepAlive(s)
}

func main() {
	for si, n := range []int{0, 1, 2, 3, 7, 8, 9, 63, 64, 65, 1000, 4097} {
		check(mk(n, si+1), "mixed")
	}
	all := make([]uint64, 1000)
	for i := range all {
		all[i] = ^uint64(0)
	}
	check(all, "all-ones")
	check(make([]uint64, 1000), "all-zero")

	ck(countBitsAsm(nil, 0) == 0, "count bits nil/0")
	finish("l08_asm_count_bits")
}
