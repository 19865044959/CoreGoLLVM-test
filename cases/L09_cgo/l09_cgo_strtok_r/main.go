// L09: C strtok_r 分词：Go 提供 C.CString 缓冲区（C 内存），C 库在原位
// 写入 NUL 切分 token；覆盖多分隔符、空 token 跳过、空串、长 token、
// 两个 strtok_r 流交错（saveptr 独立）以及跨 GC 的 token 稳定性。
// C 只在调用期内访问指针，不保留任何 Go 指针。
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

// runTokens 在 C 缓冲区上调用 strtok_r，返回 token 副本、缓冲区原始字节
// （含结尾 NUL）与调用总次数（含最后返回 NULL 的一次）。
func runTokens(text, delim string, gcEvery int) ([]string, []byte, int) {
	ctext := C.CString(text)
	cdelim := C.CString(delim)
	defer C.free(unsafe.Pointer(ctext))
	defer C.free(unsafe.Pointer(cdelim))

	var save *C.char
	p := ctext
	out := []string{}
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
	return out, raw, calls
}

func main() {
	const cname = "l09_cgo_strtok_r"

	// 基本分词 + 原位 NUL 改写（缓冲区长度不变）。
	toks, raw, calls := runTokens("alpha,beta,gamma", ",", 0)
	ck(slices.Equal(toks, []string{"alpha", "beta", "gamma"}), "basic tokens")
	ck(calls == 4, "basic call count")
	ck(len(raw) == 17 && raw[5] == 0 && raw[10] == 0, "delimiters rewritten to NUL")
	ck(raw[0] == 'a' && raw[6] == 'b' && raw[11] == 'g' && raw[16] == 0, "buffer bytes preserved")

	// 多分隔符 + 连续/首尾分隔符：空 token 被跳过。
	toks, _, _ = runTokens(",,alpha;;beta, ,gamma,,", ",; ", 0)
	ck(slices.Equal(toks, []string{"alpha", "beta", "gamma"}), "multi delimiters skip empties")

	// 跳过连续分隔符时只把 token 后第一个分隔符写成 NUL。
	toks, raw, _ = runTokens("alpha,beta;;gamma", ",;", 0)
	ck(slices.Equal(toks, []string{"alpha", "beta", "gamma"}), "adjacent delimiters tokens")
	ck(raw[5] == 0 && raw[10] == 0, "token terminators written")
	ck(raw[11] == ';', "skipped delimiter keeps original byte")

	// 无分隔符 / 全分隔符 / 空串。
	toks, _, calls = runTokens("single", ",", 0)
	ck(slices.Equal(toks, []string{"single"}) && calls == 2, "single token")
	toks, _, calls = runTokens(",,;;", ",;", 0)
	ck(len(toks) == 0 && calls == 1, "all delimiters yields no token")
	toks, _, calls = runTokens("", ",", 0)
	ck(len(toks) == 0 && calls == 1, "empty string yields no token")

	// 长 token（200 字节）与尾部 token。
	long := ""
	for i := 0; i < 200; i++ {
		long += string(rune('a' + i%26))
	}
	toks, _, _ = runTokens(long+",tail", ",", 0)
	ck(len(toks) == 2 && len(toks[0]) == 200 && toks[0] == long && toks[1] == "tail", "long token")

	// 两个独立 saveptr 交错推进：验证 strtok_r 可重入。
	b1 := C.CString("a1,b2,c3")
	d1 := C.CString(",")
	b2 := C.CString("x|y|z")
	d2 := C.CString("|")
	var s1, s2 *C.char
	p1, p2 := b1, b2
	type pair struct{ a, b string }
	var got []pair
	for i := 0; i < 4; i++ {
		t1 := C.strtok_r(p1, d1, &s1)
		t2 := C.strtok_r(p2, d2, &s2)
		p1, p2 = nil, nil
		as, bs := "", ""
		if t1 != nil {
			as = C.GoString(t1)
		}
		if t2 != nil {
			bs = C.GoString(t2)
		}
		got = append(got, pair{as, bs})
		if i == 1 {
			gcStress()
		}
	}
	ck(slices.Equal(got, []pair{{"a1", "x"}, {"b2", "y"}, {"c3", "z"}, {"", ""}}), "interleaved strtok_r streams")
	C.free(unsafe.Pointer(b1))
	C.free(unsafe.Pointer(d1))
	C.free(unsafe.Pointer(b2))
	C.free(unsafe.Pointer(d2))

	// GC 夹在 token 循环中：token 指向 C 缓冲区，内容必须稳定。
	toks, _, _ = runTokens("gc00,gc01,gc02,gc03,gc04,gc05", ",", 2)
	ck(slices.Equal(toks, []string{"gc00", "gc01", "gc02", "gc03", "gc04", "gc05"}), "tokens stable across GC")

	finish(cname)
}
