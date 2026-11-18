// L09: C snprintf 的位置参数/动态宽度边界：固定参数包装层分别转发
// %*.*s、%*.*lld、%1$s/%2$lld、%1$lld%%%2$s、%1$*2$lld、%s%n-%hhn、
// %zu、%jd、%td、%hhd，覆盖负宽度、负精度（视为省略）、精度截断、
// 零填充、%n 计数、SIZE_MAX/INT64_MAX 与零容量/截断返回值语义。
// 缓冲区由 Go 在调用期内提供，C 只写不保留，C 不持有 Go 指针。
package main

/*
#include <stdint.h>
#include <stddef.h>
#include <stdlib.h>
#include "sp.h"
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

const caseName = "l09_cgo_snprintf_positional"

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

// snap 分配容量为 cap 的 Go 缓冲区（cap=0 时传 nil），执行 call 后返回
// 内容、snprintf 返回值以及 NUL 终止是否落在预期位置。
func snap(cap int, call func(buf *C.char, n C.int) C.int) (string, int, bool) {
	var b []byte
	var p *C.char
	if cap > 0 {
		b = make([]byte, cap)
		p = (*C.char)(unsafe.Pointer(&b[0]))
	}
	r := int(call(p, C.int(cap)))
	runtime.KeepAlive(b)
	if cap <= 0 {
		return "", r, true
	}
	idx := r
	if idx >= cap {
		idx = cap - 1
	}
	if idx < 0 {
		return "", r, false
	}
	n := idx
	return string(b[:n]), r, b[idx] == 0
}

func cpair(a, b string) (*C.char, *C.char) {
	return C.CString(a), C.CString(b)
}

func dynStr(f, s string, width, prec, cap int) (string, int, bool) {
	cf, cs := cpair(f, s)
	defer C.free(unsafe.Pointer(cf))
	defer C.free(unsafe.Pointer(cs))
	return snap(cap, func(b *C.char, n C.int) C.int {
		return C.sp_dyn_str(cf, C.int(width), C.int(prec), cs, b, n)
	})
}

func dynInt(f string, width, prec int, v int64, cap int) (string, int, bool) {
	cf := C.CString(f)
	defer C.free(unsafe.Pointer(cf))
	return snap(cap, func(b *C.char, n C.int) C.int {
		return C.sp_dyn_int(cf, C.int(width), C.int(prec), C.longlong(v), b, n)
	})
}

func posStrInt(f, s string, v int64, cap int) (string, int, bool) {
	cf, cs := cpair(f, s)
	defer C.free(unsafe.Pointer(cf))
	defer C.free(unsafe.Pointer(cs))
	return snap(cap, func(b *C.char, n C.int) C.int {
		return C.sp_pos_str_int(cf, cs, C.longlong(v), b, n)
	})
}

func posIntStr(f string, v int64, s string, cap int) (string, int, bool) {
	cf, cs := cpair(f, s)
	defer C.free(unsafe.Pointer(cf))
	defer C.free(unsafe.Pointer(cs))
	return snap(cap, func(b *C.char, n C.int) C.int {
		return C.sp_pos_int_str(cf, C.longlong(v), cs, b, n)
	})
}

func posWidth(f string, v int64, width, cap int) (string, int, bool) {
	cf := C.CString(f)
	defer C.free(unsafe.Pointer(cf))
	return snap(cap, func(b *C.char, n C.int) C.int {
		return C.sp_pos_width(cf, C.longlong(v), C.int(width), b, n)
	})
}

func nCount(s string, cap int) (string, int, bool, int, int) {
	cs := C.CString(s)
	defer C.free(unsafe.Pointer(cs))
	var n1 C.int
	var n2 C.schar
	str, r, ok := snap(cap, func(b *C.char, n C.int) C.int {
		return C.sp_n_count(cs, &n1, &n2, b, n)
	})
	return str, r, ok, int(n1), int(n2)
}

func sizeFmt(f string, v uint64, cap int) (string, int, bool) {
	cf := C.CString(f)
	defer C.free(unsafe.Pointer(cf))
	return snap(cap, func(b *C.char, n C.int) C.int {
		return C.sp_size(cf, C.size_t(v), b, n)
	})
}

func intmaxFmt(f string, v int64, cap int) (string, int, bool) {
	cf := C.CString(f)
	defer C.free(unsafe.Pointer(cf))
	return snap(cap, func(b *C.char, n C.int) C.int {
		return C.sp_intmax(cf, C.longlong(v), b, n)
	})
}

func ptrdiffFmt(f string, v int64, cap int) (string, int, bool) {
	cf := C.CString(f)
	defer C.free(unsafe.Pointer(cf))
	return snap(cap, func(b *C.char, n C.int) C.int {
		return C.sp_ptrdiff(cf, C.long(v), b, n)
	})
}

func hhdFmt(f string, v int, cap int) (string, int, bool) {
	cf := C.CString(f)
	defer C.free(unsafe.Pointer(cf))
	return snap(cap, func(b *C.char, n C.int) C.int {
		return C.sp_hhd(cf, C.int(v), b, n)
	})
}

func check(tag, got, want string, r, wantR int, okNul bool) {
	ck(got == want, fmt.Sprintf("%s: got %q want %q", tag, got, want))
	ck(r == wantR, fmt.Sprintf("%s: return %d want %d", tag, r, wantR))
	ck(okNul, tag+": missing NUL at expected position")
}

func main() {
	// 动态宽度/精度字符串。
	s, r, ok := dynStr("[%*.*s]", "abcdef", 8, 3, 64)
	check("dyn str width8 prec3", s, "[     abc]", r, 10, ok)
	s, r, ok = dynStr("[%*.*s]", "ab", -6, -1, 64)
	check("dyn str neg width/prec", s, "[ab    ]", r, 8, ok)
	s, r, ok = dynStr("[%*.*s]", "abc", 4, 0, 64)
	check("dyn str prec0", s, "[    ]", r, 6, ok)
	s, r, ok = dynStr("[%*.*s]", "abc", 0, 10, 64)
	check("dyn str prec>len", s, "[abc]", r, 5, ok)

	// 动态宽度/精度整数。
	s, r, ok = dynInt("[%*.*lld]", 10, 4, 42, 64)
	check("dyn int w10 p4", s, "[      0042]", r, 12, ok)
	s, r, ok = dynInt("%0*.*lld", 6, -1, -42, 64)
	check("dyn int zero pad neg", s, "-00042", r, 6, ok)
	s, r, ok = dynInt("%+*.*lld", 8, -1, 42, 64)
	check("dyn int plus pad", s, "     +42", r, 8, ok)

	// 位置参数。
	s, r, ok = posStrInt("%1$s/%2$lld", "tail", 42, 64)
	check("pos str/int", s, "tail/42", r, 7, ok)
	s, r, ok = posIntStr("%1$lld%%%2$s", 9, "z", 64)
	check("pos int/%str", s, "9%z", r, 3, ok)
	s, r, ok = posWidth("%1$*2$lld", 7, 6, 64)
	check("pos width", s, "     7", r, 6, ok)
	s, r, ok = posWidth("%1$*2$lld", 7, -6, 64)
	check("pos neg width", s, "7     ", r, 6, ok)

	// %n / %hhn 计数。
	s, r, ok, n1, n2 := nCount("xy", 64)
	check("%n count", s, "xy-", r, 3, ok)
	ck(n1 == 2, fmt.Sprintf("%%n n1=%d want 2", n1))
	ck(n2 == 3, fmt.Sprintf("%%hhn n2=%d want 3", n2))

	// 长度修饰符。
	s, r, ok = sizeFmt("%zu", math.MaxUint64, 64)
	check("size_t", s, "18446744073709551615", r, 20, ok)
	s, r, ok = intmaxFmt("%jd", math.MaxInt64, 64)
	check("intmax_t", s, "9223372036854775807", r, 19, ok)
	s, r, ok = ptrdiffFmt("%td", -12345, 64)
	check("ptrdiff_t", s, "-12345", r, 6, ok)
	s, r, ok = hhdFmt("%hhd", 200, 64)
	check("signed char wrap", s, "-56", r, 3, ok)

	// 零容量：返回所需长度，不写缓冲区。
	s, r, ok = dynStr("[%*.*s]", "abcdef", 8, 3, 0)
	ck(s == "" && r == 10 && ok, fmt.Sprintf("cap0 s=%q r=%d", s, r))

	// 截断：cap=6 只保留前 5 字节，NUL 在 [5]；cap=1 只写 NUL。
	s, r, ok = dynStr("[%*.*s]", "abcdef", 8, 3, 6)
	check("truncate cap6", s, "[    ", r, 10, ok)
	s, r, ok = dynStr("[%*.*s]", "abcdef", 8, 3, 1)
	check("truncate cap1", s, "", r, 10, ok)

	// 重复调用：容量变化但返回值恒定，NUL 位置正确，GC 不影响格式化。
	full := "[     abc]"
	wantR := 10
	for i := 0; i < 100; i++ {
		cap := 1 + i%40
		s, r, ok = dynStr("[%*.*s]", "abcdef", 8, 3, cap)
		keep := cap - 1
		if keep > len(full) {
			keep = len(full)
		}
		check(fmt.Sprintf("loop cap=%d", cap), s, full[:keep], r, wantR, ok)
		if i%25 == 0 {
			gcStress()
		}
	}

	finish(caseName)
}
