// L09: POSIX regex 的 REG_STARTEND 边界：C 每次调用独立 regcomp/regexec，
// 用 [start,end) 字节范围在含内嵌 NUL 的 Go 缓冲区上匹配，覆盖 NUL 之后
// 的匹配、范围截断、^/$ 与 REG_NOTBOL/REG_NOTEOL、nmatch 超过捕获组数
// 时的 -1 填充、嵌套/可选组、BRE 反向引用以及无效模式。正则对象在 C 堆，
// Go 缓冲区仅调用期内被 C 读取，C 不保留 Go 指针。
package main

/*
#include <stdint.h>
#include <stddef.h>
#include <stdlib.h>
#include "rx.h"
*/
import "C"

import (
	"fmt"
	"os"
	"runtime"
	"sync/atomic"
	"unsafe"
)

const caseName = "l09_cgo_regex_startend"

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

type span struct {
	s, e int32
}

func compileRE(pat string, cflags int) (unsafe.Pointer, int) {
	cp := C.CString(pat)
	defer C.free(unsafe.Pointer(cp))
	var err C.int
	re := C.rx_compile(cp, C.int(cflags), &err)
	return re, int(err)
}

func execRE(re unsafe.Pointer, text []byte, start, end, eflags, max int) ([]span, int, int) {
	spans := make([]C.rx_span_t, max)
	var nerr C.int
	rc := int(C.rx_exec(re, (*C.char)(unsafe.Pointer(&text[0])), C.int(len(text)),
		C.int(start), C.int(end), C.int(eflags),
		(*C.rx_span_t)(unsafe.Pointer(&spans[0])), C.int(max), &nerr))
	runtime.KeepAlive(text)
	out := make([]span, max)
	for i := range spans {
		out[i] = span{int32(spans[i].s), int32(spans[i].e)}
	}
	return out, rc, int(nerr)
}

func expect(tag string, got []span, want ...span) {
	if len(got) < len(want) {
		ck(false, fmt.Sprintf("%s: got %d spans, want %d", tag, len(got), len(want)))
		return
	}
	for i, w := range want {
		ck(got[i] == w, fmt.Sprintf("%s: span[%d]=%v want %v", tag, i, got[i], w))
	}
}

