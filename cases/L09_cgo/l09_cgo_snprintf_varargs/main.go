// L09: C snprintf 变参包装：cgo 绑定库通常用固定参数 C 包装层转发到
// printf 系列。覆盖 %lld/%llu/%x/%o、宽度/精度/标志、浮点格式、%s
// 宽度截断、返回值（所需长度）与缓冲截断/零容量语义；另验证 C 拥有
// 的缓冲区与跨 GC 稳定性。C 只在调用期内写缓冲区，不保留 Go 指针。
package main

/*
#include <stdint.h>
#include <stddef.h>
#include <stdlib.h>
#include "sv.h"
*/
import "C"

import (
	"fmt"
	"math"
	"os"
	"runtime"
	"sync/atomic"
	"unsafe"
)

const caseName = "l09_cgo_snprintf_varargs"

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

type sn struct {
	content string
	n       int
	buf     []byte
	cap     int
}

func makeSn(buf []byte, capacity, n int) sn {
	if capacity <= 0 || n < 0 {
		return sn{content: "", n: n, buf: buf, cap: capacity}
	}
	end := n
	if end > capacity-1 {
		end = capacity - 1
	}
	if end < 0 {
		end = 0
	}
	return sn{content: string(buf[:end]), n: n, buf: buf, cap: capacity}
}

func nulOK(r sn) bool {
	if r.cap == 0 {
		return true
	}
	idx := r.n
	if idx < 0 || idx >= r.cap {
		idx = r.cap - 1
	}
	return r.buf[idx] == 0
}

func bufArg(capacity int) ([]byte, *C.char) {
	if capacity <= 0 {
		return nil, nil
	}
	b := make([]byte, capacity)
	return b, (*C.char)(unsafe.Pointer(&b[0]))
}

func fInt(format string, v int64, capacity int) sn {
	cf := C.CString(format)
	defer C.free(unsafe.Pointer(cf))
	buf, p := bufArg(capacity)
	n := int(C.sv_int(p, C.size_t(capacity), cf, C.int64_t(v)))
	return makeSn(buf, capacity, n)
}

func fUint(format string, v uint64, capacity int) sn {
	cf := C.CString(format)
	defer C.free(unsafe.Pointer(cf))
	buf, p := bufArg(capacity)
	n := int(C.sv_uint(p, C.size_t(capacity), cf, C.uint64_t(v)))
	return makeSn(buf, capacity, n)
}

func fDouble(format string, v float64, capacity int) sn {
	cf := C.CString(format)
	defer C.free(unsafe.Pointer(cf))
	buf, p := bufArg(capacity)
	n := int(C.sv_double(p, C.size_t(capacity), cf, C.double(v)))
	return makeSn(buf, capacity, n)
}

func fStr(format, s string, capacity int) sn {
	cf := C.CString(format)
	cs := C.CString(s)
	defer C.free(unsafe.Pointer(cf))
	defer C.free(unsafe.Pointer(cs))
	buf, p := bufArg(capacity)
	n := int(C.sv_str(p, C.size_t(capacity), cf, cs))
	return makeSn(buf, capacity, n)
}

func fMixed(format string, a int32, b uint32, c float64, s string, capacity int) sn {
	cf := C.CString(format)
	cs := C.CString(s)
	defer C.free(unsafe.Pointer(cf))
	defer C.free(unsafe.Pointer(cs))
	buf, p := bufArg(capacity)
	n := int(C.sv_mixed(p, C.size_t(capacity), cf, C.int32_t(a), C.uint32_t(b), C.double(c), cs))
	return makeSn(buf, capacity, n)
}

func expect(r sn, want string, tag string) {
	ck(r.content == want, fmt.Sprintf("%s: content %q", tag, r.content))
	ck(r.n == len(want), fmt.Sprintf("%s: return length %d", tag, r.n))
	ck(nulOK(r), tag+": NUL terminated")
}

func main() {
	expect(fInt("%lld", math.MinInt64, 64), "-9223372036854775808", "min int64")
	expect(fInt("%+lld", 42, 32), "+42", "plus flag")
	expect(fInt("%-6lld|", 42, 32), "42    |", "left align width")
	expect(fInt("%06lld", -42, 32), "-00042", "zero pad negative")
	expect(fInt("v=%lld%%", 7, 32), "v=7%", "literal percent")

	expect(fUint("%llu", math.MaxUint64, 64), "18446744073709551615", "max uint64")
	expect(fUint("%#010llx", 0xbeef, 32), "0x0000beef", "hex prefix zero pad")
	expect(fUint("%llo", 8, 32), "10", "octal")

	expect(fDouble("%.2f", 3.14159, 64), "3.14", "fixed precision")
	expect(fDouble("%05.2f", -1.5, 64), "-1.50", "width with precision")
	expect(fDouble("%e", 1.5, 64), "1.500000e+00", "scientific")
	expect(fDouble("%g", 0.001, 64), "0.001", "general")
	expect(fDouble("%f", math.Copysign(0, -1), 64), "-0.000000", "negative zero")
	expect(fDouble("%.0f", 2.75, 64), "3", "round to integer")

	expect(fStr("%-8s|", "cgo", 64), "cgo     |", "left align string")
	expect(fStr("%.3s", "abcdef", 64), "abc", "string precision")
	expect(fStr("%10.4s", "abcdef", 64), "      abcd", "string width and precision")
	expect(fStr("%s", "", 64), "", "empty string")

	expect(fMixed("%d|%u|%.2f|%s", -7, 4000000000, 2.75, "ok", 128), "-7|4000000000|2.75|ok", "mixed varargs")

	// 截断：返回所需长度，缓冲区写 cap-1 字节并以 NUL 结尾。
	r := fStr("%s", "abcdefgh", 5)
	ck(r.n == 8, "truncated return length")
	ck(r.content == "abcd", "truncated content")
	ck(nulOK(r) && r.buf[4] == 0, "truncated NUL terminator")

	// 恰好容纳。
	r = fStr("%s", "abcd", 5)
	ck(r.n == 4 && r.content == "abcd" && r.buf[4] == 0, "exact fit")

	// 零容量：不写内存，只返回所需长度。
	r = fStr("%s", "abcdefgh", 0)
	ck(r.n == 8 && r.content == "" && len(r.buf) == 0, "zero capacity")

	// 容量 1：只写 NUL。
	r = fInt("%lld", 12345, 1)
	ck(r.n == 5 && r.content == "" && r.buf[0] == 0, "capacity one writes NUL only")

	// C 拥有的缓冲区：格式结果由 Go 读回复核后释放。
	cp := C.malloc(64)
	if cp == nil {
		ck(false, "malloc C buffer")
		finish(caseName)
		return
	}
	cf := C.CString("%lld")
	n := int(C.sv_int((*C.char)(cp), 64, cf, 123))
	ck(n == 3 && C.GoString((*C.char)(cp)) == "123", "C-owned buffer formatting")
	C.free(unsafe.Pointer(cf))
	C.free(cp)

	// 显式 GC 后重复格式化，结果一致。
	gcStress()
	expect(fInt("%06lld", -42, 32), "-00042", "post-GC int")
	expect(fDouble("%05.2f", -1.5, 64), "-1.50", "post-GC double")
	expect(fStr("%10.4s", "abcdef", 64), "      abcd", "post-GC string")
	expect(fMixed("%d|%u|%.2f|%s", -7, 4000000000, 2.75, "ok", 128), "-7|4000000000|2.75|ok", "post-GC mixed")

	finish(caseName)
}
