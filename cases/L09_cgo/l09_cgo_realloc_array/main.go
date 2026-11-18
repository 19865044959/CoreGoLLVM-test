// L09: C realloc 数组语义：realloc(NULL,n) 当 malloc、增长保留前缀、
// 同尺寸不动内容、缩小保留前缀、反复增长/收缩，期间显式 GC；同时校验
// realloc 结果满足 max_align_t 对齐。内存全部归 C 堆，Go 只经 unsafe.Slice
// 在 C 内存上读写，C 不保留任何 Go 指针。
package main

/*
#include <stdint.h>
#include <stddef.h>
#include <stdlib.h>
#include "ra.h"
*/
import "C"

import (
	"fmt"
	"os"
	"runtime"
	"sync/atomic"
	"unsafe"
)

const caseName = "l09_cgo_realloc_array"

var (
	failed int32
	gcSink [][]byte
	amax   int
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

func grow(p unsafe.Pointer, n int, tag string) unsafe.Pointer {
	np := C.ra_realloc(p, C.int32_t(n))
	if np == nil {
		ck(false, tag+": realloc returned NULL")
		finish(caseName)
		return nil
	}
	ck(int(C.ra_mod(np, C.size_t(amax))) == 0, tag+": realloc alignment")
	return np
}

func checkAll(p unsafe.Pointer, n int, tag string) {
	a := unsafe.Slice((*int32)(p), n)
	bad := -1
	for i := 0; i < n; i++ {
		if int64(a[i]) != int64(i)*3+1 {
			bad = i
			break
		}
	}
	ck(bad == -1, fmt.Sprintf("%s: value[%d]", tag, bad))
	ck(int(C.ra_check_prefix(p, C.int32_t(n))) == -1, tag+": C prefix")
	ck(int64(C.ra_sum(p, C.int32_t(n))) == 3*int64(n)*int64(n-1)/2+int64(n), tag+": sum")
}

func checkPrefix(p unsafe.Pointer, n int, tag string) {
	a := unsafe.Slice((*int32)(p), n)
	bad := -1
	for i := 0; i < n; i++ {
		if int64(a[i]) != int64(i)*3+1 {
			bad = i
			break
		}
	}
	ck(bad == -1, fmt.Sprintf("%s: prefix[%d]", tag, bad))
	ck(int(C.ra_check_prefix(p, C.int32_t(n))) == -1, tag+": C prefix")
}

func main() {
	amax = int(C.ra_alignof_max())
	ck(amax >= 8, "alignof(max_align_t) >= 8")

	// realloc(NULL, n) 作为首次分配。
	p := grow(nil, 8, "initial")
	C.ra_fill(p, 0, 8)
	checkAll(p, 8, "initial")

	// 增长：旧前缀保留，新区域由 C 填充后可读。
	p = grow(p, 32, "grow-32")
	checkPrefix(p, 8, "grow-32")
	C.ra_fill(p, 8, 32)
	checkAll(p, 32, "grow-32")

	// 同尺寸 realloc：内容不变。
	p = grow(p, 32, "same-32")
	checkAll(p, 32, "same-32")

	// 缩小：仅前缀 13 个元素有效。
	p = grow(p, 13, "shrink-13")
	checkPrefix(p, 13, "shrink-13")

	// 一次增长到 1000，再增长到 65536（256 KiB）。
	p = grow(p, 1000, "grow-1000")
	checkPrefix(p, 13, "grow-1000")
	C.ra_fill(p, 13, 1000)
	checkAll(p, 1000, "grow-1000")
	p = grow(p, 65536, "grow-65536")
	checkPrefix(p, 1000, "grow-65536")
	C.ra_fill(p, 1000, 65536)
	checkAll(p, 65536, "grow-65536")

	// 反复增长/收缩 64 轮，期间显式 GC。
	C.ra_free(p)
	p = nil
	size := 0
	for cyc := 0; cyc < 64; cyc++ {
		old := size
		size += 17
		p = grow(p, size, fmt.Sprintf("cycle-%d-grow", cyc))
		if old > 0 {
			checkPrefix(p, old, fmt.Sprintf("cycle-%d-prefix", cyc))
		}
		C.ra_fill(p, C.int32_t(old), C.int32_t(size))
		checkAll(p, size, fmt.Sprintf("cycle-%d", cyc))
		if cyc%8 == 7 {
			size = 13
			p = grow(p, size, fmt.Sprintf("cycle-%d-shrink", cyc))
			checkPrefix(p, 13, fmt.Sprintf("cycle-%d-shrink", cyc))
		}
		if cyc%8 == 0 {
			gcStress()
			checkPrefix(p, size, fmt.Sprintf("cycle-%d-after-gc", cyc))
		}
	}

	// GC 后仍可读写最后一块 C 内存。
	gcStress()
	checkAll(p, size, "final-after-gc")
	C.ra_free(p)
	C.ra_free(nil)

	finish(caseName)
}
