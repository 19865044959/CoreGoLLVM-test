// L09: POSIX regex（regcomp/regexec/regfree）经 C 辅助函数调用：
// 覆盖捕获组、POSIX leftmost-longest（a|ab 在 ab 上取 ab）、锚点、
// 字符类、重复组、无效模式、无匹配、空匹配、显式匹配范围上限，
// 结果偏移与 Go 字符串切片逐一对照。C 每次调用独立编译/释放，不保留
// 任何 Go 指针。
package main

/*
#include <stdint.h>
#include <stdlib.h>
#include "rg.h"
*/
import "C"

import (
	"fmt"
	"os"
	"runtime"
	"slices"
	"sync/atomic"
	"unsafe"
)

const caseName = "l09_cgo_posix_regex"

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

type rng struct {
	s, e int
}

func reExec(pat, text string, max int) ([]rng, int) {
	cp := C.CString(pat)
	ct := C.CString(text)
	defer C.free(unsafe.Pointer(cp))
	defer C.free(unsafe.Pointer(ct))
	buf := make([]C.mr_t, max)
	var n C.int
	rc := int(C.rg_exec(cp, ct, (*C.mr_t)(unsafe.Pointer(&buf[0])), C.int(max), &n))
	out := make([]rng, 0, int(n))
	for i := 0; i < int(n); i++ {
		out = append(out, rng{int(buf[i].s), int(buf[i].e)})
	}
	runtime.KeepAlive(buf)
	return out, rc
}

func sub(text string, r rng) string {
	if r.s < 0 || r.e < r.s || r.e > len(text) {
		return "<bad-range>"
	}
	return text[r.s:r.e]
}

func main() {
	ck(int(C.rg_max_ranges()) >= 8, "regex range capacity")

	// 捕获组 + 数字量词。
	r, rc := reExec("([a-z]+)-([0-9]{2,4})", "xx abc-123 yy", 8)
	ck(rc == 0, "basic match rc")
	ck(slices.Equal(r, []rng{{3, 10}, {3, 6}, {7, 10}}), "basic ranges")
	if len(r) == 3 {
		ck(sub("xx abc-123 yy", r[0]) == "abc-123", "basic full match")
		ck(sub("xx abc-123 yy", r[1]) == "abc", "basic group1")
		ck(sub("xx abc-123 yy", r[2]) == "123", "basic group2")
	}

	// 整体匹配取 leftmost-longest（abcd）；子表达式切分随 libc，
	// 但两组合起来必须正好覆盖整体匹配。
	r, rc = reExec("(a|ab)(c|bcd)", "abcd", 8)
	ck(rc == 0 && len(r) == 3 && r[0] == (rng{0, 4}), "leftmost-longest full match")
	if len(r) == 3 {
		ck(sub("abcd", r[1])+sub("abcd", r[2]) == "abcd", "subexpressions cover full match")
	}
	r, rc = reExec("foo|foobar", "foobar", 8)
	ck(rc == 0 && slices.Equal(r, []rng{{0, 6}}), "leftmost-longest alternation")

	// 锚点与字符类。
	r, rc = reExec("^[0-9]+$", "12345", 8)
	ck(rc == 0 && slices.Equal(r, []rng{{0, 5}}), "anchored digits match")
	r, rc = reExec("^[0-9]+$", "12a", 8)
	ck(rc == 1 && len(r) == 0, "anchored digits reject")
	r, rc = reExec("[[:digit:]]+", "ab987cd", 8)
	ck(rc == 0 && slices.Equal(r, []rng{{2, 5}}), "POSIX character class")
	r, rc = reExec("([[:alpha:]]+)[ =]([[:digit:]]+)", "key=42", 8)
	ck(rc == 0 && slices.Equal(r, []rng{{0, 6}, {0, 3}, {4, 6}}), "alpha/digit classes with groups")
	if len(r) == 3 {
		ck(sub("key=42", r[1]) == "key" && sub("key=42", r[2]) == "42", "class group substrings")
	}

	// 空匹配：x* 在 bbb 起始处匹配空串。
	r, rc = reExec("x*", "bbb", 8)
	ck(rc == 0 && slices.Equal(r, []rng{{0, 0}}), "empty match")
	// 无匹配与无效模式可区分。
	r, rc = reExec("z+", "abc", 8)
	ck(rc == 1 && len(r) == 0, "no match rc")
	r, rc = reExec("([", "abc", 8)
	ck(rc == -1 && len(r) == 0, "invalid pattern rc")

	// 重复组：整段匹配确定；不依赖未定义的多轮捕获语义。
	r, rc = reExec("(ab|cd)+", "abcdab", 8)
	ck(rc == 0 && slices.Equal(r[:1], []rng{{0, 6}}), "repeated group full match")

	// 转义、大小写敏感、点号匹配换行（未设 REG_NEWLINE）。
	r, rc = reExec("a\\+b", "a+b", 8)
	ck(rc == 0 && slices.Equal(r, []rng{{0, 3}}), "escaped plus literal")
	r, rc = reExec("a\\+b", "aab", 8)
	ck(rc == 1, "escaped plus rejects aab")
	r, rc = reExec("[a-z]+", "ABC", 8)
	ck(rc == 1, "case sensitive")
	r, rc = reExec("^b", "a\nb", 8)
	ck(rc == 1, "no REG_NEWLINE anchors stay string-wide")
	r, rc = reExec("a.b", "a\nb", 8)
	ck(rc == 0 && slices.Equal(r, []rng{{0, 3}}), "dot matches newline by default")

	// 显式范围上限：max=2 只返回整体 + 第一组；max=1 只返回整体。
	r, rc = reExec("([a-z]+)-([0-9]+)", "ab-12", 2)
	ck(rc == 0 && slices.Equal(r, []rng{{0, 5}, {0, 2}}), "range cap 2")
	r, rc = reExec("([a-z]+)-([0-9]+)", "ab-12", 1)
	ck(rc == 0 && slices.Equal(r, []rng{{0, 5}}), "range cap 1")

	// 50 轮重复 + 周期 GC，结果稳定。
	pat, text, want := "([[:alpha:]]+)-([[:digit:]]+)", "id-2026", []rng{{0, 7}, {0, 2}, {3, 7}}
	for i := 0; i < 50; i++ {
		got, rc := reExec(pat, text, 8)
		ck(rc == 0 && slices.Equal(got, want), fmt.Sprintf("gc round %d ranges", i))
		if i%10 == 0 {
			gcStress()
		}
	}
	gcStress()
	got, rc := reExec(pat, text, 8)
	ck(rc == 0 && slices.Equal(got, want), "post-GC ranges")
	ck(sub(text, got[2]) == "2026", "post-GC substring")

	finish(caseName)
}
