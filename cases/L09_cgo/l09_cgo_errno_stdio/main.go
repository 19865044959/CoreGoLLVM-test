// L09: stdio 路径的 errno 链：每个 C 辅助函数在同一调用内清零 errno、
// 执行 fopen/fwrite/fread/fgets/fclose/remove/rename/strtol 并立刻把
// errno 写入出参，Go 侧负责断言 ENOENT/EISDIR/ERANGE 与 LONG_MAX/MIN。
// 覆盖缺失文件、空路径、读目录、只读流写入、只写流读取、w+ 回读、
// 文件生命周期与跨显式 GC 的 FILE* 稳定性。FILE* 归 C 所有，
// C 不保留 Go 指针。
package main

/*
#include <stdint.h>
#include <stddef.h>
#include <stdlib.h>
#include "es.h"
*/
import "C"

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sync/atomic"
	"unsafe"
)

const caseName = "l09_cgo_errno_stdio"

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

func cstr(s string) *C.char { return C.CString(s) }

func openFile(path, mode string) (unsafe.Pointer, int) {
	cp := cstr(path)
	cm := cstr(mode)
	defer C.free(unsafe.Pointer(cp))
	defer C.free(unsafe.Pointer(cm))
	var err C.int
	f := C.es_open(cp, cm, &err)
	return f, int(err)
}

func closeFile(f unsafe.Pointer) (int, int) {
	var err C.int
	rc := int(C.es_close(f, &err))
	return rc, int(err)
}

func writeFile(f unsafe.Pointer, s string) (int, int) {
	b := []byte(s)
	var err C.int
	var n C.int
	if len(b) == 0 {
		n = C.es_write(f, nil, 0, &err)
	} else {
		n = C.es_write(f, (*C.char)(unsafe.Pointer(&b[0])), C.int(len(b)), &err)
	}
	runtime.KeepAlive(b)
	return int(n), int(err)
}

func readSome(f unsafe.Pointer, cap int) (int, int, []byte) {
	b := make([]byte, cap)
	var err C.int
	n := int(C.es_read(f, (*C.char)(unsafe.Pointer(&b[0])), C.int(cap), &err))
	return n, int(err), b[:n]
}

func gets(f unsafe.Pointer, cap int) (int, string) {
	b := make([]byte, cap)
	rc := int(C.es_gets(f, (*C.char)(unsafe.Pointer(&b[0])), C.int(cap)))
	if rc != 1 {
		return rc, ""
	}
	n := 0
	for n < len(b) && b[n] != 0 {
		n++
	}
	return rc, string(b[:n])
}

func strtol(s string, base int) (int64, int, int) {
	cp := cstr(s)
	defer C.free(unsafe.Pointer(cp))
	var err, end C.int
	v := C.es_strtol(cp, C.int(base), &err, &end)
	return int64(v), int(err), int(end)
}

func removePath(path string) (int, int) {
	cp := cstr(path)
	defer C.free(unsafe.Pointer(cp))
	var err C.int
	rc := int(C.es_remove(cp, &err))
	return rc, int(err)
}

func renamePath(a, b string) (int, int) {
	ca := cstr(a)
	cb := cstr(b)
	defer C.free(unsafe.Pointer(ca))
	defer C.free(unsafe.Pointer(cb))
	var err C.int
	rc := int(C.es_rename(ca, cb, &err))
	return rc, int(err)
}

