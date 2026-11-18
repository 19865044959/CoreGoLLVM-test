// L08: 手写 amd64 SSE2 查找首个非零字节：PCMPEQB 与全零向量比较，
// PMOVMSKB + 取反 + BSFL 定位，尾部标量扫描；与 Go 实现对照。
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

func firstNonzeroAsm(p *byte, n int) int

func firstNonzeroGo(b []byte) int {
	for i, v := range b {
		if v != 0 {
			return i
		}
	}
	return -1
}

func ptr(b []byte) *byte {
	if len(b) == 0 {
		return nil
	}
	return &b[0]
}

func main() {
	sizes := []int{0, 1, 2, 15, 16, 17, 31, 32, 33, 63, 64, 65, 255, 256, 4096}
	for _, n := range sizes {
		// 全零
		z := make([]byte, n)
		ck(firstNonzeroAsm(ptr(z), n) == -1, fmt.Sprintf("all zero n=%d", n))

		if n == 0 {
			continue
		}
		// 单个非零位于首/中/末。
		for _, pos := range []int{0, n / 2, n - 1} {
			b := make([]byte, n)
			b[pos] = byte(0x80 + pos%127)
			got := firstNonzeroAsm(ptr(b), n)
			ck(got == pos, fmt.Sprintf("single nonzero n=%d pos=%d got=%d", n, pos, got))
			ck(got == firstNonzeroGo(b), fmt.Sprintf("single vs go n=%d pos=%d", n, pos))
		}
		// 稀疏零：每 5 字节一个非零，覆盖块边界。
		b := make([]byte, n)
		for i := 0; i < n; i += 5 {
			b[i] = byte(i + 1)
		}
		ck(firstNonzeroAsm(ptr(b), n) == firstNonzeroGo(b), fmt.Sprintf("sparse n=%d", n))
		// 块内 16 偏移处的非零（尾部路径）。
		c := make([]byte, n)
		last := n - 1
		if last > 16 {
			last = 16
		}
		c[last] = 1
		ck(firstNonzeroAsm(ptr(c), n) == firstNonzeroGo(c), fmt.Sprintf("block tail n=%d", n))
	}

	// 子切片偏移。
	base := make([]byte, 600)
	base[333] = 0x7e
	ck(firstNonzeroAsm(&base[64], 300) == 333-64, "sub-slice first nonzero")
	ck(firstNonzeroAsm(&base[334], 200) == -1, "sub-slice all zero")

	gcStress()
	check := make([]byte, 1000)
	check[777] = 9
	ck(firstNonzeroAsm(ptr(check), len(check)) == 777, "after GC")
	ck(firstNonzeroGo(check) == 777, "go after GC")
	runtime.KeepAlive(base)

	finish("l08_asm_first_nonzero")
}
