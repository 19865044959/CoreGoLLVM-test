// L08: 手写 amd64 SSE2 点积：PMADDWD 一次算 8 个 int16 乘加，
// 每块四个 int32 通道符号扩展到 64 位累加，尾部标量 IMULQ；
// 与 Go int64 点积对照，覆盖块边界与子切片。
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

func dotI16Asm(a, b *int16, n int) int64

func mk16(n, seed int) []int16 {
	s := make([]int16, n)
	for i := range s {
		s[i] = int16(((i+1)*(seed+3)*37)%601 - 300)
	}
	return s
}

func dotGo(a, b []int16) int64 {
	var t int64
	for i := range a {
		t += int64(a[i]) * int64(b[i])
	}
	return t
}

func ptr(s []int16) *int16 {
	if len(s) == 0 {
		return nil
	}
	return &s[0]
}

func main() {
	sizes := []int{0, 1, 2, 7, 8, 9, 15, 16, 17, 31, 32, 33, 63, 64, 65, 1000}
	for si, n := range sizes {
		a := mk16(n, si+1)
		b := mk16(n, si+7)
		want := dotGo(a, b)
		ck(dotI16Asm(ptr(a), ptr(b), n) == want, fmt.Sprintf("dot n=%d", n))
		gcStress()
		ck(dotGo(a, b) == want, fmt.Sprintf("source after GC n=%d", n))
		ck(dotI16Asm(ptr(a), ptr(b), n) == want, fmt.Sprintf("dot after GC n=%d", n))
		if n > 3 {
			got := dotI16Asm(&a[1], &b[1], n-2)
			ck(got == want-int64(a[0])*int64(b[0])-int64(a[n-1])*int64(b[n-1]),
				fmt.Sprintf("dot sub-slice n=%d", n))
		}
		runtime.KeepAlive(a)
		runtime.KeepAlive(b)
	}

	// 极值边界（每对乘积和单块通道不溢出 int32）。
	big := []int16{32767, -32768, 32767, -32768, 100, -100, 0, 1}
	big2 := []int16{2, 2, -2, -2, 3, 3, 0, -1}
	ck(dotI16Asm(ptr(big), ptr(big2), len(big)) == dotGo(big, big2), "int16 boundary dot")
	gcStress()
	ck(dotI16Asm(ptr(big), ptr(big2), len(big)) == dotGo(big, big2), "boundary dot after GC")
	ck(dotI16Asm(nil, nil, 0) == 0, "nil zero")

	finish("l08_asm_dot_i16")
}
