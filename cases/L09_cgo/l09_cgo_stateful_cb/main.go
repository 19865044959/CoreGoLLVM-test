// L09: 带状态回调：C 在单次调用内维护 accumulator，每一步经整数 handle
// 回调 Go；Go 按 handle 在自己的状态表里查 mode/mult/bias 并更新计数、
// 偶尔分配垃圾并 GC。C 不保存 Go 指针或函数指针到调用结束之后。
package main

/*
#include <stdint.h>
#include "stateful.h"
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

	// handle -> 状态；handle 是整数，C 侧不会持有 Go 指针。
	foldStates = map[uintptr]*foldState{}
)

type foldState struct {
	mode  int
	mult  int64
	bias  int64
	calls int64
	sum   int64
}

const (
	handleA = uintptr(0xA001)
	handleB = uintptr(0xB002)
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

//export goStateFold
func goStateFold(h C.uintptr_t, acc C.int64_t, v C.int32_t) C.int64_t {
	st := foldStates[uintptr(h)]
	if st == nil {
		ck(false, fmt.Sprintf("unknown callback handle %#x", uintptr(h)))
		return acc
	}
	st.calls++
	st.sum += int64(v)
	var r int64
	switch st.mode {
	case 1:
		r = int64(acc)*2 + int64(v)
	case 2:
		r = int64(acc) + int64(v)*st.mult + st.bias
	default:
		r = int64(acc)
	}
	// 回调栈上分配垃圾并显式 GC，验证 C→Go 回调期间状态表与回调参数可见。
	if st.calls%64 == 0 {
		junk := make([]byte, 4096)
		junk[0] = byte(st.calls)
		gcSink = append(gcSink[:0], junk)
		runtime.GC()
	}
	return C.int64_t(r)
}

func refFoldInto(acc int64, vals []int32, st *foldState) int64 {
	for _, v := range vals {
		switch st.mode {
		case 1:
			acc = acc*2 + int64(v)
		case 2:
			acc = acc + int64(v)*st.mult + st.bias
		}
	}
	return acc
}

func sum32(vals []int32) int64 {
	var s int64
	for _, v := range vals {
		s += int64(v)
	}
	return s
}

func makeVals(n, seed int) []int32 {
	s := make([]int32, n)
	for i := range s {
		s[i] = int32((i*seed)%101) - 50
	}
	return s
}

func foldC(vals []int32, h uintptr) int64 {
	if len(vals) == 0 {
		return int64(C.c_fold(nil, 0, C.uintptr_t(h)))
	}
	r := int64(C.c_fold((*C.int32_t)(unsafe.Pointer(&vals[0])), C.int32_t(len(vals)), C.uintptr_t(h)))
	runtime.KeepAlive(vals)
	return r
}

func main() {
	stA := &foldState{mode: 1}
	stB := &foldState{mode: 2, mult: 3, bias: -7}
	foldStates[handleA] = stA
	foldStates[handleB] = stB

	a := makeVals(100, 37)
	b := makeVals(73, 53)

	got := foldC(a, handleA)
	ck(got == refFoldInto(0, a, stA), "fold A result")
	ck(stA.calls == 100, "fold A callback count")
	ck(stA.sum == sum32(a), "fold A arg sum")
	ck(stB.calls == 0 && stB.sum == 0, "handle B untouched by fold A")

	// 一次 C 调用内交替使用两个 handle：C 的 accumulator 连续传递，
	// 但每一步按 handle 选择 Go 侧不同状态。
	want := refFoldInto(refFoldInto(0, a, stA), b, stB)
	cgot := int64(C.c_fold_two(
		(*C.int32_t)(unsafe.Pointer(&a[0])), C.int32_t(len(a)), C.uintptr_t(handleA),
		(*C.int32_t)(unsafe.Pointer(&b[0])), C.int32_t(len(b)), C.uintptr_t(handleB)))
	ck(cgot == want, "fold two-handle result")
	ck(stA.calls == 200, "handle A calls after interleaved fold")
	ck(stB.calls == 73, "handle B calls after interleaved fold")
	ck(stA.sum == 2*sum32(a), "handle A arg sum after interleaved fold")
	ck(stB.sum == sum32(b), "handle B arg sum after interleaved fold")

	// 空输入不触发回调。
	beforeA, beforeB := stA.calls, stB.calls
	ck(foldC(nil, handleA) == 0, "empty fold result")
	ck(stA.calls == beforeA && stB.calls == beforeB, "empty fold triggers no callback")

	gcStress()
	ck(stA.calls == 200 && stB.calls == 73, "state table survives GC")
	ck(foldStates[handleA] == stA && foldStates[handleB] == stB, "state pointers stable after GC")

	// GC 后继续回调，计数从原值累加。
	c := makeVals(64, 29)
	prev := stB.calls
	got = foldC(c, handleB)
	ck(got == refFoldInto(0, c, stB), "fold B after GC")
	ck(stB.calls == prev+64, "handle B calls continue after GC")

	// 再次交错调用，确认两套状态互不串扰。
	gotA := foldC(a, handleA)
	gotB := foldC(b, handleB)
	ck(gotA == refFoldInto(0, a, stA), "fold A after interleaved calls")
	ck(gotB == refFoldInto(0, b, stB), "fold B after interleaved calls")
	ck(stA.calls == 300 && stB.calls == 2*73+64, "final per-handle callback counts")

	gcStress()
	ck(stA.calls == 300 && stB.calls == 2*73+64, "final counts after GC")
	finish("l09_cgo_stateful_cb")
}
