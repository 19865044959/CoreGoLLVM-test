// L09: cgo 使用 C.malloc/free 与“Go 数值切片指针传给 C 读取”两类跨边界访问，
// 期间触发 GC，校验数据不丢失、不越界。
package main

import (
	"fmt"
	"os"
	"runtime"
	"sync/atomic"
	"unsafe"
)

/*
#include <stdint.h>
#include <stdlib.h>

static void *c_alloc(size_t n) { return malloc(n); }
static void c_free(void *p) { free(p); }
static int64_t c_sum(const int64_t *p, int n) {
	int64_t s = 0;
	for (int i = 0; i < n; i++) s += p[i];
	return s;
}
static void c_fill(int64_t *p, int n, int64_t base) {
	for (int i = 0; i < n; i++) p[i] = base + i;
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

func main() {
	// 1) Go 数值切片指针传给 C：C 只在调用期读取，不保留指针。
	a := []int64{1, 2, 3, 4, 5}
	got := int64(C.c_sum((*C.int64_t)(unsafe.Pointer(&a[0])), C.int(5)))
	ck(got == 15, "C reads Go slice")
	gcStress()
	ck(len(a) == 5 && a[4] == 5, "Go slice after cgo + GC")

	// 2) C 分配内存 + C 填充，Go 侧读取；中途 GC。
	n := 64
	p := C.c_alloc(C.size_t(n) * C.size_t(unsafe.Sizeof(C.int64_t(0))))
	if p == nil {
		ck(false, "C.malloc failed")
		finish("l09_cgo_malloc_and_slice")
		return
	}
	arr := (*[64]C.int64_t)(p)
	C.c_fill((*C.int64_t)(p), C.int(n), 1000)
	gcStress()
	var sum int64
	for i := 0; i < n; i++ {
		sum += int64(arr[i])
	}
	ck(sum == int64(n*1000+n*(n-1)/2), "C-allocated memory after GC")
	C.c_free(p)

	// 3) 重复 malloc/free + GC，验证 cgo 堆与 Go GC 共存。
	for r := 0; r < 50; r++ {
		q := C.c_alloc(64)
		if q == nil {
			ck(false, "C.malloc failed in loop")
			break
		}
		*(*C.int64_t)(q) = C.int64_t(r)
		runtime.GC()
		ck(int64(*(*C.int64_t)(q)) == int64(r), "C memory content across GC")
		C.c_free(q)
	}
	finish("l09_cgo_malloc_and_slice")
}
