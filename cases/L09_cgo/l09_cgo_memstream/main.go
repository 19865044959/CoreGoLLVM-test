// L09: C FILE* 流边界：open_memstream 写入（fwrite/fprintf/fflush/
// ftell/fseek）与 fmemopen 读取（fgets/feof/ferror/fseek）。FILE* 与
// 缓冲区全部归 C 管理，Go 只持有不透明指针并在调用期内读写；覆盖
// 偏移重写、EOF/错误状态、跨 GC 继续读写与最终释放。
package main

/*
#include <stdint.h>
#include <stddef.h>
#include <stdlib.h>
#include "ms.h"
*/
import "C"

import (
	"fmt"
	"os"
	"runtime"
	"sync/atomic"
	"unsafe"
)

const caseName = "l09_cgo_memstream"

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

func wwrite(w *C.mw_t, s string) int {
	b := []byte(s)
	if len(b) == 0 {
		return 0
	}
	return int(C.mw_write(w, (*C.char)(unsafe.Pointer(&b[0])), C.int(len(b))))
}

func snapshot(w *C.mw_t) (string, int) {
	var ln C.size_t
	p := C.mw_buf(w, &ln)
	if p == nil {
		return "", -1
	}
	return C.GoStringN(p, C.int(ln)), int(ln)
}

