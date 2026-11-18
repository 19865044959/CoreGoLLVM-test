// L09: C 结构体内嵌函数指针（vtable）：op_t{name, fn, arity, code} 组成
// vtable_t，C 侧按索引经结构体字段里的函数指针分发；Go 通过 C 辅助函数
// 读取布局、替换方法指针并跨 GC 复核。C 只持有自己的函数指针，不保存
// 任何 Go 指针。
package main

/*
#include <stdint.h>
#include <stddef.h>
#include <stdlib.h>
#include "vtable.h"
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

func name(vt *C.vtable_t, i int) string {
	return C.GoString(C.vt_name(vt, C.int(i)))
}

func main() {
	const cname = "l09_cgo_struct_fnptr"

	ck(uintptr(C.vt_sizeof_op()) == unsafe.Sizeof(C.op_t{}), "sizeof op_t")
	ck(uintptr(C.vt_sizeof_vtable()) == unsafe.Sizeof(C.vtable_t{}), "sizeof vtable_t")
	ck(uintptr(C.vt_off_fn()) == unsafe.Offsetof(C.op_t{}.fn), "offsetof op_t.fn")
	ck(uintptr(C.vt_off_ops1()) == unsafe.Offsetof(C.vtable_t{}.ops)+unsafe.Sizeof(C.op_t{}), "offsetof vtable_t.ops[1]")
	ck(uintptr(C.vt_off_bias()) == unsafe.Offsetof(C.vtable_t{}.bias), "offsetof vtable_t.bias")
	ck(uintptr(C.vt_off_calls()) == unsafe.Offsetof(C.vtable_t{}.calls), "offsetof vtable_t.calls")

	p := C.malloc(C.size_t(C.vt_sizeof_vtable()))
	if p == nil {
		ck(false, "malloc vtable_t")
		finish(cname)
		return
	}
	defer C.free(p)
	vt := (*C.vtable_t)(p)
	C.vt_init(vt)

	wantNames := []string{"add", "mul", "max", "min"}
	for i, want := range wantNames {
		ck(name(vt, i) == want, fmt.Sprintf("name[%d]=%q", i, want))
		ck(int32(C.vt_arity(vt, C.int(i))) == 2, fmt.Sprintf("arity[%d]", i))
		ck(int32(C.vt_code(vt, C.int(i))) == int32(i+1), fmt.Sprintf("code[%d]", i))
	}
	ck(name(vt, 4) == "", "name out of range is NULL")
	ck(int32(C.vt_arity(vt, -1)) == -1, "arity out of range")
	ck(int32(C.vt_unique_fns(vt)) == 4, "4 unique function pointers")

	refs := []func(int64, int64) int64{
		func(a, b int64) int64 { return int64(uint64(a) + uint64(b)) },
		func(a, b int64) int64 { return int64(uint64(a) * uint64(b)) },
		func(a, b int64) int64 {
			if a > b {
				return a
			}
			return b
		},
		func(a, b int64) int64 {
			if a < b {
				return a
			}
			return b
		},
	}
	pairs := [][2]int64{
		{7, 3}, {-7, 3}, {0, 0}, {123456789, -987654321},
		{1 << 40, -(1 << 30)}, {-5, -9}, {1, -1}, {42, 42},
	}
	valid := int64(0)
	for op := 0; op < 4; op++ {
		for _, pr := range pairs {
			got := int64(C.vt_dispatch(vt, C.int(op), C.int64_t(pr[0]), C.int64_t(pr[1])))
			ck(got == refs[op](pr[0], pr[1]), fmt.Sprintf("dispatch op=%d a=%d b=%d", op, pr[0], pr[1]))
			valid++
		}
	}
	ck(int64(C.vt_calls(vt)) == valid, "valid dispatch call count")
	for _, idx := range []int{-1, 4, 99} {
		got := int64(C.vt_dispatch(vt, C.int(idx), 1, 2))
		ck(got == -1<<63, fmt.Sprintf("invalid index %d sentinel", idx))
	}
	ck(int64(C.vt_calls(vt)) == valid, "invalid dispatch does not count")

	// bias 只影响返回结果，不改变底层算子。
	C.vt_set_bias(vt, 7)
	got := int64(C.vt_dispatch(vt, 0, 2, 3))
	ck(got == 12, "bias applied to add")
	valid++
	got = int64(C.vt_dispatch(vt, 1, 2, 3))
	ck(got == 13, "bias applied to mul")
	valid++
	ck(int64(C.vt_calls(vt)) == valid, "calls after bias dispatch")

	// 替换结构体内的函数指针：旧 code 返回，分发结果立即改变。
	ck(int32(C.vt_replace(vt, 1, 1)) == 2, "replace mul -> add returns old code")
	ck(int32(C.vt_code(vt, 1)) == 1, "replaced function code")
	ck(name(vt, 1) == "mul", "name field unchanged after replace")
	got = int64(C.vt_dispatch(vt, 1, 9, 4))
	ck(got == 20, "dispatch through replaced pointer")
	valid++
	ck(int32(C.vt_unique_fns(vt)) == 3, "unique fns after first replace")
	ck(int32(C.vt_replace(vt, 3, 2)) == 4, "replace min -> mul returns old code")
	ck(int32(C.vt_unique_fns(vt)) == 3, "unique fns after second replace")
	ck(int32(C.vt_replace(vt, 2, 1)) == 3, "replace max -> add returns old code")
	ck(int32(C.vt_unique_fns(vt)) == 2, "unique fns after third replace")
	got = int64(C.vt_dispatch(vt, 3, 6, 7))
	ck(got == 49, "dispatch through second replaced pointer")
	valid++

	// 非法替换不改变任何字段。
	ck(int32(C.vt_replace(vt, -1, 1)) == -1, "replace invalid index -1")
	ck(int32(C.vt_replace(vt, 4, 1)) == -1, "replace invalid index 4")
	ck(int32(C.vt_replace(vt, 0, 9)) == -1, "replace invalid code")
	ck(int32(C.vt_code(vt, 0)) == 1, "code unchanged after invalid replace")

	// 名称复制：足够、恰好、不足、非法索引。
	buf := make([]byte, 16)
	n := int(C.vt_copy_name(vt, 0, (*C.char)(unsafe.Pointer(&buf[0])), C.size_t(len(buf))))
	ck(n == 3 && string(buf[:n]) == "add" && buf[n] == 0, "copy_name add")
	small := make([]byte, 4)
	n = int(C.vt_copy_name(vt, 0, (*C.char)(unsafe.Pointer(&small[0])), C.size_t(len(small))))
	ck(n == 3 && string(small[:n]) == "add", "copy_name exact fit")
	tiny := []byte{0xaa, 0xbb, 0xcc}
	n = int(C.vt_copy_name(vt, 0, (*C.char)(unsafe.Pointer(&tiny[0])), C.size_t(len(tiny))))
	ck(n == -1 && tiny[0] == 0, "copy_name short buffer rejected")
	tiny[0] = 0xaa
	n = int(C.vt_copy_name(vt, 7, (*C.char)(unsafe.Pointer(&tiny[0])), C.size_t(len(tiny))))
	ck(n == -1 && tiny[0] == 0, "copy_name invalid index rejected")

	// GC 只回收 Go 堆；vtable_t 与函数指针均在 C 堆，内容必须稳定。
	gcStress()
	ck(int64(C.vt_calls(vt)) == valid, "calls stable across GC")
	for i, want := range wantNames {
		ck(name(vt, i) == want, fmt.Sprintf("name[%d] stable across GC", i))
	}
	ck(int32(C.vt_code(vt, 0)) == 1 && int32(C.vt_code(vt, 1)) == 1 &&
		int32(C.vt_code(vt, 2)) == 1 && int32(C.vt_code(vt, 3)) == 2, "replaced codes stable across GC")
	got = int64(C.vt_dispatch(vt, 0, 11, 5))
	ck(got == 23, "dispatch after GC")
	valid++
	got = int64(C.vt_dispatch(vt, 3, 6, 7))
	ck(got == 49, "replaced mul after GC")
	valid++
	ck(int64(C.vt_calls(vt)) == valid, "final call count")
	ck(int32(C.vt_unique_fns(vt)) == 2, "unique fns stable across GC")

	finish(cname)
}