func main() {
	enoent := int32(C.es_enoent())
	eisdir := int32(C.es_eisdir())
	erange := int32(C.es_erange())
	longMax := int64(C.es_long_max())
	longMin := int64(C.es_long_min())

	dir, err := os.MkdirTemp("", "l09_cgo_errno_stdio")
	if err != nil {
		ck(false, "MkdirTemp")
		finish(caseName)
		return
	}
	defer os.RemoveAll(dir)

	missing := filepath.Join(dir, "missing")
	roPath := filepath.Join(dir, "ro.txt")
	if err := os.WriteFile(roPath, []byte("alpha\nbeta\n"), 0o644); err != nil {
		ck(false, "WriteFile ro.txt")
		finish(caseName)
		return
	}

	// 1) 缺失路径 / 空路径 → ENOENT，指针为 nil。
	f, e := openFile(missing, "r")
	ck(f == nil && int32(e) == enoent, fmt.Sprintf("fopen missing: fNil=%v err=%d", f == nil, e))
	f, e = openFile("", "r")
	ck(f == nil && int32(e) == enoent, fmt.Sprintf("fopen empty path: fNil=%v err=%d", f == nil, e))

	// 2) 正常写入/关闭/重开读取整条 errno 链。
	rwPath := filepath.Join(dir, "rw.txt")
	f, e = openFile(rwPath, "w")
	ck(f != nil && e == 0, fmt.Sprintf("fopen w: fNil=%v err=%d", f == nil, e))
	if f != nil {
		n, we := writeFile(f, "alpha\nbeta\n")
		ck(n == 11 && we == 0, fmt.Sprintf("fwrite 11: n=%d err=%d", n, we))
		ck(int(C.es_tell(f)) == 11, "ftell after write")
		rc, ce := closeFile(f)
		ck(rc == 0 && ce == 0, fmt.Sprintf("fclose w: rc=%d err=%d", rc, ce))
	}

	f, e = openFile(rwPath, "r")
	ck(f != nil && e == 0, "fopen rw for read")
	if f != nil {
		rc, line := gets(f, 32)
		ck(rc == 1 && line == "alpha\n", fmt.Sprintf("fgets line1=%q rc=%d", line, rc))
		rc, line = gets(f, 32)
		ck(rc == 1 && line == "beta\n", fmt.Sprintf("fgets line2=%q rc=%d", line, rc))
		rc, line = gets(f, 32)
		ck(rc == 0 && line == "", fmt.Sprintf("fgets EOF rc=%d", rc))
		ck(int(C.es_eof(f)) == 1, "feof at EOF")
		ck(int(C.es_error(f)) == 0, "ferror clean at EOF")
		C.es_rewind(f)
		rc, line = gets(f, 32)
		ck(rc == 1 && line == "alpha\n", "fgets after rewind")
		gcStress()
		rc, line = gets(f, 32)
		ck(rc == 1 && line == "beta\n", "fgets after rewind+GC")
		rc, ce := closeFile(f)
		ck(rc == 0 && ce == 0, "fclose reader")
	}

	// 3) 只读流写入：fwrite 短写 0，流错误置位。
	f, _ = openFile(roPath, "r")
	if f != nil {
		n, _ := writeFile(f, "nope")
		ck(n == 0, fmt.Sprintf("fwrite to read-only n=%d", n))
		ck(int(C.es_error(f)) != 0, "ferror after read-only write")
		closeFile(f)
	}

	// 4) 只写流读取：fread 0 且流错误置位。
	woPath := filepath.Join(dir, "wo.txt")
	f, _ = openFile(woPath, "w")
	if f != nil {
		n, _, _ := readSome(f, 8)
		ck(n == 0, fmt.Sprintf("fread from write-only n=%d", n))
		ck(int(C.es_error(f)) != 0, "ferror after write-only read")
		closeFile(f)
	}

	// 5) 目录作为文件读取：ferror + EISDIR。
	f, e = openFile(dir, "r")
	ck(f != nil && e == 0, "fopen directory succeeds")
	if f != nil {
		n, re, _ := readSome(f, 64)
		ck(n == 0 && int32(re) == eisdir, fmt.Sprintf("fread dir n=%d err=%d", n, re))
		ck(int(C.es_error(f)) != 0, "ferror after dir read")
		closeFile(f)
	}

	// 6) w+ 写入 → 回绕读取 → EOF。
	wpPath := filepath.Join(dir, "wp.txt")
	f, e = openFile(wpPath, "w+")
	if f != nil {
		n, we := writeFile(f, "cgo")
		ck(n == 3 && we == 0, "w+ write cgo")
		C.es_rewind(f)
		n, _, b := readSome(f, 8)
		ck(n == 3 && string(b) == "cgo", fmt.Sprintf("w+ readback %q", b))
		n, _, _ = readSome(f, 8)
		ck(n == 0, "w+ second read returns 0")
		ck(int(C.es_eof(f)) == 1, "w+ EOF after readback")
		closeFile(f)
	}

	// 7) remove / rename 的 errno。
	rc, re := removePath(missing)
	ck(rc == -1 && int32(re) == enoent, fmt.Sprintf("remove missing rc=%d err=%d", rc, re))
	rc, re = renamePath(missing, filepath.Join(dir, "x"))
	ck(rc == -1 && int32(re) == enoent, fmt.Sprintf("rename missing rc=%d err=%d", rc, re))
	rc, re = removePath(woPath)
	ck(rc == 0 && re == 0, "remove existing")
	gcStress()

	// 8) strtol 溢出/部分解析/前导空白/进制。
	v, se, off := strtol("999999999999999999999999", 10)
	ck(v == longMax && int32(se) == erange, fmt.Sprintf("strtol overflow v=%d err=%d", v, se))
	ck(off == 24, fmt.Sprintf("strtol overflow endoff=%d", off))
	v, se, off = strtol("-999999999999999999999999", 10)
	ck(v == longMin && int32(se) == erange, fmt.Sprintf("strtol underflow v=%d err=%d", v, se))
	ck(off == 25, fmt.Sprintf("strtol underflow endoff=%d", off))
	v, se, off = strtol("123xyz", 10)
	ck(v == 123 && se == 0 && off == 3, fmt.Sprintf("strtol 123xyz v=%d err=%d off=%d", v, se, off))
	v, se, off = strtol("  42xyz", 10)
	ck(v == 42 && se == 0 && off == 4, fmt.Sprintf("strtol leading space v=%d err=%d off=%d", v, se, off))
	v, se, off = strtol("abc", 10)
	ck(v == 0 && se == 0 && off == 0, fmt.Sprintf("strtol abc v=%d err=%d off=%d", v, se, off))
	v, se, off = strtol("0x1f", 0)
	ck(v == 31 && se == 0 && off == 4, fmt.Sprintf("strtol 0x1f v=%d err=%d off=%d", v, se, off))

	finish(caseName)
}
