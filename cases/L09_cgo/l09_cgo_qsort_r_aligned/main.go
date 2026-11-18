// L09: C qsort_r + posix_memalign 组合：64 字节对齐的 qa_rec_t 数组由
// C 分配/填充，C 比较器经 qsort_r 的 context 参数读取排序模式并统计
// 比较次数与元素地址对齐；Go 只校验布局、顺序、payload 完整性与
// 对齐不变量，期间显式 GC。C 只持有自己分配的内存，不保留 Go 指针。
package main

/*
#include <stdint.h>
#include <stddef.h>
#include <stdlib.h>
#include "qa.h"
*/
import "C"

import (
	"fmt"
	"os"
	"runtime"
	"sync/atomic"
	"unsafe"
)

const caseName = "l09_cgo_qsort_r_aligned"

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

func expectKey(i int) int32 { return int32((i*13 + 5) % 11) }

// checkValues 校验 tie 的唯一性/完整性以及 payload 派生值，不断言顺序。
func checkValues(tag string, recs []C.qa_rec_t) {
	n := len(recs)
	seen := make([]bool, n)
	for i, r := range recs {
		idx := int(r.tie)
		if idx < 0 || idx >= n || seen[idx] {
			ck(false, fmt.Sprintf("%s: tie[%d]=%d duplicate/out-of-range", tag, i, idx))
			continue
		}
		seen[idx] = true
		ck(int32(r.key) == expectKey(idx), fmt.Sprintf("%s: key[%d]", tag, i))
		ck(int32(r.payload) == int32(idx*7+1), fmt.Sprintf("%s: payload[%d]", tag, i))
		ck(r.pad == 0, fmt.Sprintf("%s: pad[%d]", tag, i))
	}
	all := true
	for _, v := range seen {
		all = all && v
	}
	ck(all, tag+": tie permutation incomplete")
}

// checkOrder 校验 tie 内容合法且按 mode 有序。
func checkOrder(tag string, recs []C.qa_rec_t, mode int) {
	checkValues(tag, recs)
	for i := 1; i < len(recs); i++ {
		a, b := recs[i-1], recs[i]
		if mode == 0 {
			ck(a.key < b.key || (a.key == b.key && a.tie < b.tie),
				fmt.Sprintf("%s: asc order at %d (%d/%d then %d/%d)", tag, i, int32(a.key), int32(a.tie), int32(b.key), int32(b.tie)))
		} else {
			ck(a.key > b.key || (a.key == b.key && a.tie < b.tie),
				fmt.Sprintf("%s: desc order at %d (%d/%d then %d/%d)", tag, i, int32(a.key), int32(a.tie), int32(b.key), int32(b.tie)))
		}
	}
}

func main() {
	ck(uintptr(C.qa_rec_size()) == unsafe.Sizeof(C.qa_rec_t{}), "sizeof qa_rec_t")
	ck(uintptr(C.qa_rec_align()) == unsafe.Alignof(C.qa_rec_t{}), "alignof qa_rec_t")
	ck(uintptr(C.qa_off_key()) == unsafe.Offsetof(C.qa_rec_t{}.key), "offsetof key")
	ck(uintptr(C.qa_off_tie()) == unsafe.Offsetof(C.qa_rec_t{}.tie), "offsetof tie")
	ck(uintptr(C.qa_off_payload()) == unsafe.Offsetof(C.qa_rec_t{}.payload), "offsetof payload")
	ck(int(C.qa_ctx_size()) == int(unsafe.Sizeof(C.qa_ctx_t{})), "sizeof qa_ctx_t")

	ctx := (*C.qa_ctx_t)(C.malloc(C.size_t(unsafe.Sizeof(C.qa_ctx_t{}))))
	if ctx == nil {
		ck(false, "malloc qa_ctx_t")
		finish(caseName)
		return
	}
	defer C.free(unsafe.Pointer(ctx))

	for _, n := range []int{1, 2, 7, 64, 257} {
		var p *C.qa_rec_t
		rc := int(C.qa_alloc(&p, C.int(n)))
		if rc != 0 || p == nil {
			ck(false, fmt.Sprintf("posix_memalign n=%d rc=%d", n, rc))
			continue
		}
		ck(C.qa_aligned(unsafe.Pointer(p)) == 1, fmt.Sprintf("n=%d: 64-byte alignment", n))
		C.qa_fill(p, 0, C.int(n))

		recs := unsafe.Slice(p, n)
		checkValues(fmt.Sprintf("n=%d filled", n), recs)

		for _, mode := range []int{0, 1, 0} {
			C.qa_sort(p, C.int(n), ctx, C.int(mode))
			ck(int(C.qa_ctx_misaligned(ctx)) == 0, fmt.Sprintf("n=%d mode=%d: comparator misaligned", n, mode))
			if n > 1 {
				ck(int(C.qa_ctx_calls(ctx)) > 0, fmt.Sprintf("n=%d mode=%d: no comparisons", n, mode))
			}
			ck(int(C.qa_verify(p, C.int(n), C.int(mode))) == -1, fmt.Sprintf("n=%d mode=%d: C verify", n, mode))
			checkOrder(fmt.Sprintf("n=%d mode=%d sorted", n, mode), recs, mode)
			gcStress()
		}

		// 再排一次已排序数组（qsort 的退化输入），结果不变。
		C.qa_sort(p, C.int(n), ctx, 0)
		ck(int(C.qa_verify(p, C.int(n), 0)) == -1, fmt.Sprintf("n=%d: re-sort sorted input", n))
		checkOrder(fmt.Sprintf("n=%d re-sorted", n), recs, 0)
		gcStress()
		C.qa_free(p)
	}

	finish(caseName)
}
