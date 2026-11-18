// L09: C 结构体函数指针+侵入式链表：fl_node{value,kind,pad,fn,next} 由 C
// 分配并串成单链，C 沿 next 指针取 fn 逐节点执行；Go 通过 C 辅助函数
// 中途替换节点函数指针（fn id 0..3）并复核整链结果、递归/迭代一致性、
// 布局偏移与显式 GC 后的稳定性。C 只持有自己的内存和函数指针，
// 不保留任何 Go 指针。
package main

/*
#include <stdint.h>
#include <stddef.h>
#include <stdlib.h>
#include "fl.h"
*/
import "C"

import (
	"fmt"
	"os"
	"runtime"
	"sync/atomic"
	"unsafe"
)

const caseName = "l09_cgo_fnptr_list"

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

func valueAt(i int) int64 { return int64(i)*17 - 40 }

func apply(kind int, v, acc int64) int64 {
	switch kind {
	case 0:
		return acc + v
	case 1:
		return acc - v
	case 2:
		return acc ^ int64(uint64(v)*2654435761)
	case 3:
		return (acc*3 + v) % 1000000007
	default:
		panic("bad kind")
	}
}

func expected(kinds []int, seed int64) int64 {
	acc := seed
	for i, k := range kinds {
		acc = apply(k, valueAt(i), acc)
	}
	return acc
}

func runSize(n int) {
	kinds := make([]int, n)
	for i := range kinds {
		kinds[i] = i % 4
	}
	head := C.fl_build(C.int(n))
	if head == nil {
		ck(false, fmt.Sprintf("n=%d: fl_build", n))
		return
	}

	ck(int(C.fl_count(head)) == n, fmt.Sprintf("n=%d: count", n))
	ck(int(C.fl_null_fn(head)) == 0, fmt.Sprintf("n=%d: null fn", n))
	for i := 0; i < n; i++ {
		ck(int64(C.fl_value_at(head, C.int(i))) == valueAt(i), fmt.Sprintf("n=%d: value[%d]", n, i))
		ck(int(C.fl_fn_id(head, C.int(i))) == kinds[i], fmt.Sprintf("n=%d: kind[%d]", n, i))
	}

	const seed1 = int64(12345)
	const seed2 = int64(-7)
	checkWalk := func(tag string) {
		ck(int64(C.fl_walk(head, C.int64_t(seed1))) == expected(kinds, seed1), tag+": walk seed1")
		ck(int64(C.fl_walk_rec(head, C.int64_t(seed1))) == expected(kinds, seed1), tag+": walk_rec seed1")
		ck(int64(C.fl_walk(head, C.int64_t(seed2))) == expected(kinds, seed2), tag+": walk seed2")
		ck(int64(C.fl_walk_rec(head, C.int64_t(seed2))) == expected(kinds, seed2), tag+": walk_rec seed2")
	}
	checkWalk(fmt.Sprintf("n=%d initial", n))
	gcStress()

	// 中途替换函数指针：头部 → mulmod，尾部 → sub，中间 → xor（最后写，
	// 小规模时中间可能与头/尾重合，最终值以最后一次写入为准）。
	idxMid := n / 2
	ck(int(C.fl_set_fn(head, 0, 3)) == 0, fmt.Sprintf("n=%d: set head fn", n))
	kinds[0] = 3
	ck(int(C.fl_set_fn(head, C.int(n-1), 1)) == 0, fmt.Sprintf("n=%d: set tail fn", n))
	kinds[n-1] = 1
	ck(int(C.fl_set_fn(head, C.int(idxMid), 2)) == 0, fmt.Sprintf("n=%d: set mid fn", n))
	kinds[idxMid] = 2
	if n > 1 {
		checkWalk(fmt.Sprintf("n=%d three swaps", n))
	}

	// 越界替换必须失败且不改变链。
	ck(int(C.fl_set_fn(head, C.int(n), 0)) == -1, fmt.Sprintf("n=%d: out-of-range set", n))
	ck(int(C.fl_set_fn(head, -1, 0)) == -1, fmt.Sprintf("n=%d: negative set", n))
	ck(int(C.fl_set_fn(head, 0, 4)) == -1, fmt.Sprintf("n=%d: invalid fn id", n))

	gcStress()
	checkWalk(fmt.Sprintf("n=%d after GC", n))
	ck(int(C.fl_fn_id(head, C.int(idxMid))) == 2, fmt.Sprintf("n=%d: mid fn id survives GC", n))
	C.fl_free(head)
}

func main() {
	ck(uintptr(C.fl_size()) == unsafe.Sizeof(C.fl_node{}), "sizeof fl_node")
	ck(uintptr(C.fl_off_value()) == unsafe.Offsetof(C.fl_node{}.value), "offsetof value")
	ck(uintptr(C.fl_off_kind()) == unsafe.Offsetof(C.fl_node{}.kind), "offsetof kind")
	ck(uintptr(C.fl_off_fn()) == unsafe.Offsetof(C.fl_node{}.fn), "offsetof fn")
	ck(uintptr(C.fl_off_next()) == unsafe.Offsetof(C.fl_node{}.next), "offsetof next")

	for _, n := range []int{1, 2, 8, 63, 257} {
		runSize(n)
	}
	finish(caseName)
}
