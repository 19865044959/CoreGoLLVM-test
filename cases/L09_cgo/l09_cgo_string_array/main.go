// L09: C 字符串数组（char **，NULL 结尾）：静态表、C malloc 表、以及
// Go 用 C.CString 组装的表。Go 直接以 []*C.char 遍历并 C.GoString 复制，
// C 只在调用期内读指针，不保留 Go 指针。
package main

/*
#include <stdint.h>
#include <stdlib.h>
#include "strarray.h"
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

var staticWant = []string{"alpha", "", "beta-2", "hello, cgo", "zeta"}

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

func dynWant(i int) string {
	switch {
	case i == 3:
		return ""
	case i == 7:
		b := make([]byte, 200)
		for j := range b {
			b[j] = byte('a' + (i+j)%26)
		}
		return string(b)
	default:
		return fmt.Sprintf("item-%02d", i)
	}
}

func joinLen(v []string, sep byte) int64 {
	var n int64
	for _, s := range v {
		n += int64(len(s))
	}
	if len(v) > 1 {
		n += int64(len(v)-1) * int64(sep)
	}
	return n
}

func main() {
	// 静态只读字符串数组。
	var sn C.int32_t
	sp := C.c_static_names(&sn)
	ck(sp != nil, "static string array")
	ck(int(sn) == len(staticWant), "static string count")
	ck(C.c_strv_count(sp) == sn, "C count of static array")
	sarr := unsafe.Slice(sp, int(sn)+1)
	ck(sarr[int(sn)] == nil, "static array NULL terminated")
	for i, want := range staticWant {
		ck(C.GoString(sarr[i]) == want, fmt.Sprintf("static[%d]", i))
		ck(C.GoString(C.c_strv_get(sp, C.int32_t(i))) == want, fmt.Sprintf("static get[%d]", i))
	}
	ck(C.c_strv_get(sp, sn) == nil, "static get past end is NULL")
	ck(int64(C.c_strv_join_len(sp, C.int32_t('|'))) == joinLen(staticWant, '|'), "static join length")

	// C malloc 的字符串数组（含空串与 200 字节长串）。
	var dn C.int32_t
	dp := C.c_alloc_names(&dn)
	ck(dp != nil, "C allocated string array")
	ck(int(dn) == 12, "C allocated string count")
	if dp != nil {
		darr := unsafe.Slice(dp, int(dn)+1)
		ck(darr[int(dn)] == nil, "dynamic array NULL terminated")
		ck(C.c_strv_count(dp) == dn, "C count of dynamic array")
		dyn := make([]string, int(dn))
		for i := range dyn {
			dyn[i] = dynWant(i)
			ck(C.GoString(darr[i]) == dyn[i], fmt.Sprintf("dynamic[%d]", i))
			ck(C.GoString(C.c_strv_get(dp, C.int32_t(i))) == dyn[i], fmt.Sprintf("dynamic get[%d]", i))
		}
		ck(len(dyn[7]) == 200, "dynamic long string length")
		ck(int64(C.c_strv_join_len(dp, C.int32_t(','))) == joinLen(dyn, ','), "dynamic join length")
		ck(C.c_strv_get(dp, C.int32_t(-1)) == nil, "negative index is NULL")
		ck(C.c_strv_get(dp, dn) == nil, "dynamic get past end is NULL")

		// 跨 GC 复核 C 内存内容（Go 只保存 C 指针）。
		for round := 0; round < 8; round++ {
			gcStress()
			ck(C.c_strv_count(sp) == sn, fmt.Sprintf("static count round=%d", round))
			ck(C.c_strv_count(dp) == dn, fmt.Sprintf("dynamic count round=%d", round))
			for i := 0; i < len(staticWant); i += 2 {
				ck(C.GoString(sarr[i]) == staticWant[i], fmt.Sprintf("static round=%d i=%d", round, i))
			}
			for i := 0; i < len(dyn); i += 3 {
				ck(C.GoString(darr[i]) == dyn[i], fmt.Sprintf("dynamic round=%d i=%d", round, i))
			}
			runtime.KeepAlive(sp)
			runtime.KeepAlive(dp)
			runtime.KeepAlive(sarr)
			runtime.KeepAlive(darr)
		}
		C.c_free_names(dp)
	}

	// Go 组装的 char**：元素是 C.CString（C 内存），数组本身在 Go 堆。
	goStrings := []string{"one", "two", "", "four", "five"}
	carr := make([]*C.char, len(goStrings)+1)
	for i, s := range goStrings {
		carr[i] = C.CString(s)
	}
	carr[len(goStrings)] = nil
	ck(int32(C.c_strv_count(&carr[0])) == int32(len(goStrings)), "Go-built array count")
	ck(int64(C.c_strv_join_len(&carr[0], C.int32_t(','))) == joinLen(goStrings, ','), "Go-built join length")
	for i, s := range goStrings {
		ck(C.GoString(carr[i]) == s, fmt.Sprintf("Go-built[%d]", i))
	}
	runtime.KeepAlive(carr)
	for i := range goStrings {
		C.free(unsafe.Pointer(carr[i]))
	}

	finish("l09_cgo_string_array")
}
