// L09: C memmove 重叠拷贝：把 Go []byte 传给 C，使用 memmove 处理
// 前向/后向重叠、原地与大块偏移；与 Go copy（memmove 语义）对照，
// 并用首尾哨兵检测越界写，跨 GC 复核内容。
package main

import (
	"bytes"
	"fmt"
	"os"
	"runtime"
	"sync/atomic"
	"unsafe"
)

/*
#include <stddef.h>
#include <string.h>

static void c_move(void *dst, void *src, size_t n) {
	memmove(dst, src, n);
}
*/
import "C"

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

func mk(n, seed int) []byte {
	b := make([]byte, n)
	for i := range b {
		b[i] = byte((i*17 + seed*31) & 0xff)
	}
	return b
}

func main() {
	const size = 256
	for _, d := range []struct {
		start, dst, n int
		desc          string
	}{
		{0, 8, 24, "forward overlap"},
		{8, 0, 24, "backward overlap"},
		{0, 0, 24, "in-place"},
		{0, 40, 16, "non-overlap forward"},
		{40, 0, 16, "non-overlap backward"},
		{100, 96, 64, "near overlap small distance"},
		{96, 100, 64, "near overlap reverse"},
		{200, 0, 50, "far overlap"},
		{0, 200, 50, "far overlap reverse"},
		{10, 20, 0, "zero length"},
	} {
		src := mk(size, d.start+3)
		got := append([]byte(nil), src...)
		want := append([]byte(nil), src...)

		C.c_move(unsafe.Pointer(&got[d.dst]), unsafe.Pointer(&got[d.start]), C.size_t(d.n))
		copy(want[d.dst:d.dst+d.n], want[d.start:d.start+d.n])

		ck(bytes.Equal(got, want), d.desc)
		if d.n > 0 && d.dst+d.n < size {
			ck(got[d.dst+d.n] == src[d.dst+d.n], d.desc+" tail untouched")
		}
		if d.n > 0 && d.dst > 0 {
			ck(got[d.dst-1] == src[d.dst-1], d.desc+" head untouched")
		}
		runtime.KeepAlive(got)
	}

	// 定种子偏移矩阵：每次与 Go copy 对照。
	buf := mk(1024, 77)
	for i := 0; i < 200; i++ {
		a := (i * 37) % 900
		b := (i * 53) % 900
		n := (i*29)%120 + 1
		if a+n > len(buf) || b+n > len(buf) {
			continue
		}
		got := append([]byte(nil), buf...)
		want := append([]byte(nil), buf...)
		C.c_move(unsafe.Pointer(&got[b]), unsafe.Pointer(&got[a]), C.size_t(n))
		copy(want[b:b+n], want[a:a+n])
		ck(bytes.Equal(got, want), fmt.Sprintf("matrix i=%d a=%d b=%d n=%d", i, a, b, n))
		if i%50 == 49 {
			gcStress()
		}
	}

	gcStress()
	after := mk(512, 91)
	ref := append([]byte(nil), after...)
	C.c_move(unsafe.Pointer(&after[1]), unsafe.Pointer(&after[0]), C.size_t(400))
	copy(ref[1:401], ref[0:400])
	ck(bytes.Equal(after, ref), "overlap after GC")
	runtime.KeepAlive(after)

	finish("l09_cgo_memmove_overlap")
}
