// L09: C struct 读写：Go 侧直接读写字段、C 侧填充/求和，
// 校验 sizeof/offsetof 与 Go unsafe 布局一致，并跨 GC 复核。
package main

/*
#include <stddef.h>
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>

typedef struct {
	int32_t x;
	int32_t y;
	int64_t z;
	char tag[8];
} point_t;

static void c_point_set(point_t *p, int32_t x, int32_t y, int64_t z, const char *tag) {
	p->x = x;
	p->y = y;
	p->z = z;
	snprintf(p->tag, sizeof p->tag, "%s", tag);
}

static int64_t c_point_sum(const point_t *p) {
	return (int64_t)p->x + (int64_t)p->y + p->z;
}

static int64_t c_points_total(const point_t *p, int n) {
	int64_t s = 0;
	for (int i = 0; i < n; i++) {
		s += (int64_t)p[i].x + (int64_t)p[i].y + p[i].z;
	}
	return s;
}

static size_t c_point_size(void) { return sizeof(point_t); }
static size_t c_point_off_z(void) { return offsetof(point_t, z); }
static size_t c_point_off_tag(void) { return offsetof(point_t, tag); }
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
	ck(uint64(unsafe.Sizeof(C.point_t{})) == uint64(C.c_point_size()), "Go sizeof matches C sizeof(point_t)")
	ck(uint64(unsafe.Offsetof(C.point_t{}.z)) == uint64(C.c_point_off_z()), "offsetof z")
	ck(uint64(unsafe.Offsetof(C.point_t{}.tag)) == uint64(C.c_point_off_tag()), "offsetof tag")

	var p C.point_t
	tag := C.CString("abc")
	C.c_point_set(&p, 3, -7, 1000000007, tag)
	C.free(unsafe.Pointer(tag))

	ck(p.x == 3 && p.y == -7 && p.z == 1000000007, "C-filled fields visible in Go")
	ck(C.GoString(&p.tag[0]) == "abc", "C-filled char array")
	ck(int64(C.c_point_sum(&p)) == 1000000003, "C sum of C-filled struct")

	gcStress()
	ck(p.x == 3 && p.y == -7 && p.z == 1000000007, "fields after GC")
	ck(C.GoString(&p.tag[0]) == "abc", "tag after GC")

	// Go 直接改 C struct 字段，C 侧立即可见。
	p.x = 10
	p.z = -100
	ck(int64(C.c_point_sum(&p)) == -97, "Go-written fields read by C")

	// Go 数组传给 C 批量读取（C 不保留指针）。
	const n = 16
	arr := make([]C.point_t, n)
	var want int64
	for i := range arr {
		arr[i].x = C.int32_t(i)
		arr[i].y = C.int32_t(-2 * i)
		arr[i].z = C.int64_t(int64(i) * 1000)
		want += int64(i) - int64(2*i) + int64(i)*1000
	}
	ck(int64(C.c_points_total((*C.point_t)(unsafe.Pointer(&arr[0])), C.int(n))) == want,
		"C reads Go array of C structs")
	gcStress()
	runtime.KeepAlive(arr)
	ck(int64(C.c_points_total((*C.point_t)(unsafe.Pointer(&arr[0])), C.int(n))) == want,
		"C reads Go array of C structs after GC")
	finish("l09_cgo_struct")
}
