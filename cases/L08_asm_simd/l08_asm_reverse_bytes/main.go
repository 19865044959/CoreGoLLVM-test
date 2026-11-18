// L08: 手写 amd64 汇编原地反转字节切片，双指针交换；
// 与 Go 参考实现对照，覆盖奇偶长度、子切片偏移、双重反转与 GC 稳定性。
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

func reverseAsm(p *byte, n int)

func reverseGo(b []byte) {
	for i, j := 0, len(b)-1; i < j; i, j = i+1, j-1 {
		b[i], b[j] = b[j], b[i]
	}
}

func mk(n, seed int) []byte {
	b := make([]byte, n)
	for i := range b {
		b[i] = byte((i*29 + seed*7) & 0xff)
	}
	return b
}

func ptr(b []byte) *byte {
	if len(b) == 0 {
		return nil
	}
	return &b[0]
}

func main() {
	sizes := []int{0, 1, 2, 3, 7, 8, 15, 16, 17, 31, 32, 33, 255, 256, 1000}
	for _, n := range sizes {
		src := mk(n, n+1)
		a := append([]byte(nil), src...)
		b := append([]byte(nil), src...)

		reverseGo(b)
		reverseAsm(ptr(a), n)
		ck(bytes.Equal(a, b), fmt.Sprintf("reverse n=%d", n))

		reverseAsm(ptr(a), n)
		ck(bytes.Equal(a, src), fmt.Sprintf("double reverse identity n=%d", n))
	}

	// 从非零偏移开始的子切片反转，哨兵不受影响。
	base := mk(300, 9)
	base[99] = 0xaa
	base[260] = 0xbb
	want := append([]byte(nil), base...)
	reverseGo(want[100:260])
	reverseAsm(&base[100], 160)
	ck(bytes.Equal(base, want), "sub-slice reverse")
	ck(base[99] == 0xaa && base[260] == 0xbb, "sub-slice sentinels")

	gcStress()
	snapshot := append([]byte(nil), base...)
	check := append([]byte(nil), base...)
	reverseAsm(ptr(check), len(check))
	reverseAsm(ptr(check), len(check))
	ck(bytes.Equal(check, snapshot), "double reverse after GC")
	reverseAsm(ptr(check), len(check))
	reverseGo(snapshot)
	ck(bytes.Equal(check, snapshot), "reverse after GC")
	runtime.KeepAlive(base)

	// 空输入不应解引用。
	reverseAsm(nil, 0)
	reverseAsm(nil, 1) // n=1 也不访问：mid 检查后直接返回
	finish("l08_asm_reverse_bytes")
}
