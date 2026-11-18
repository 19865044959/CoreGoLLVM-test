// L08: 手写 amd64 SSE2 计算两个无符号字节缓冲的逐元素绝对差（|a-b|）。
// 每 16 字节用 PMAXUB/PMINUB/PSUBUSB 向量化，尾部标量；
// 与 Go 参考实现对照，穷举全部 256x256 输入对并覆盖别名/哨兵/GC。
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

func absDiffU8Asm(dst, a, b *byte, n int)

func absDiffRef(x, y byte) byte {
	if x >= y {
		return x - y
	}
	return y - x
}

func ptr(b []byte) *byte {
	if len(b) == 0 {
		return nil
	}
	return &b[0]
}

func checkN(a, b []byte, label string) {
	n := len(a)
	dst := make([]byte, n+2)
	dst[0], dst[n+1] = 0x3c, 0x3c
	want := make([]byte, n+2)
	want[0], want[n+1] = 0x3c, 0x3c
	for i := 0; i < n; i++ {
		want[i+1] = absDiffRef(a[i], b[i])
	}
	absDiffU8Asm(&dst[1], ptr(a), ptr(b), n)
	ck(bytes.Equal(dst, want), fmt.Sprintf("%s n=%d got(dst)=%x want=%x", label, n, dst, want))
	ck(dst[0] == 0x3c && dst[n+1] == 0x3c, fmt.Sprintf("%s sentinel n=%d", label, n))
	acopy := append([]byte(nil), a...)
	bcopy := append([]byte(nil), b...)
	gcStress()
	absDiffU8Asm(&dst[1], ptr(a), ptr(b), n)
	ck(bytes.Equal(dst, want), fmt.Sprintf("%s after GC n=%d", label, n))
	ck(bytes.Equal(a, acopy) && bytes.Equal(b, bcopy), fmt.Sprintf("%s source mutated n=%d", label, n))
	if n > 5 {
		sub := make([]byte, n-3)
		absDiffU8Asm(ptr(sub), ptr(a[2:n-1]), ptr(b[2:n-1]), n-3)
		for i := 0; i < n-3; i++ {
			if sub[i] != absDiffRef(acopy[i+2], bcopy[i+2]) {
				ck(false, fmt.Sprintf("%s sub-slice i=%d", label, i))
				break
			}
		}
		ck(true, "sub-slice sweep done")
	}
	runtime.KeepAlive(a)
	runtime.KeepAlive(b)
	runtime.KeepAlive(dst)
}

func main() {
	// 穷举 256x256：a 为高字节、b 为低字节。
	const N = 65536
	a := make([]byte, N)
	b := make([]byte, N)
	for i := 0; i < N; i++ {
		a[i] = byte(i >> 8)
		b[i] = byte(i & 0xff)
	}
	checkN(a, b, "exhaustive")
	checkN(b, a, "exhaustive-swapped")

	// 边界长度、全 0/全 255、尾部路径。
	for _, n := range []int{0, 1, 2, 3, 7, 8, 15, 16, 17, 31, 32, 33, 63, 64, 65, 255, 1024} {
		x := make([]byte, n)
		y := make([]byte, n)
		for i := range x {
			x[i] = byte((i*17 + n) & 0xff)
			y[i] = byte((i*53 + 29) & 0xff)
		}
		if n > 0 {
			x[0], y[0] = 0, 255
			x[n-1], y[n-1] = 255, 0
		}
		checkN(x, y, "edges")
	}
	checkN(make([]byte, 300), bytes.Repeat([]byte{0xff}, 300), "zero-ff")
	checkN(bytes.Repeat([]byte{0xff}, 300), make([]byte, 300), "ff-zero")

	// 别名 dst==a / dst==b。
	for _, n := range []int{15, 16, 17, 64, 257} {
		x := make([]byte, n+2)
		y := make([]byte, n)
		for i := 0; i < n; i++ {
			x[i+1] = byte((i * 19) & 0xff)
			y[i] = byte((i * 83) & 0xff)
		}
		x[0], x[n+1] = 0x44, 0x44
		orig := append([]byte(nil), x...)
		want := make([]byte, n+2)
		want[0], want[n+1] = 0x44, 0x44
		for i := 0; i < n; i++ {
			want[i+1] = absDiffRef(orig[i+1], y[i])
		}
		absDiffU8Asm(&x[1], &x[1], ptr(y), n)
		ck(bytes.Equal(x, want), fmt.Sprintf("alias dst==a n=%d", n))
	}
	for _, n := range []int{15, 16, 17, 64, 257} {
		x := make([]byte, n)
		y := make([]byte, n+2)
		for i := 0; i < n; i++ {
			x[i] = byte((i * 67) & 0xff)
			y[i+1] = byte((i * 23) & 0xff)
		}
		y[0], y[n+1] = 0x55, 0x55
		orig := append([]byte(nil), y...)
		want := make([]byte, n+2)
		want[0], want[n+1] = 0x55, 0x55
		for i := 0; i < n; i++ {
			want[i+1] = absDiffRef(x[i], orig[i+1])
		}
		absDiffU8Asm(&y[1], ptr(x), &y[1], n)
		ck(bytes.Equal(y, want), fmt.Sprintf("alias dst==b n=%d", n))
	}

	finish("l08_asm_abs_diff_u8")
}
