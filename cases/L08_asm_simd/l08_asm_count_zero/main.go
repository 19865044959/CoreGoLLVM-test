// L08: 手写 amd64 SSE2 统计切片中零字节数量，与 bytes.Count 对照；
// 覆盖全零、全非零、稀疏零、16 字节边界与 GC 后源数据稳定。
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

func countZeroAsm(p *byte, n int) int

func mkZeros(n int) []byte { return make([]byte, n) }

func mkOnes(n int) []byte {
	b := make([]byte, n)
	for i := range b {
		b[i] = 0xff
	}
	return b
}

func mkSparse(n, step, seed int) []byte {
	b := make([]byte, n)
	for i := range b {
		b[i] = byte(((i+seed)*17)&0xff) | 1 // 非零
		if i%step == 0 {
			b[i] = 0
		}
	}
	return b
}

func check(b []byte, label string) {
	want := bytes.Count(b, []byte{0})
	var p *byte
	if len(b) > 0 {
		p = &b[0]
	}
	got := countZeroAsm(p, len(b))
	ck(got == want, fmt.Sprintf("%s n=%d got=%d want=%d", label, len(b), got, want))
	if len(b) > 3 {
		sub := countZeroAsm(&b[2], len(b)-3)
		subWant := bytes.Count(b[2:len(b)-1], []byte{0})
		ck(sub == subWant, fmt.Sprintf("%s sub-slice n=%d", label, len(b)))
	}
	gcStress()
	ck(countZeroAsm(p, len(b)) == want, fmt.Sprintf("%s after GC n=%d", label, len(b)))
	runtime.KeepAlive(b)
}

func main() {
	for _, n := range []int{0, 1, 15, 16, 17, 32, 64, 255, 4096} {
		check(mkZeros(n), "all-zero")
		check(mkOnes(n), "all-nonzero")
		check(mkSparse(n, 3, n), "sparse")
		check(mkSparse(n, 7, n+5), "sparse2")
	}
	ck(countZeroAsm(nil, 0) == 0, "nil/0")
	finish("l08_asm_count_zero")
}
