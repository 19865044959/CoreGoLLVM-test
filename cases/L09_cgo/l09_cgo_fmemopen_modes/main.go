// L09: C fmemopen 的定长缓冲区模式边界：C 分配缓冲区，"w" 精确填满与
// 中途 seek 重写后的 NUL/长度语义、close 时自动 flush、'a' 从首个 NUL
// 追加、'w+' 写入后回绕读取与 SEEK_CUR/SEEK_END、'r' 按行读取到边界
// EOF、内嵌 NUL 与无 NUL 定长读取。FILE* 与缓冲区均归 C，Go 只在调用期
// 内读写，C 不保留 Go 指针。
package main

/*
#include <stdint.h>
#include <stddef.h>
#include <stdlib.h>
#include "fm.h"
*/
import "C"

import (
	"fmt"
	"os"
	"runtime"
	"sync/atomic"
	"unsafe"
)

const caseName = "l09_cgo_fmemopen_modes"

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

func newBuf(n int) (unsafe.Pointer, []byte) {
	p := C.malloc(C.size_t(n))
	if p == nil {
		ck(false, "malloc buffer")
		finish(caseName)
	}
	b := unsafe.Slice((*byte)(p), n)
	for i := range b {
		b[i] = 'Z'
	}
	return p, b
}

func openMem(p unsafe.Pointer, size int, mode string) (unsafe.Pointer, int) {
	cm := C.CString(mode)
	defer C.free(unsafe.Pointer(cm))
	var err C.int
	f := C.fm_open(p, C.int(size), cm, &err)
	return f, int(err)
}

func put(f unsafe.Pointer, s string) int {
	b := []byte(s)
	if len(b) == 0 {
		return int(C.fm_put(f, nil, 0))
	}
	n := int(C.fm_put(f, (*C.char)(unsafe.Pointer(&b[0])), C.int(len(b))))
	runtime.KeepAlive(b)
	return n
}

func seek(f unsafe.Pointer, off int64, whence int) int {
	return int(C.fm_seek(f, C.long(off), C.int(whence)))
}

func gets(f unsafe.Pointer, cap int) (int, string) {
	b := make([]byte, cap)
	rc := int(C.fm_gets(f, (*C.char)(unsafe.Pointer(&b[0])), C.int(cap)))
	if rc != 1 {
		return rc, ""
	}
	n := 0
	for n < len(b) && b[n] != 0 {
		n++
	}
	return rc, string(b[:n])
}

func readBytes(f unsafe.Pointer, cap int) (int, int, []byte) {
	b := make([]byte, cap)
	var err C.int
	n := int(C.fm_read(f, (*C.char)(unsafe.Pointer(&b[0])), C.int(cap), &err))
	return n, int(err), b[:n]
}

func closeMem(f unsafe.Pointer) (int, int) {
	var err C.int
	rc := int(C.fm_close(f, &err))
	return rc, int(err)
}

