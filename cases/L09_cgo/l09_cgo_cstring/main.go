// L09: C.CString/C.GoString/C.CBytes/C.GoStringN 往返转换：
// ASCII、UTF-8、空串、内嵌 NUL 字节，以及 32 个 C 字符串跨 GC 存活后逐一校验并释放。
package main

/*
#include <stdint.h>
#include <stdlib.h>
#include <string.h>

static size_t c_strlen(const char *s) { return strlen(s); }
static int32_t c_byte_at(const char *s, int i) { return (int32_t)(unsigned char)s[i]; }
*/
import "C"

import (
	"fmt"
	"os"
	"runtime"
	"strings"
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

func roundtrip(s string, label string) {
	cs := C.CString(s)
	defer C.free(unsafe.Pointer(cs))
	ck(C.GoString(cs) == s, "GoString(CString) "+label)
	ck(uint64(C.c_strlen(cs)) == uint64(len(s)), "strlen(CString) "+label)
	for i := 0; i < len(s); i++ {
		if C.c_byte_at(cs, C.int(i)) != C.int32_t(s[i]) {
			ck(false, fmt.Sprintf("byte %d of %s", i, label))
			break
		}
	}
	gcStress()
	ck(C.GoString(cs) == s, "GoString after GC "+label)
}

func main() {
	roundtrip("hello", "ascii")
	roundtrip("", "empty")
	roundtrip("Go 汇编与 cgo 边界", "utf8")
	roundtrip(strings.Repeat("abcXYZ012", 25), "ascii-200")

	data := []byte{'a', 0, 'b', 0xff, 'c'}
	cb := C.CBytes(data)
	ck(cb != nil, "CBytes non-nil")
	ck(C.GoStringN((*C.char)(cb), C.int(len(data))) == string(data), "GoStringN keeps embedded NUL")
	got := C.GoString((*C.char)(cb))
	ck(got == "a", "GoString stops at NUL")
	C.free(cb)

	ck(C.GoString(nil) == "", "GoString(nil) empty")

	// 多个 C 字符串同时存活，跨 GC 校验。
	const n = 32
	strs := make([]*C.char, n)
	wants := make([]string, n)
	for i := range strs {
		wants[i] = fmt.Sprintf("cstring-%02d-é", i)
		strs[i] = C.CString(wants[i])
	}
	gcStress()
	for i := range strs {
		ck(C.GoString(strs[i]) == wants[i], fmt.Sprintf("live cstring %d after GC", i))
	}
	for i := range strs {
		C.free(unsafe.Pointer(strs[i]))
	}

	// 反复分配/释放，触发 GC，验证 C 堆与 Go GC 共存。
	for r := 0; r < 200; r++ {
		p := C.CString(wants[r%n])
		ck(C.GoString(p) == wants[r%n], "churn GoString")
		C.free(unsafe.Pointer(p))
		if r%25 == 0 {
			runtime.GC()
			b := make([]byte, 1024)
			b[0] = byte(r)
			_ = b
		}
	}
	finish("l09_cgo_cstring")
}
