// L08: 手写 amd64 SSE2 无符号字节饱和加/饱和减（PADDUSB/PSUBUSB），
// 尾部标量钳制；与 Go 参考实现对照，穷举全部 256x256 输入对，
// 覆盖块边界、哨兵、别名 dst==a、子切片与 GC。
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

func satAddU8Asm(dst, a, b *byte, n int)
func satSubU8Asm(dst, a, b *byte, n int)

func addRef(x, y byte) byte {
	s := int(x) + int(y)
	if s > 255 {
		return 255
	}
	return byte(s)
}

func subRef(x, y byte) byte {
	if x < y {
		return 0
	}
	return x - y
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
	dst[0], dst[n+1] = 0x5c, 0x5c
	for i := 0; i < n; i++ {
		dst[i+1] = addRef(a[i], b[i])
	}
	wantAdd := append([]byte(nil), dst...)
	// 用参考值覆盖，再交给汇编重算。
	for i := 0; i < n; i++ {
		dst[i+1] = 0
	}
	satAddU8Asm(&dst[1], ptr(a), ptr(b), n)
	ck(bytes.Equal(dst, wantAdd), fmt.Sprintf("%s add n=%d", label, n))
	ck(dst[0] == 0x5c && dst[n+1] == 0x5c, fmt.Sprintf("%s add sentinel n=%d", label, n))

	for i := 0; i < n; i++ {
		dst[i+1] = subRef(a[i], b[i])
	}
	wantSub := append([]byte(nil), dst...)
	for i := 0; i < n; i++ {
		dst[i+1] = 0
	}
	satSubU8Asm(&dst[1], ptr(a), ptr(b), n)
	ck(bytes.Equal(dst, wantSub), fmt.Sprintf("%s sub n=%d", label, n))
	ck(dst[0] == 0x5c && dst[n+1] == 0x5c, fmt.Sprintf("%s sub sentinel n=%d", label, n))

	acopy := append([]byte(nil), a...)
	gcStress()
	satAddU8Asm(&dst[1], ptr(a), ptr(b), n)
	ck(bytes.Equal(dst, wantAdd), fmt.Sprintf("%s add after GC n=%d", label, n))
	ck(bytes.Equal(a, acopy), fmt.Sprintf("%s source mutated n=%d", label, n))
	if n > 5 {
		satAddU8Asm(ptr(a[2:n-1]), ptr(a[2:n-1]), ptr(b[2:n-1]), n-3)
		for i := 0; i < n-3; i++ {
			if a[i+2] != addRef(acopy[i+2], b[i+2]) {
				ck(false, fmt.Sprintf("%s alias add i=%d", label, i))
				break
			}
		}
		ck(true, "alias sweep done")
	}
	runtime.KeepAlive(a)
	runtime.KeepAlive(b)
	runtime.KeepAlive(dst)
}

func main() {
	// 穷举 256x256：a 为高字节、b 为低字节，覆盖全部输入对。
	const N = 65536
	a := make([]byte, N)
	b := make([]byte, N)
	for i := 0; i < N; i++ {
		a[i] = byte(i >> 8)
		b[i] = byte(i & 0xff)
	}
	checkN(a, b, "exhaustive")
	// 交换操作数：饱和加可交换、饱和减不可交换。
	checkN(b, a, "exhaustive-swapped")

	// 边界长度与尾部路径。
	for _, n := range []int{0, 1, 2, 3, 7, 8, 15, 16, 17, 31, 32, 33, 63, 64, 65, 255} {
		x := make([]byte, n)
		y := make([]byte, n)
		for i := range x {
			x[i] = byte((i*29 + n) & 0xff)
			y[i] = byte((i*71 + 13) & 0xff)
		}
		if n > 0 {
			x[0], y[0] = 255, 1
			x[n-1], y[n-1] = 0, 255
		}
		checkN(x, y, "edges")
	}

	// 别名 dst==a：先保存参考再原地执行。
	for _, n := range []int{15, 16, 17, 64, 257} {
		x := make([]byte, n+2)
		y := make([]byte, n)
		for i := 0; i < n; i++ {
			x[i+1] = byte((i * 13) & 0xff)
			y[i] = byte((i * 97) & 0xff)
		}
		x[0], x[n+1] = 0x77, 0x77
		orig := append([]byte(nil), x...)
		want := make([]byte, n+2)
		want[0], want[n+1] = 0x77, 0x77
		for i := 0; i < n; i++ {
			want[i+1] = addRef(orig[i+1], y[i])
		}
		satAddU8Asm(&x[1], &x[1], ptr(y), n)
		ck(bytes.Equal(x, want), fmt.Sprintf("alias dst==a n=%d", n))
	}
	for _, n := range []int{15, 16, 17, 64, 257} {
		x := make([]byte, n)
		y := make([]byte, n+2)
		for i := 0; i < n; i++ {
			x[i] = byte((i * 41) & 0xff)
			y[i+1] = byte((i * 7) & 0xff)
		}
		y[0], y[n+1] = 0x66, 0x66
		orig := append([]byte(nil), y...)
		want := make([]byte, n+2)
		want[0], want[n+1] = 0x66, 0x66
		for i := 0; i < n; i++ {
			want[i+1] = subRef(x[i], orig[i+1])
		}
		satSubU8Asm(&y[1], ptr(x), &y[1], n)
		ck(bytes.Equal(y, want), fmt.Sprintf("alias dst==b n=%d", n))
	}

	finish("l08_asm_sat_add_u8")
}