func main() {
	const seekSet, seekCur, seekEnd = 0, 1, 2

	// 1) "w" 精确填满 size-1 个数据字节 + NUL，size 之外不动。
	p, b := newBuf(8)
	f, err := openMem(p, 5, "w")
	ck(f != nil && err == 0, fmt.Sprintf("open w: fNil=%v err=%d", f == nil, err))
	if f != nil {
		ck(put(f, "abcd") == 4, "w fwrite abcd")
		ck(int(C.fm_tell(f)) == 4, "w tell after abcd")
		ck(int(C.fm_flush(f)) == 0, "w flush")
		ck(string(b[:4]) == "abcd" && b[4] == 0, fmt.Sprintf("w exact-fit bytes=%q", b[:5]))
		for i := 5; i < 8; i++ {
			ck(b[i] == 'Z', fmt.Sprintf("w byte[%d] beyond size untouched", i))
		}
		rc, ce := closeMem(f)
		ck(rc == 0 && ce == 0, "w close")
	}
	C.free(p)

	// 2) "w" 中途 seek 重写：tell 跟随位置，flush 后旧尾部仍在。
	p, b = newBuf(16)
	for i := range b {
		b[i] = 0
	}
	f, err = openMem(p, 16, "w")
	if f != nil {
		ck(put(f, "hello world") == 11, "rewrite initial write")
		ck(int(C.fm_flush(f)) == 0, "rewrite flush1")
		ck(int(C.fm_tell(f)) == 11, "rewrite tell 11")
		ck(seek(f, 6, seekSet) == 0, "rewrite seek 6")
		ck(put(f, "CGO") == 3, "rewrite CGO")
		ck(int(C.fm_flush(f)) == 0, "rewrite flush2")
		ck(int(C.fm_tell(f)) == 9, "rewrite tell 9")
		ck(string(b[:11]) == "hello CGOld", fmt.Sprintf("rewrite bytes=%q", b[:11]))
		ck(b[11] == 0, "rewrite NUL at previous extent")
		rc, _ := closeMem(f)
		ck(rc == 0, "rewrite close")
	}
	C.free(p)

	// 3) "w" close 自动 flush：短内容在 close 后 NUL 终止。
	p, b = newBuf(8)
	f, _ = openMem(p, 8, "w")
	if f != nil {
		ck(put(f, "zz") == 2, "close-flush write")
		rc, _ := closeMem(f)
		ck(rc == 0, "close-flush close rc")
		ck(string(b[:2]) == "zz" && b[2] == 0, fmt.Sprintf("close-flush bytes=%q", b[:4]))
		ck(b[3] == 'Z', "close-flush byte[3] untouched")
	}
	C.free(p)

	// 4) "a" 从首个 NUL 追加。
	p, b = newBuf(16)
	b[0], b[1], b[2] = 'h', 'i', 0
	f, err = openMem(p, 16, "a")
	ck(f != nil && err == 0, "open append")
	if f != nil {
		ck(int(C.fm_tell(f)) == 2, "append initial tell 2")
		ck(put(f, " there") == 6, "append write")
		gcStress()
		ck(int(C.fm_flush(f)) == 0, "append flush")
		ck(int(C.fm_tell(f)) == 8, "append tell 8")
		ck(string(b[:8]) == "hi there" && b[8] == 0, fmt.Sprintf("append bytes=%q", b[:9]))
		for i := 9; i < 16; i++ {
			ck(b[i] == 'Z', fmt.Sprintf("append byte[%d] untouched", i))
		}
		rc, _ := closeMem(f)
		ck(rc == 0, "append close")
	}
	C.free(p)

	// 5) "w+" 写入 → 回绕读取 → EOF → SEEK_CUR/SEEK_END。
	p, b = newBuf(32)
	for i := range b {
		b[i] = 0
	}
	f, err = openMem(p, 32, "w+")
	ck(f != nil && err == 0, "open w+")
	if f != nil {
		ck(put(f, "xyz") == 3, "w+ write")
		ck(int(C.fm_flush(f)) == 0, "w+ flush")
		ck(seek(f, 0, seekSet) == 0, "w+ rewind")
		ck(int(C.fm_getc(f)) == 'x', "w+ getc x")
		ck(int(C.fm_getc(f)) == 'y', "w+ getc y")
		ck(int(C.fm_getc(f)) == 'z', "w+ getc z")
		ck(int(C.fm_getc(f)) == -1, "w+ getc EOF")
		ck(int(C.fm_eof(f)) == 1, "w+ feof")
		ck(seek(f, 0, seekSet) == 0, "w+ seek 0 clears EOF")
		ck(int(C.fm_getc(f)) == 'x', "w+ reread x")
		ck(int(C.fm_tell(f)) == 1, "w+ tell after x")
		ck(seek(f, -1, seekCur) == 0, "w+ seek -1 CUR")
		ck(int(C.fm_getc(f)) == 'x', "w+ seek CUR reread x")
		ck(seek(f, 0, seekEnd) == 0, "w+ seek END")
		ck(int(C.fm_tell(f)) == 3, "w+ tell at end")
		ck(int(C.fm_error(f)) == 0, "w+ no stream error")
		rc, _ := closeMem(f)
		ck(rc == 0, "w+ close")
	}
	C.free(p)

	// 6) "r" 按行读取，恰好到 size 边界后 EOF。
	p, b = newBuf(12)
	copy(b, "line1\nline2\n")
	f, err = openMem(p, 12, "r")
	ck(f != nil && err == 0, "open r lines")
	if f != nil {
		rc, line := gets(f, 32)
		ck(rc == 1 && line == "line1\n", fmt.Sprintf("r line1=%q rc=%d", line, rc))
		rc, line = gets(f, 32)
		ck(rc == 1 && line == "line2\n", fmt.Sprintf("r line2=%q rc=%d", line, rc))
		rc, line = gets(f, 32)
		ck(rc == 0 && line == "", "r EOF at size boundary")
		ck(int(C.fm_eof(f)) == 1 && int(C.fm_error(f)) == 0, "r eof/error flags")
		ck(seek(f, 0, seekSet) == 0, "r rewind")
		rc, line = gets(f, 32)
		ck(rc == 1 && line == "line1\n", "r line1 after rewind")
		rc, _ = closeMem(f)
		ck(rc == 0, "r close")
	}
	C.free(p)

	// 7) "r" 定长读取包含内嵌 NUL；到 size 后 EOF。
	p, b = newBuf(5)
	copy(b, []byte("ab\x00cd"))
	f, err = openMem(p, 5, "r")
	ck(f != nil && err == 0, "open r NUL")
	if f != nil {
		n, re, got := readBytes(f, 16)
		ck(n == 5 && re == 0, fmt.Sprintf("r NUL count=%d err=%d", n, re))
		ck(string(got) == "ab\x00cd", fmt.Sprintf("r NUL bytes=%q", got))
		n, _, _ = readBytes(f, 16)
		ck(n == 0 && int(C.fm_eof(f)) == 1, "r NUL EOF")
		rc, _ := closeMem(f)
		ck(rc == 0, "r NUL close")
	}
	C.free(p)

	// 8) "r" 无 NUL 的 8 字节窗口读到末尾后 EOF。
	p, b = newBuf(8)
	copy(b, "abcdefgh")
	f, _ = openMem(p, 8, "r")
	if f != nil {
		n, _, got := readBytes(f, 16)
		ck(n == 8 && string(got) == "abcdefgh", fmt.Sprintf("r nonul n=%d %q", n, got))
		n, _, _ = readBytes(f, 16)
		ck(n == 0 && int(C.fm_eof(f)) == 1, "r nonul EOF")
		rc, _ := closeMem(f)
		ck(rc == 0, "r nonul close")
	}
	C.free(p)

	finish(caseName)
}
