// L08: 手写 amd64 汇编对 []uint32 求和（64 位累加，4 路展开），
// 覆盖 0xffffffff 溢出边界、子切片与 GC 后底层数组稳定。
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

func sumU32Asm(p *uint32, n int) uint64

func sumU32Go(s []uint32) uint64 {
	var t uint64
	for _, v := range s {
		t += uint64(v)
	}
	return t
}

func mk(n, seed int) []uint32 {
	s := make([]uint32, n)
	for i := range s {
		s[i] = uint32((uint64(i)*2654435761 + uint64(seed)*97) & 0xffffffff)
	}
	return s
}

func ptr(s []uint32) *uint32 {
	if len(s) == 0 {
		return nil
	}
	return &s[0]
}

func main() {
	sizes := []int{0, 1, 2, 3, 4, 7, 8, 15, 16, 17, 63, 64, 65, 1000, 100000}
	for si, n := range sizes {
		s := mk(n, si+1)
		want := sumU32Go(s)
		ck(sumU32Asm(ptr(s), n) == want, fmt.Sprintf("asm sum n=%d", n))
		gcStress()
		ck(sumU32Go(s) == want, fmt.Sprintf("source after GC n=%d", n))
		ck(sumU32Asm(ptr(s), n) == want, fmt.Sprintf("asm sum after GC n=%d", n))
		if n > 3 {
			got := sumU32Asm(&s[1], n-2)
			ck(got == want-uint64(s[0])-uint64(s[n-1]), fmt.Sprintf("sub-slice n=%d", n))
		}
		runtime.KeepAlive(s)
	}

	// 边界值：全 0xffffffff，64 位累加不得在 32 位回绕。
	maxv := make([]uint32, 100)
	for i := range maxv {
		maxv[i] = ^uint32(0)
	}
	ck(sumU32Asm(ptr(maxv), len(maxv)) == 100*uint64(^uint32(0)), "max u32 accumulation")
	gcStress()
	ck(sumU32Asm(ptr(maxv), len(maxv)) == 100*uint64(^uint32(0)), "max u32 after GC")
	ck(sumU32Asm(nil, 0) == 0, "nil zero")

	finish("l08_asm_sum_u32")
}
