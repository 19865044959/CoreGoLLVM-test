// L09: C 堆分配失败边界：posix_memalign 对非法对齐（0/1/2/3/4/6/12/24）
// 返回 EINVAL 且不写出参；SIZE_MAX 级 malloc/calloc 乘法溢出/realloc 请求
// 返回 NULL+ENOMEM，失败的 realloc 必须保持原块内容；同时对照合法对齐
// 分配、calloc 零初始化、realloc(NULL,n) 与缩小保留前缀。C 只持有自己的
// 内存，Go 通过哨兵字节校验跨显式 GC 的完整性；C 不保留 Go 指针。
package main

/*
#cgo CFLAGS: -Wno-alloc-size-larger-than
#include <stdint.h>
#include <stddef.h>
#include <stdlib.h>
#include "af.h"
*/
import "C"

import (
	"fmt"
	"os"
	"runtime"
	"sync/atomic"
	"unsafe"
)

const caseName = "l09_cgo_alloc_failure"

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

func fill(p unsafe.Pointer, n int, v byte) {
	C.af_fill(p, C.size_t(n), C.uchar(v))
}

func checkBytes(p unsafe.Pointer, n int, v byte) int {
	return int(C.af_check(p, C.size_t(n), C.uchar(v)))
}

func main() {
	einval := int32(C.af_einval())
	enomem := int32(C.af_enomem())
	ck(einval != 0 && enomem != 0 && einval != enomem, "EINVAL/ENOMEM distinct")
	ck(uintptr(C.af_align_max()) >= 8, "alignof(max_align_t) >= 8")

	// 1) 非法对齐：EINVAL，出参保持 NULL。
	for _, align := range []int{0, 1, 2, 3, 4, 6, 12, 24} {
		var p unsafe.Pointer
		rc := int32(C.af_posix(C.size_t(align), 64, &p))
		ck(rc == einval, fmt.Sprintf("posix align=%d rc=%d want EINVAL", align, rc))
		ck(p == nil, fmt.Sprintf("posix align=%d out not NULL", align))
	}

	// 2) 合法对齐：指针按请求对齐，内容可写可读。
	for _, align := range []int{8, 16, 32, 64, 4096} {
		var p unsafe.Pointer
		rc := int32(C.af_posix(C.size_t(align), 100, &p))
		ck(rc == 0 && p != nil, fmt.Sprintf("posix align=%d rc=%d", align, rc))
		if p == nil {
			continue
		}
		ck(uintptr(p)%uintptr(align) == 0, fmt.Sprintf("posix align=%d pointer alignment", align))
		fill(p, 100, 0xa5)
		ck(checkBytes(p, 100, 0xa5) == -1, fmt.Sprintf("posix align=%d fill/check", align))
		gcStress()
		ck(checkBytes(p, 100, 0xa5) == -1, fmt.Sprintf("posix align=%d survives GC", align))
		C.af_free(p)
	}

	// 3) SIZE_MAX 级失败：NULL + ENOMEM。
	var hp unsafe.Pointer
	rc := int32(C.af_posix_huge(&hp))
	ck(rc == enomem && hp == nil, fmt.Sprintf("posix huge rc=%d pNil=%v", rc, hp == nil))

	var merr C.int
	mp := C.af_malloc_max(&merr)
	ck(mp == nil && int32(merr) == enomem, fmt.Sprintf("malloc max: pNil=%v err=%d", mp == nil, int32(merr)))

	var cerr C.int
	cp := C.af_calloc_overflow(&cerr)
	ck(cp == nil && int32(cerr) == enomem, fmt.Sprintf("calloc overflow: pNil=%v err=%d", cp == nil, int32(cerr)))

	// 4) 失败的 realloc 必须保留原块。
	var aerr C.int
	orig := C.af_malloc(64, &aerr)
	if orig == nil {
		ck(false, "malloc 64 for realloc test")
	} else {
		fill(orig, 64, 0x5a)
		var rerr C.int
		rp := C.af_realloc_max(orig, &rerr)
		ck(rp == nil && int32(rerr) == enomem, fmt.Sprintf("realloc max: pNil=%v err=%d", rp == nil, int32(rerr)))
		ck(checkBytes(orig, 64, 0x5a) == -1, "failed realloc preserves original block")
		C.af_free(orig)
	}

	// 5) realloc(NULL,n) 等价 malloc；缩小保留前缀。
	var nerr C.int
	p := C.af_realloc(nil, 128, &nerr)
	ck(p != nil && int32(nerr) == 0, fmt.Sprintf("realloc NULL: pNil=%v err=%d", p == nil, int32(nerr)))
	if p != nil {
		fill(p, 128, 0x33)
		var serr C.int
		q := C.af_realloc(p, 32, &serr)
		ck(q != nil && int32(serr) == 0, "realloc shrink")
		if q != nil {
			ck(checkBytes(q, 32, 0x33) == -1, "shrink preserves prefix")
			C.af_free(q)
		} else {
			C.af_free(p)
		}
	}

	// 6) calloc 零初始化；free(NULL) 安全。
	var zerr C.int
	z := C.af_calloc(16, 4, &zerr)
	ck(z != nil && int32(zerr) == 0, fmt.Sprintf("calloc 16x4 err=%d", int32(zerr)))
	if z != nil {
		ck(checkBytes(z, 64, 0) == -1, "calloc zeroed")
		fill(z, 64, 0x11)
		ck(checkBytes(z, 64, 0x11) == -1, "calloc writable")
		C.af_free(z)
	}
	C.af_free(nil)

	// 7) 反复失败/成功分配，周期性显式 GC。
	for i := 0; i < 200; i++ {
		var e1 C.int
		bad := C.af_malloc_max(&e1)
		ck(bad == nil && int32(e1) == enomem, fmt.Sprintf("iter %d: malloc max", i))
		var e2 C.int
		good := C.af_malloc(32, &e2)
		ck(good != nil && int32(e2) == 0, fmt.Sprintf("iter %d: malloc 32", i))
		if good != nil {
			fill(good, 32, byte(i))
			ck(checkBytes(good, 32, byte(i)) == -1, fmt.Sprintf("iter %d: content", i))
			C.af_free(good)
		}
		if i%25 == 0 {
			gcStress()
		}
	}

	finish(caseName)
}