func main() {
	// ---- writer: open_memstream ----
	var rc C.int = -1
	w := C.mw_open(&rc)
	if rc != 0 || w == nil {
		ck(false, "open_memstream")
		finish(caseName)
		return
	}
	ck(wwrite(w, "hello") == 5, "fwrite hello")
	ck(int(C.mw_tell(w)) == 5, "tell after hello")
	ck(wwrite(w, " world") == 6, "fwrite world")
	ck(int(C.mw_tell(w)) == 11, "tell after world")
	cs := C.CString("cgo")
	ck(int(C.mw_printf(w, 7, cs)) == 5, "fprintf return")
	C.free(unsafe.Pointer(cs))
	ck(int(C.mw_tell(w)) == 16, "tell after fprintf")
	s, ln := snapshot(w)
	ck(ln == 16 && s == "hello world7:cgo", "memstream snapshot")
	ck(int(C.mw_error(w)) == 0, "no stream error")

	// 回到开头重写前 5 字节：flush 后长度更新为当前位置。
	ck(int(C.mw_seek(w, 0)) == 0, "seek to 0")
	ck(wwrite(w, "HELLO") == 5, "rewrite prefix")
	ck(int(C.mw_tell(w)) == 5, "tell after rewrite")
	s, ln = snapshot(w)
	ck(ln == 5 && s == "HELLO", "snapshot after rewrite")

	// 从旧长度末尾继续写：中间旧字节保留，长度变为 17。
	ck(int(C.mw_seek(w, 16)) == 0, "seek to old end")
	ck(wwrite(w, "!") == 1, "append at old end")
	s, ln = snapshot(w)
	ck(ln == 17 && s == "HELLO world7:cgo!", "snapshot after extend")

	var cbuf *C.char
	var clen C.size_t
	ck(int(C.mw_close(w, &cbuf, &clen)) == 0, "close memstream")
	ck(int(clen) == 17 && C.GoStringN(cbuf, C.int(clen)) == "HELLO world7:cgo!", "buffer after close")
	C.ms_free(cbuf)

	// GC 期间保持流打开：C FILE* 与缓冲区不受 Go GC 影响。
	w = C.mw_open(&rc)
	ck(rc == 0 && w != nil, "reopen memstream")
	ck(wwrite(w, "gc") == 2, "write before GC")
	cs = C.CString("x")
	ck(int(C.mw_printf(w, 9, cs)) == 3, "fprintf before GC")
	C.free(unsafe.Pointer(cs))
	gcStress()
	s, ln = snapshot(w)
	ck(ln == 5 && s == "gc9:x", "snapshot after GC")
	ck(int(C.mw_close(w, &cbuf, &clen)) == 0 && int(clen) == 5 && C.GoStringN(cbuf, C.int(clen)) == "gc9:x", "close after GC")
	C.ms_free(cbuf)

	// ---- reader: fmemopen ----
	text := "alpha|1\nbeta|2\ngamma|3"
	ct := C.CString(text)
	var rrc C.int = -1
	r := C.mr_open(ct, C.int(len(text)), &rrc)
	ck(rrc == 0 && r != nil, "fmemopen")
	if r == nil {
		C.free(unsafe.Pointer(ct))
		ck(false, "fmemopen reader")
		finish(caseName)
		return
	}
	C.free(unsafe.Pointer(ct))
	line := make([]byte, 32)
	readLine := func() (string, int) {
		n := int(C.mr_line(r, (*C.char)(unsafe.Pointer(&line[0])), C.int(len(line))))
		if n < 0 {
			return "", -1
		}
		return string(line[:n]), n
	}
	l1, n1 := readLine()
	ck(n1 == 8 && l1 == "alpha|1\n", "line 1")
	ck(int(C.mr_tell(r)) == 8, "tell after line 1")
	l2, n2 := readLine()
	ck(n2 == 7 && l2 == "beta|2\n", "line 2")
	l3, n3 := readLine()
	ck(n3 == 7 && l3 == "gamma|3", "line 3 without trailing newline")
	ck(int(C.mr_tell(r)) == 22, "tell after line 3")
	lx, nx := readLine()
	ck(nx == -1 && lx == "", "read past EOF")
	ck(int(C.mr_eof(r)) == 1, "EOF flag set")
	ck(int(C.mr_error(r)) == 0, "EOF is not an error")

	// 回绕重读与定位到中间行。
	ck(int(C.mr_seek(r, 0)) == 0, "reader seek 0")
	l1, n1 = readLine()
	ck(n1 == 8 && l1 == "alpha|1\n", "line 1 after seek")
	ck(int(C.mr_seek(r, 15)) == 0, "reader seek 15")
	l3, n3 = readLine()
	ck(n3 == 7 && l3 == "gamma|3", "line 3 after seek")
	ck(int(C.mr_seek(r, 8)) == 0, "reader seek 8")
	l2, n2 = readLine()
	ck(n2 == 7 && l2 == "beta|2\n", "line 2 after seek")

	// 20 轮 seek→读三行→EOF，期间显式 GC。
	for i := 0; i < 20; i++ {
		ck(int(C.mr_seek(r, 0)) == 0, fmt.Sprintf("round %d seek", i))
		if i%5 == 0 {
			gcStress()
		}
		a, _ := readLine()
		b, _ := readLine()
		c, _ := readLine()
		d, dn := readLine()
		ck(a == "alpha|1\n" && b == "beta|2\n" && c == "gamma|3", fmt.Sprintf("round %d lines", i))
		ck(dn == -1 && d == "", fmt.Sprintf("round %d EOF", i))
		ck(int(C.mr_eof(r)) == 1 && int(C.mr_error(r)) == 0, fmt.Sprintf("round %d eof/error flags", i))
	}
	C.mr_close(r)

	// 空行与多行混合。
	ct = C.CString("a\n\nb")
	rrc = -1
	r = C.mr_open(ct, 4, &rrc)
	ck(rrc == 0 && r != nil, "fmemopen empty line")
	if r != nil {
		a, _ := readLine()
		b, _ := readLine()
		c, _ := readLine()
		ck(a == "a\n" && b == "\n" && c == "b", "empty line preserved")
		_, dn := readLine()
		ck(dn == -1 && int(C.mr_eof(r)) == 1, "empty-line reader EOF")
		C.mr_close(r)
	}
	C.free(unsafe.Pointer(ct))

	// 最终 GC 后重开 reader 复核。
	gcStress()
	ct = C.CString(text)
	rrc = -1
	r = C.mr_open(ct, C.int(len(text)), &rrc)
	ck(rrc == 0 && r != nil, "final fmemopen")
	if r != nil {
		a, _ := readLine()
		ck(a == "alpha|1\n", "final line 1")
		C.mr_close(r)
	}
	C.free(unsafe.Pointer(ct))

	finish(caseName)
}
