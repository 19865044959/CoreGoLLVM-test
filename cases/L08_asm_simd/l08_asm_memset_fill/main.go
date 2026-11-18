// L08: 手写 amd64 汇编填充字节（memset 语义）：把单字节广播成 8 字节字
// 后按 8 字节推进，尾部逐字节写；用首尾哨兵检测越界写，GC 后复核。
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

func memsetAsm(p *byte, n int, v byte)

func main() {
	buf := make([]byte, 200)
	for _, v := range []byte{0x00, 0x5a, 0xff, 0x7f} {
		for n := 0; n <= 70; n++ {
			for i := range buf {
				buf[i] = 0xcc
			}
			memsetAsm(&buf[32], n, v)
			ck(buf[31] == 0xcc, fmt.Sprintf("head sentinel n=%d v=%#x", n, v))
			ck(buf[32+n] == 0xcc, fmt.Sprintf("tail sentinel n=%d v=%#x", n, v))
			ck(bytes.Equal(buf[32:32+n], bytes.Repeat([]byte{v}, n)),
				fmt.Sprintf("fill n=%d v=%#x", n, v))
		}
	}

	// 子切片起始偏移 + 大块填充（含 8 字节对齐/非对齐起始）。
	big := make([]byte, 4096)
	for i := range big {
		big[i] = 0x11
	}
	for _, off := range []int{0, 1, 7, 8, 9} {
		for _, n := range []int{0, 1, 7, 8, 9, 63, 64, 65, 1000, 4000} {
			for i := range big {
				big[i] = 0x11
			}
			memsetAsm(&big[off], n, 0xa5)
			ck(bytes.Equal(big[off:off+n], bytes.Repeat([]byte{0xa5}, n)),
				fmt.Sprintf("big fill off=%d n=%d", off, n))
			if off > 0 {
				ck(big[off-1] == 0x11, fmt.Sprintf("big head sentinel off=%d n=%d", off, n))
			}
			ck(big[off+n] == 0x11, fmt.Sprintf("big tail sentinel off=%d n=%d", off, n))
		}
	}

	gcStress()
	for i := range big {
		big[i] = 0
	}
	memsetAsm(&big[13], 777, 0x3c)
	ck(bytes.Equal(big[13:790], bytes.Repeat([]byte{0x3c}, 777)), "fill after GC")
	ck(big[12] == 0 && big[790] == 0, "sentinels after GC")
	memsetAsm(nil, 0, 0xff)

	finish("l08_asm_memset_fill")
}
