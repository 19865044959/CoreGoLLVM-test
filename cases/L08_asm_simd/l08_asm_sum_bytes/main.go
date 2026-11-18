// L08: 手写 amd64 SSE2 对字节切片求和（PSADBW 每块累加 16 字节），
// 与纯 Go 64 位累加对照；覆盖块边界、子切片、nil/0 与 GC 后源数据稳定。
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

func sumBytesAsm(p *byte, n int) int64

func mk(n, seed int) []byte {
	s := make([]byte, n)
	for i := range s {
		s[i] = byte((i*29 + seed*11) & 0xff)
	}
	return s
}

func sumGo(s []byte) int64 {
	var t int64
	for _, v := range s {
		t += int64(v)
	}
	return t
}

func ptr(b []byte) *byte {
	if len(b) == 0 {
		return nil
	}
	return &b[0]
}

func check(b []byte, label string, seed int) {
	want := sumGo(b)
	got := sumBytesAsm(ptr(b), len(b))
	ck(got == want, fmt.Sprintf("%s n=%d got=%d want=%d", label, len(b), got, want))

	gcStress()
	ck(sumGo(b) == want, fmt.Sprintf("%s source after GC n=%d", label, len(b)))
	ck(sumBytesAsm(ptr(b), len(b)) == want, fmt.Sprintf("%s after GC n=%d", label, len(b)))

	if len(b) > 4 {
		sub := sumBytesAsm(&b[2], len(b)-3)
		subWant := sumGo(b[2 : len(b)-1])
		ck(sub == subWant, fmt.Sprintf("%s sub-slice n=%d", label, len(b)))
	}
	runtime.KeepAlive(b)
}

func main() {
	for si, n := range []int{0, 1, 2, 15, 16, 17, 31, 32, 33, 63, 64, 65, 255, 1000, 4097} {
		check(mk(n, si+1), "seq", si+1)
	}
	// 极值填充：全 0xff 与全 0x00。
	all := make([]byte, 777)
	for i := range all {
		all[i] = 0xff
	}
	check(all, "all-0xff", 3)
	check(make([]byte, 4096), "all-zero", 4)

	ck(sumBytesAsm(nil, 0) == 0, "sum bytes nil/0")
	finish("l08_asm_sum_bytes")
}
