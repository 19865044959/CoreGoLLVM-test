// L09: cgo 场景下的 C errno 处理：单次 C 调用内设置/读取 errno、
// 真实 fopen 失败后立即读取 errno，再映射为 Go 的 fs.ErrNotExist。
// C 只在调用期内读写 Go 栈上的 int32 出参，不保留 Go 指针。
package main

/*
#include <errno.h>
#include <stdint.h>
#include <stdio.h>

static int32_t c_errno_roundtrip(int32_t e) {
	errno = e;
	return (int32_t)errno;
}

static int32_t c_open_missing(int32_t *first, int32_t *after) {
	FILE *f = fopen("/nonexistent/gc-uaf-tests/l09_errno", "r");
	*after = (int32_t)errno;
	if (f != NULL) {
		fclose(f);
		*first = 0;
		return 0;
	}
	*first = (int32_t)errno;
	return -1;
}

static int32_t c_fail_with_enoent(void) {
	errno = 0;
	FILE *f = fopen("/nonexistent/gc-uaf-tests/l09_errno2", "r");
	if (f != NULL) {
		fclose(f);
		return 0;
	}
	return (int32_t)errno;
}
*/
import "C"

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"runtime"
	"sync"
	"sync/atomic"
	"syscall"
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

func main() {
	codes := []C.int32_t{C.EACCES, C.ENOENT, C.EINVAL, C.EPERM, 0}
	for i, e := range codes {
		ck(C.c_errno_roundtrip(e) == e, fmt.Sprintf("errno roundtrip code[%d]=%d", i, int32(e)))
	}

	var first, after C.int32_t
	rc := C.c_open_missing(&first, &after)
	ck(rc == -1, "fopen missing returns -1")
	ck(first == C.ENOENT, "errno after real failure is ENOENT")
	ck(after == C.ENOENT, "errno observed immediately inside C is ENOENT")
	err := syscall.Errno(first)
	ck(errors.Is(err, fs.ErrNotExist), "syscall.Errno(ENOENT) matches fs.ErrNotExist")
	ck(os.IsNotExist(err), "os.IsNotExist for ENOENT errno")
	ck(C.c_fail_with_enoent() == C.ENOENT, "errno cleared then set by failing fopen")

	gcStress()
	ck(C.c_errno_roundtrip(C.EACCES) == C.EACCES, "errno roundtrip after GC")

	// 并发调用：errno 是线程局部量，每次 C 调用内完成 set/get，结果必须稳定。
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			e := C.int32_t(int32(id)*3 + int32(C.EACCES))
			for r := 0; r < 200; r++ {
				ck(C.c_errno_roundtrip(e) == e, "concurrent errno roundtrip")
			}
			if id%2 == 0 {
				b := make([]byte, 2048)
				b[0] = byte(id)
				_ = b
				runtime.GC()
			}
		}(g)
	}
	wg.Wait()
	finish("l09_cgo_errno")
}
