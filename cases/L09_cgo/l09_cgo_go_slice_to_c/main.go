// L09: C 直接写 Go 数值切片（C 不保留指针）：子切片起始偏移、
// 首尾哨兵检测越界写、多轮 GC 后 Go 侧读回一致。
package main

/*
#include <stdint.h>

static void c_fill_i32(int32_t *p, int n, int32_t base) {
	for (int i = 0; i < n; i++) {
		p[i] = base + (int32_t)(i * 3);
	}
}

static void c_scale_i64(int64_t *p, int n, int64_t mul) {
	for (int i = 0; i < n; i++) {
		p[i] = p[i] * mul;
	}
}

static int64_t c_sum_i64(const int64_t *p, int n) {
	int64_t s = 0;
	for (int i = 0; i < n; i++) {
		s += p[i];
	}
	return s;
}
*/
import "C"

import (
	"fmt"
	"os"
	"runtime"
	"sync/atomic"
	"unsafe"
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

func main() {
	const sentinel = 999

	// C 写 Go 切片：只写中间 6 个元素，首尾哨兵不得被改。
	base := make([]int32, 8)
	for i := range base {
		base[i] = sentinel
	}
	sub := base[1:7]
	C.c_fill_i32((*C.int32_t)(unsafe.Pointer(&sub[0])), C.int(len(sub)), 1000)
	for i := 0; i < 6; i++ {
		ck(sub[i] == int32(1000+i*3), fmt.Sprintf("C-written Go slice elem %d", i))
	}
	ck(base[0] == sentinel && base[7] == sentinel, "Go slice sentinels untouched")

	gcStress()
	for i := 0; i < 6; i++ {
		ck(sub[i] == int32(1000+i*3), fmt.Sprintf("C-written Go slice after GC elem %d", i))
	}
	runtime.KeepAlive(base)

	// C 读 Go int64 切片。
	nums := []int64{1, -2, 3, -4, 5, 1 << 40}
	var want int64
	for _, v := range nums {
		want += v
	}
	ck(int64(C.c_sum_i64((*C.int64_t)(unsafe.Pointer(&nums[0])), C.int(len(nums)))) == want,
		"C reads Go int64 slice")
	ck(int64(C.c_sum_i64(nil, 0)) == 0, "C sum of nil/0")

	// C 原地缩放到 Go 切片，多轮 GC。
	for round := 0; round < 64; round++ {
		for i := range nums {
			nums[i] = int64(i + 1)
		}
		C.c_scale_i64((*C.int64_t)(unsafe.Pointer(&nums[0])), C.int(len(nums)), 2)
		gcStress()
		for i := range nums {
			ck(nums[i] == int64(i+1)*2, fmt.Sprintf("C-scaled Go slice round=%d i=%d", round, i))
		}
		runtime.KeepAlive(nums)
	}

	// C 写空切片：长度为 0 时不得解引用。
	empty := make([]int32, 0)
	if len(empty) == 0 {
		C.c_fill_i32(nil, 0, 7)
	}
	finish("l09_cgo_go_slice_to_c")
}
