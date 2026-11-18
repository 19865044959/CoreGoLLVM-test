// L08: 手写 amd64 汇编按 8 字节重复模式填充缓冲（尾部按低字节序补齐），
// 与 Go 参考实现对照；用首尾哨兵检测越界写，并跨 GC 复核。
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

func fillPatternAsm(p *byte, n int, pat uint64)

func fillRef(b []byte, pat uint64) {
	for i := range b {
		b[i] = byte(pat >> (8 * uint(i%8)))
	}
}

const sentinel = 0xcc

func checkAt(n, off int, pat uint64, seed int) {
	buf := make([]byte, n+2)
	for i := range buf {
		buf[i] = sentinel
	}
	region := buf[1 : 1+n]
	fillPatternAsm(&buf[1], n, pat)

	want := make([]byte, n)
	fillRef(want, pat)
	ck(bytes.Equal(region, want), fmt.Sprintf("fill n=%d off=0 pat=%016x", n, pat))
	ck(buf[0] == sentinel && buf[n+1] == sentinel, fmt.Sprintf("fill sentinel n=%d", n))

	gcStress()
	ck(bytes.Equal(buf[1:1+n], want), fmt.Sprintf("fill after GC n=%d pat=%016x", n, pat))
	ck(buf[0] == sentinel && buf[n+1] == sentinel, fmt.Sprintf("fill sentinel after GC n=%d", n))
	runtime.KeepAlive(buf)
	_ = seed
}

func checkOffset(n, off int, pat uint64) {
	buf := make([]byte, off+n+2)
	for i := range buf {
		buf[i] = sentinel
	}
	fillPatternAsm(&buf[off], n, pat)

	want := make([]byte, n)
	fillRef(want, pat)
	ck(bytes.Equal(buf[off:off+n], want), fmt.Sprintf("fill off=%d n=%d pat=%016x", off, n, pat))
	ck(buf[off-1] == sentinel && buf[off+n] == sentinel, fmt.Sprintf("offset sentinel off=%d n=%d", off, n))
	gcStress()
	ck(bytes.Equal(buf[off:off+n], want), fmt.Sprintf("fill off=%d after GC n=%d", off, n))
	runtime.KeepAlive(buf)
}

func main() {
	pats := []uint64{
		0x0000000000000000,
		0x0102030405060708,
		0xffffffffffffffff,
		0xa5b6c7d8e9f00f1e,
		0xffff0000ffff0000,
	}
	for _, pat := range pats {
		for n := 0; n <= 70; n++ {
			checkAt(n, 0, pat, n+1)
		}
		checkAt(4096, 0, pat, 7)
	}
	for _, off := range []int{1, 3, 7, 8, 9} {
		for _, n := range []int{0, 1, 7, 8, 9, 511, 512} {
			checkOffset(n, off, 0x0102030405060708)
		}
	}
	fillPatternAsm(nil, 0, 0x0102030405060708)
	finish("l08_asm_fill_pattern")
}
