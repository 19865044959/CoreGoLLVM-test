// L09: C 返回“指针 + 长度”：malloc 数组（调用方 free）、
// 静态数组（不可 free）、静态字符串（GoStringN），以及 NULL 返回值。
// C 内存跨 GC 保持稳定；Go 侧用 unsafe.Slice/GoStringN 读取。
package main

/*
#include <stddef.h>
#include <stdint.h>
#include <stdlib.h>

static int64_t *c_make_i64s(int n, int64_t base) {
	int64_t *p = (int64_t *)malloc(sizeof(int64_t) * (size_t)n);
	if (p == NULL) {
		return NULL;
	}
	for (int i = 0; i < n; i++) {
		p[i] = base + (int64_t)i * 7;
	}
	return p;
}

static int32_t *c_static_i32s(void) {
	static int32_t v[4] = {10, 20, 30, 40};
	return v;
}

static const char *c_message(size_t *len) {
	static const char msg[] = "cgo-ptr-len";
	*len = sizeof(msg) - 1;
	return msg;
}

static int64_t *c_null_ptr(void) {
	return NULL;
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
	const n = 64
	p := C.c_make_i64s(n, -100)
	ck(p != nil, "C malloc array non-NULL")
	if p != nil {
		s := unsafe.Slice((*int64)(unsafe.Pointer(p)), n)
		for i := 0; i < n; i++ {
			ck(s[i] == int64(-100+i*7), fmt.Sprintf("C array elem %d", i))
		}
		gcStress()
		for i := 0; i < n; i++ {
			ck(s[i] == int64(-100+i*7), fmt.Sprintf("C array elem after GC %d", i))
		}
		runtime.KeepAlive(s)
		C.free(unsafe.Pointer(p))
	}

	// 静态数组：C 返回指针 + 已知长度，不可 free。
	q := C.c_static_i32s()
	qs := unsafe.Slice((*int32)(unsafe.Pointer(q)), 4)
	ck(qs[0] == 10 && qs[1] == 20 && qs[2] == 30 && qs[3] == 40, "C static array")
	gcStress()
	ck(qs[3] == 40, "C static array after GC")

	// 指针 + 出参长度：字符串。
	var lenOut C.size_t
	m := C.c_message(&lenOut)
	ck(m != nil, "C message pointer")
	ck(int(lenOut) == len("cgo-ptr-len"), "C message length")
	ck(C.GoStringN(m, C.int(lenOut)) == "cgo-ptr-len", "C message content")
	gcStress()
	ck(C.GoStringN(m, C.int(lenOut)) == "cgo-ptr-len", "C message after GC")

	// NULL 返回值必须可判别；零长度 unsafe.Slice 合法。
	nullp := C.c_null_ptr()
	ck(nullp == nil, "C NULL pointer detected")
	zs := unsafe.Slice((*int64)(unsafe.Pointer(nullp)), 0)
	ck(len(zs) == 0, "unsafe.Slice(NULL, 0) empty")

	// 多轮 malloc/free + GC，验证 C 由指针返回的内存不被 Go GC 干扰。
	for r := 0; r < 100; r++ {
		rp := C.c_make_i64s(16, C.int64_t(r))
		if rp == nil {
			ck(false, "C malloc in loop")
			break
		}
		rs := unsafe.Slice((*int64)(unsafe.Pointer(rp)), 16)
		runtime.GC()
		ck(rs[0] == int64(r) && rs[15] == int64(r)+15*7,
			fmt.Sprintf("C returned array round=%d after GC", r))
		C.free(unsafe.Pointer(rp))
	}
	finish("l09_cgo_return_ptr_len")
}
