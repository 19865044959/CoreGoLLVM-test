// L09: C qsort + //export 比较回调：Go 把 []int32 传给 C，glibc qsort
// 在 C 调用期内反复回调 Go 比较器（偶尔分配垃圾并 GC），C 不保留任何
// Go 指针/函数指针到调用结束之后。
package main

import (
	"fmt"
	"os"
	"runtime"
	"sort"
	"sync/atomic"
	"unsafe"
)

/*
#include <stdint.h>
void c_sort_ints(int32_t *a, int32_t n);
*/
import "C"

var (
	failed   int32
	gcSink   [][]byte
	cmpCalls int64
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

//export goIntCmp
func goIntCmp(a, b unsafe.Pointer) C.int {
	atomic.AddInt64(&cmpCalls, 1)
	x := int32(*(*C.int32_t)(a))
	y := int32(*(*C.int32_t)(b))
	if cmpCalls%251 == 0 {
		// 在 C→Go 回调栈上制造垃圾并 GC，验证跨 cgo 回调的栈/寄存器扫描。
		junk := make([]byte, 2048)
		junk[0] = byte(cmpCalls)
		gcSink = append(gcSink[:0], junk)
		runtime.GC()
	}
	if x < y {
		return C.int(-1)
	}
	if x > y {
		return C.int(1)
	}
	return C.int(0)
}

func mk(n, seed int) []int32 {
	s := make([]int32, n)
	for i := range s {
		s[i] = int32(((i+seed)*7919)%4001) - 2000
	}
	return s
}

func sortViaC(s []int32) {
	if len(s) < 2 {
		return
	}
	C.c_sort_ints((*C.int32_t)(unsafe.Pointer(&s[0])), C.int32_t(len(s)))
}

func main() {
	sizes := []int{0, 1, 2, 3, 7, 8, 16, 33, 64, 100, 257, 1000}
	for si, n := range sizes {
		s := mk(n, si+1)
		ref := append([]int32(nil), s...)
		sort.Slice(ref, func(i, j int) bool { return ref[i] < ref[j] })

		sortViaC(s)
		ok := len(s) == n
		for i := range s {
			if s[i] != ref[i] {
				ok = false
				break
			}
		}
		ck(ok, fmt.Sprintf("qsort n=%d", n))

		gcStress()
		ok = true
		for i := range s {
			if s[i] != ref[i] {
				ok = false
				break
			}
		}
		ck(ok, fmt.Sprintf("qsort result after GC n=%d", n))
		runtime.KeepAlive(s)
	}

	// 大量重复值 + 负数边界。
	dup := make([]int32, 300)
	for i := range dup {
		dup[i] = int32((i%7)-3) * 100000
	}
	ref := append([]int32(nil), dup...)
	sort.Slice(ref, func(i, j int) bool { return ref[i] < ref[j] })
	sortViaC(dup)
	ok := true
	for i := range dup {
		if dup[i] != ref[i] {
			ok = false
			break
		}
	}
	ck(ok, "qsort duplicates")
	ck(dup[0] == -300000 && dup[len(dup)-1] == 300000, "qsort duplicates endpoints")

	ck(cmpCalls > 0, "comparator was invoked")
	before := atomic.LoadInt64(&cmpCalls)
	sortViaC(nil)
	sortViaC([]int32{42})
	ck(atomic.LoadInt64(&cmpCalls) == before, "no comparator for n<2")

	// 高值区间（避免 qsort 比较器有符号处理错误）。
	ext := []int32{0, -1, 1, 1<<31 - 1, -1 << 31, 123456789, -123456789}
	sortViaC(ext)
	ck(ext[0] == -1<<31 && ext[len(ext)-1] == 1<<31-1, "qsort int32 extremes")

	finish("l09_cgo_qsort_cb")
}
