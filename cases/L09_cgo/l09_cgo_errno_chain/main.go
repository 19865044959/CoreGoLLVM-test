// L09: errno 链：C 辅助函数在同一次调用内设置 errno 并立刻写入出参，
// Go 侧再映射为 syscall.Errno / errors.Is。覆盖 ENOENT（空路径与不存在
// 路径）、EISDIR（读目录 fd）、EBADF（写只读 fd、读已关闭 fd）、错误
// 消息缓冲截断与跨 GC 稳定性。C 不保留任何 Go 指针。
package main

/*
#include <stdint.h>
#include <stddef.h>
#include <stdlib.h>
#include "ec.h"
*/
import "C"

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"runtime"
	"sync/atomic"
	"syscall"
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

func openRO(path string) (int32, int32) {
	cp := C.CString(path)
	defer C.free(unsafe.Pointer(cp))
	var err C.int
	fd := int32(C.ec_open_ro(cp, &err))
	return fd, int32(err)
}

func errMsg(code int32, cap int) (string, int) {
	if cap == 0 {
		return "", int(C.ec_strerror(C.int(code), nil, 0))
	}
	buf := make([]byte, cap)
	n := int(C.ec_strerror(C.int(code), (*C.char)(unsafe.Pointer(&buf[0])), C.size_t(cap)))
	if n < 0 {
		return "", n
	}
	return string(buf[:n]), n
}

func main() {
	const cname = "l09_cgo_errno_chain"

	enoent := int32(C.ec_errno_enoent())
	eisdir := int32(C.ec_errno_eisdir())
	ebadf := int32(C.ec_errno_ebadf())
	ck(enoent == int32(syscall.ENOENT), "ENOENT value")
	ck(eisdir == int32(syscall.EISDIR), "EISDIR value")
	ck(ebadf == int32(syscall.EBADF), "EBADF value")

	// 链 1：open 失败 → ENOENT，Go 侧归类为 fs.ErrNotExist。
	fd, code := openRO("")
	ck(fd < 0 && code == enoent, "open empty path ENOENT")
	ck(errors.Is(syscall.Errno(code), fs.ErrNotExist), "ENOENT maps to fs.ErrNotExist")
	fd, code = openRO("/nonexistent/l09_cgo_errno_chain/leaf")
	ck(fd < 0 && code == enoent, "open missing path ENOENT")

	// 链 2：以只读方式打开目录 → 读 EISDIR、写 EBADF、close 成功后 errno 清零。
	dirfd, code := openRO(".")
	ck(dirfd >= 0 && code == 0, "open . succeeds")
	var err C.int = -1
	var rbuf [8]byte
	r := int32(C.ec_read_fd(C.int(dirfd), unsafe.Pointer(&rbuf[0]), C.int(len(rbuf)), &err))
	ck(r < 0 && int32(err) == eisdir, "read directory EISDIR")
	cbuf := C.CString("x")
	r = int32(C.ec_write_fd(C.int(dirfd), cbuf, 1, &err))
	C.free(unsafe.Pointer(cbuf))
	ck(r < 0 && int32(err) == ebadf, "write read-only fd EBADF")
	ck(int32(C.ec_close_fd(C.int(dirfd), &err)) == 0 && int32(err) == 0, "close directory succeeds")
	r = int32(C.ec_read_fd(C.int(dirfd), unsafe.Pointer(&rbuf[0]), C.int(len(rbuf)), &err))
	ck(r < 0 && int32(err) == ebadf, "read closed fd EBADF")
	ck(int32(C.ec_close_fd(C.int(dirfd), &err)) == -1 && int32(err) == ebadf, "double close EBADF")

	// 链 3：错误码 → 消息缓冲（含截断与零容量），消息稳定且互不相同。
	m1, n1 := errMsg(enoent, 128)
	m2, n2 := errMsg(eisdir, 128)
	m3, n3 := errMsg(ebadf, 128)
	ck(n1 > 0 && n2 > 0 && n3 > 0, "strerror lengths")
	ck(m1 != "" && m2 != "" && m3 != "", "strerror non-empty")
	ck(m1 != m2 && m1 != m3 && m2 != m3, "strerror messages distinct")
	again, _ := errMsg(enoent, 128)
	ck(again == m1, "strerror stable across calls")
	trunc, tn := errMsg(enoent, 4)
	ck(tn == -1 && trunc == "" && n1 >= 4, "strerror truncation rejected")
	_, zn := errMsg(enoent, 0)
	ck(zn == -1, "strerror zero capacity rejected")
	for _, code := range []int32{enoent, eisdir, ebadf} {
		ck(syscall.Errno(code).Error() != "", "syscall.Errno Error non-empty")
	}

	// 链 4：100 轮 open→close(-1) 错误链，每轮错误码都用最新一次调用的出参。
	for i := 0; i < 100; i++ {
		fd, code := openRO("")
		ck(fd < 0 && code == enoent, fmt.Sprintf("round %d open ENOENT", i))
		ck(int32(C.ec_close_fd(-1, &err)) == -1 && int32(err) == ebadf, fmt.Sprintf("round %d close(-1) EBADF", i))
		ck(int32(C.ec_lseek_fd(-1, 0, C.ec_seek_set(), &err)) == -1 && int32(err) == ebadf, fmt.Sprintf("round %d lseek(-1) EBADF", i))
		if i%10 == 0 {
			gcStress()
		}
	}

	// GC 后同一组错误码与消息仍正确。
	gcStress()
	fd, code = openRO("")
	ck(fd < 0 && code == enoent, "post-GC open ENOENT")
	after, _ := errMsg(ebadf, 128)
	ck(after == m3, "post-GC strerror stable")

	finish(cname)
}
