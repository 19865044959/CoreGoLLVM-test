// L09: C strsep 与 strtok_r 的空字段语义对照：两者都在调用期内原地把
// 分隔符改写为 NUL 并返回缓冲区内的 token 指针，strsep 保留连续/首尾
// 分隔符产生的空 token，strtok_r 则跳过它们。Go 校验 token 序列、
// 调用次数（含最后 NULL）、缓冲区最终字节（token 以 NUL 相连）以及
// token 循环中显式 GC 的稳定性。C 只操作 C.CString 缓冲区，不保留
// Go 指针。
package main

/*
#define _GNU_SOURCE
#include <string.h>
#include <stdlib.h>
*/
import "C"

import (
	"fmt"
	"os"
	"runtime"
	"slices"
	"strings"
	"sync/atomic"
	"unsafe"
)

const caseName = "l09_cgo_strsep_fields"

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

// splitSep 在独立 C 缓冲区上反复调用 strsep，返回 token 副本、调用次数
// （含最后返回 NULL 的一次）与分词后的缓冲区字节（含结尾 NUL）。
func splitSep(text, delim string, gcEvery int) ([]string, int, []byte) {
	ctext := C.CString(text)
	cdelim := C.CString(delim)
	defer C.free(unsafe.Pointer(ctext))
	defer C.free(unsafe.Pointer(cdelim))

	p := ctext
	var out []string
	calls := 0
	for {
		tok := C.strsep(&p, cdelim)
		calls++
		if tok == nil {
			break
		}
		out = append(out, C.GoString(tok))
		if gcEvery > 0 && calls%gcEvery == 0 {
			gcStress()
		}
	}
	raw := C.GoBytes(unsafe.Pointer(ctext), C.int(len(text)+1))
	runtime.KeepAlive(ctext)
	return out, calls, raw
}

// splitTok 用同一输入调用 strtok_r，用于对照空字段折叠行为。
func splitTok(text, delim string, gcEvery int) ([]string, int, []byte) {
	ctext := C.CString(text)
	cdelim := C.CString(delim)
	defer C.free(unsafe.Pointer(ctext))
	defer C.free(unsafe.Pointer(cdelim))

	var save *C.char
	p := ctext
	var out []string
	calls := 0
	for {
		tok := C.strtok_r(p, cdelim, &save)
		calls++
		if tok == nil {
			break
		}
		out = append(out, C.GoString(tok))
		p = nil
		if gcEvery > 0 && calls%gcEvery == 0 {
			gcStress()
		}
	}
	raw := C.GoBytes(unsafe.Pointer(ctext), C.int(len(text)+1))
	runtime.KeepAlive(ctext)
	return out, calls, raw
}

// scanMutated 按 strtok_r 的缓冲区契约重建 token：token 之后的第一个分隔符
// 必须已被改写为 NUL；未被消费的前导/连续分隔符保持原样。
func scanMutated(raw []byte, delim string) []string {
	var out []string
	isDelim := func(b byte) bool { return strings.IndexByte(delim, b) >= 0 }
	i := 0
	for i < len(raw) {
		for i < len(raw) && isDelim(raw[i]) {
			i++
		}
		if i >= len(raw) || raw[i] == 0 {
			break
		}
		start := i
		for i < len(raw) && raw[i] != 0 && !isDelim(raw[i]) {
			i++
		}
		out = append(out, string(raw[start:i]))
		if i < len(raw) && raw[i] != 0 {
			out = append(out, "<unterminated>")
			break
		}
		if i < len(raw) {
			i++
		}
	}
	return out
}

func check(tag, text, delim string, wantSep, wantTok []string, gcEvery int) {
	sep, sepCalls, sepRaw := splitSep(text, delim, gcEvery)
	ck(slices.Equal(sep, wantSep), fmt.Sprintf("%s: strsep tokens %q want %q", tag, sep, wantSep))
	ck(sepCalls == len(wantSep)+1, fmt.Sprintf("%s: strsep calls=%d want %d", tag, sepCalls, len(wantSep)+1))
	ck(len(sepRaw) == len(text)+1, fmt.Sprintf("%s: strsep raw len=%d", tag, len(sepRaw)))
	ck(string(sepRaw) == strings.Join(wantSep, "\x00")+"\x00", fmt.Sprintf("%s: strsep raw=%q", tag, sepRaw))

	tok, tokCalls, tokRaw := splitTok(text, delim, gcEvery)
	ck(slices.Equal(tok, wantTok), fmt.Sprintf("%s: strtok_r tokens %q want %q", tag, tok, wantTok))
	ck(tokCalls == len(wantTok)+1, fmt.Sprintf("%s: strtok_r calls=%d want %d", tag, tokCalls, len(wantTok)+1))
	ck(len(tokRaw) == len(text)+1, fmt.Sprintf("%s: strtok_r raw len=%d", tag, len(tokRaw)))
	ck(slices.Equal(scanMutated(tokRaw, delim), wantTok), fmt.Sprintf("%s: strtok_r buffer mutation %q", tag, tokRaw))
	ck(len(sep) >= len(tok), fmt.Sprintf("%s: strsep should preserve at least as many fields", tag))
	gcStress()
}

func main() {
	check("empty-fields", "a,,b,,", ",",
		[]string{"a", "", "b", "", ""}, []string{"a", "b"}, 2)
	check("multi-delim", "x;y,,z; ;w", ";, ",
		[]string{"x", "y", "", "z", "", "", "w"}, []string{"x", "y", "z", "w"}, 3)
	check("absent", "abc", ",",
		[]string{"abc"}, []string{"abc"}, 0)
	check("empty", "", ",",
		[]string{""}, nil, 2)
	check("leading", ",abc", ",",
		[]string{"", "abc"}, []string{"abc"}, 2)
	check("trailing", "abc,", ",",
		[]string{"abc", ""}, []string{"abc"}, 2)
	check("only-delims", ";;", ";",
		[]string{"", "", ""}, nil, 2)
	check("double-colon", "aa::bb::", ":",
		[]string{"aa", "", "bb", "", ""}, []string{"aa", "bb"}, 2)

	// 长 token 边界：单个 1000 字节字段 + 一个空尾字段。
	long := strings.Repeat("a", 1000) + ","
	check("long-token", long, ",", []string{strings.Repeat("a", 1000), ""}, []string{strings.Repeat("a", 1000)}, 4)

	finish(caseName)
}