func main() {
	extended := int(C.rx_flag_extended())
	notbol := int(C.rx_flag_notbol())
	noteol := int(C.rx_flag_noteol())
	nomatch := int(C.rx_nomatch())

	ck(int(C.rx_flag_startend()) != 0, "REG_STARTEND available")
	ck(notbol != 0 && noteol != 0 && extended != 0, "regex flags available")

	// 内嵌 NUL：C 字符串会在 NUL 处截断，REG_STARTEND 必须越过它匹配。
	textNul := []byte("aa\x00bb")
	re, err := compileRE("bb", extended)
	ck(re != nil && err == 0, fmt.Sprintf("compile bb err=%d", err))
	if re != nil {
		sp, rc, nerr := execRE(re, textNul, 0, 5, 0, 8)
		ck(rc == 0 && nerr == 0, fmt.Sprintf("bb over NUL rc=%d nerr=%d", rc, nerr))
		expect("bb over NUL", sp, span{3, 5})
		for i := 1; i < len(sp); i++ {
			ck(sp[i] == span{-1, -1}, fmt.Sprintf("bb: span[%d] unused == -1", i))
		}
		_, rc, _ = execRE(re, textNul, 0, 4, 0, 8)
		ck(rc == nomatch, fmt.Sprintf("bb range end=4 rc=%d", rc))
		sp, rc, _ = execRE(re, textNul, 3, 5, 0, 8)
		ck(rc == 0, "bb range [3,5)")
		expect("bb [3,5)", sp, span{3, 5})
		_, rc, _ = execRE(re, textNul, 0, 3, 0, 8)
		ck(rc == nomatch, "bb range [0,3) nomatch")
		C.rx_free(re)
	}

	// ^：锚定整个字符串，不锚定 REG_STARTEND 的 start。
	re, err = compileRE("^aa", extended)
	ck(re != nil && err == 0, "compile ^aa")
	if re != nil {
		sp, rc, _ := execRE(re, textNul, 0, 5, 0, 4)
		ck(rc == 0, "^aa full rc")
		expect("^aa full", sp, span{0, 2})
		_, rc, _ = execRE(re, textNul, 0, 5, notbol, 4)
		ck(rc == nomatch, "^aa with NOTBOL nomatch")
		_, rc, _ = execRE(re, textNul, 1, 5, 0, 4)
		ck(rc == nomatch, "^aa with start=1 nomatch")
		C.rx_free(re)
	}

	// $：匹配 REG_STARTEND 的 end，REG_NOTEOL 抑制。
	text4 := []byte("aabb")
	re, err = compileRE("a+$", extended)
	ck(re != nil && err == 0, "compile a+$")
	if re != nil {
		sp, rc, _ := execRE(re, text4, 0, 2, 0, 4)
		ck(rc == 0, "a+$ range [0,2)")
		expect("a+$ [0,2)", sp, span{0, 2})
		_, rc, _ = execRE(re, text4, 0, 2, noteol, 4)
		ck(rc == nomatch, "a+$ NOTEOL nomatch")
		_, rc, _ = execRE(re, text4, 0, 4, 0, 4)
		ck(rc == nomatch, "a+$ range [0,4) nomatch")
		C.rx_free(re)
	}

	// 迭代查找所有 [0-9]+ 匹配。
	digits := []byte("a12b345c6")
	re, err = compileRE("[0-9]+", extended)
	ck(re != nil && err == 0, "compile digits")
	if re != nil {
		want := []span{{1, 3}, {4, 7}, {8, 9}}
		start := 0
		for i, w := range want {
			sp, rc, _ := execRE(re, digits, start, len(digits), 0, 2)
			ck(rc == 0, fmt.Sprintf("digits match %d rc=%d", i, rc))
			expect(fmt.Sprintf("digits %d", i), sp, w)
			start = int(w.e)
		}
		_, rc, _ := execRE(re, digits, start, len(digits), 0, 2)
		ck(rc == nomatch, "digits exhausted nomatch")
		C.rx_free(re)
	}

	// 嵌套捕获组与超出组数的 -1 填充。
	re, err = compileRE("((a+)(b+))", extended)
	ck(re != nil && err == 0, "compile nested")
	if re != nil {
		sp, rc, _ := execRE(re, []byte("aabbb"), 0, 5, 0, 8)
		ck(rc == 0, "nested rc")
		expect("nested", sp, span{0, 5}, span{0, 5}, span{0, 2}, span{2, 5})
		for i := 4; i < 8; i++ {
			ck(sp[i] == span{-1, -1}, fmt.Sprintf("nested unused span[%d] == -1", i))
		}
		C.rx_free(re)
	}

	// 可选组：未参与匹配的组为 -1,-1。
	re, err = compileRE("(a)(b)?", extended)
	ck(re != nil && err == 0, "compile optional")
	if re != nil {
		sp, rc, _ := execRE(re, []byte("a"), 0, 1, 0, 4)
		ck(rc == 0, "optional rc")
		expect("optional", sp, span{0, 1}, span{0, 1}, span{-1, -1})
		sp, rc, _ = execRE(re, []byte("ab"), 0, 2, 0, 4)
		ck(rc == 0, "optional ab rc")
		expect("optional ab", sp, span{0, 2}, span{0, 1}, span{1, 2})
		C.rx_free(re)
	}

	// BRE 反向引用（\) 在 BRE 中是分组，\1 是反向引用。
	re, err = compileRE(`\(ab\)\1`, 0)
	ck(re != nil && err == 0, fmt.Sprintf("compile BRE backref err=%d", err))
	if re != nil {
		sp, rc, _ := execRE(re, []byte("abab"), 0, 4, 0, 2)
		ck(rc == 0, "BRE backref match")
		expect("BRE backref", sp, span{0, 4})
		_, rc, _ = execRE(re, []byte("abac"), 0, 4, 0, 2)
		ck(rc == nomatch, "BRE backref nomatch")
		C.rx_free(re)
	}

	// 无效模式：regcomp 失败，不返回可用对象。
	re, err = compileRE("(", extended)
	ck(re == nil && err != 0, fmt.Sprintf("invalid pattern err=%d", err))
	re, err = compileRE(`\1`, 0)
	ck(re == nil && err != 0, "invalid backreference")

	// 越界范围在进入 regexec 前被拒绝。
	re, err = compileRE("a", extended)
	if re != nil {
		_, rc, _ := execRE(re, []byte("a"), 1, 0, 0, 1)
		ck(rc == -2, "start>end rejected")
		C.rx_free(re)
	}

	// 复用编译结果 200 次，周期性显式 GC。
	re, err = compileRE("([a-z]+)-([0-9]{2,4})", extended)
	ck(re != nil && err == 0, "compile repeated")
	if re != nil {
		text := []byte("xx abc-123 yy")
		for i := 0; i < 200; i++ {
			sp, rc, _ := execRE(re, text, 0, len(text), 0, 4)
			ck(rc == 0, fmt.Sprintf("iter %d rc=%d", i, rc))
			expect(fmt.Sprintf("iter %d", i), sp, span{3, 10}, span{3, 6}, span{7, 10})
			if i%50 == 0 {
				gcStress()
			}
		}
		C.rx_free(re)
	}

	finish(caseName)
}
