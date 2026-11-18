// L08: 手写 amd64 汇编实现 memmove 语义的拷贝（重叠区间按方向选择正向/反向），
// 与 Go 内建 copy 对照；覆盖左/右重叠、原地、非重叠、块边界与 GC。
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

func moveAsm(dst, src *byte, n int)

func mk(n, seed int) []byte {
	s := make([]byte, n)
	for i := range s {
		s[i] = byte((i*31 + seed*17) & 0xff)
	}
	return s
}

func run(label string, size, srcOff, dstOff, n, seed int) {
	base := mk(size, seed)
	want := append([]byte(nil), base...)
	copy(want[dstOff:dstOff+n], want[srcOff:srcOff+n])

	moveAsm(&base[dstOff], &base[srcOff], n)
	ck(bytes.Equal(base, want),
		fmt.Sprintf("%s size=%d src=%d dst=%d n=%d", label, size, srcOff, dstOff, n))

	gcStress()
	ck(bytes.Equal(base, want), fmt.Sprintf("%s after GC size=%d n=%d", label, size, n))
	runtime.KeepAlive(base)
}

func main() {
	const size = 96
	cases := []struct {
		src, dst, n int
	}{
		{0, 0, size}, // 原地全量
		{10, 10, 40}, // 原地子区间
		{4, 5, 60},   // 右重叠 1 字节
		{8, 15, 70},  // 右重叠 7 字节
		{5, 4, 60},   // 左重叠 1 字节
		{15, 8, 70},  // 左重叠 7 字节
		{0, 60, 30},  // 非重叠（目标在后）
		{60, 0, 30},  // 非重叠（目标在前）
		{5, 10, 0},   // 零长度
		{0, 1, 95},   // 右重叠覆盖到末尾
		{1, 0, 95},   // 左重叠覆盖到开头
	}
	for i, c := range cases {
		run("fixed", size, c.src, c.dst, c.n, i+1)
	}

	// 逐长度覆盖 16/8/1 字节分段边界，均带 3 字节右/左重叠。
	for n := 0; n <= 70; n++ {
		run("right-overlap", 128, 10, 13, n, n+1)
		run("left-overlap", 128, 13, 10, n, n+1)
	}

	// 大块重叠与非重叠。
	run("large-overlap", 8192, 0, 17, 4096, 7)
	run("large-nooverlap", 8192, 0, 4096, 4096, 8)

	moveAsm(nil, nil, 0)
	finish("l08_asm_overlap_copy")
}
