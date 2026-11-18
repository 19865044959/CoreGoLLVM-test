// L08: 手写 amd64 汇编对 int64 切片求和（4 路展开），与纯 Go 求和对照，
// 覆盖 slice 底层数组指针 + 长度传参、子切片偏移与 GC 后的数据有效性。
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

func sumI64Asm(p *int64, n int) int64

func mk(n, seed int) []int64 {
	s := make([]int64, n)
	for i := range s {
		s[i] = int64(((i+seed)*37)%1000) - 500
	}
	return s
}

func sumGo(s []int64) int64 {
	var t int64
	for _, v := range s {
		t += v
	}
	return t
}

func main() {
	sizes := []int{0, 1, 3, 7, 16, 63, 64, 1000, 100000}
	for si, n := range sizes {
		s := mk(n, si+1)
		want := sumGo(s)
		var p *int64
		if n > 0 {
			p = &s[0]
		}
		ck(sumI64Asm(p, n) == want, fmt.Sprintf("asm sum size=%d", n))

		gcStress()
		ck(sumGo(s) == want, fmt.Sprintf("source slice after GC size=%d", n))
		ck(sumI64Asm(p, n) == want, fmt.Sprintf("asm sum after GC size=%d", n))

		if n > 3 {
			got := sumI64Asm(&s[1], n-2)
			ck(got == want-s[0]-s[n-1], fmt.Sprintf("asm sub-slice sum size=%d", n))
		}
		runtime.KeepAlive(s)
	}
	finish("l08_asm_slice_sum")
}
