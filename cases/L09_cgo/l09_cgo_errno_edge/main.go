// L09: C errno 边界：strtol 上溢/下溢返回 LONG_MAX/LONG_MIN 且 errno=ERANGE、
// 成功路径不残留错误、strtod 上溢返回 HUGE_VAL+ERANGE，以及 C pthread
// 之间 errno 的线程局部隔离。C 不保留 Go 指针（只同步写栈上出参）。
package main

/*
#cgo LDFLAGS: -pthread
#define _GNU_SOURCE
#include <errno.h>
#include <limits.h>
#include <pthread.h>
#include <stdint.h>
#include <stdlib.h>

static int64_t c_long_max(void) { return (int64_t)LONG_MAX; }
static int64_t c_long_min(void) { return (int64_t)LONG_MIN; }

static int64_t c_strtol_overflow(int32_t *err, int32_t *endoff) {
	errno = 0;
	const char *s = "999999999999999999999999";
	char *end = NULL;
	long v = strtol(s, &end, 10);
	*err = (int32_t)errno;
	*endoff = (int32_t)(end - s);
	return (int64_t)v;
}

static int64_t c_strtol_underflow(int32_t *err, int32_t *endoff) {
	errno = 0;
	const char *s = "-999999999999999999999999";
	char *end = NULL;
	long v = strtol(s, &end, 10);
	*err = (int32_t)errno;
	*endoff = (int32_t)(end - s);
	return (int64_t)v;
}

static int64_t c_strtol_ok(int32_t *err, int32_t *endoff) {
	errno = 0;
	const char *s = "42xyz";
	char *end = NULL;
	long v = strtol(s, &end, 10);
	*err = (int32_t)errno;
	*endoff = (int32_t)(end - s);
	return (int64_t)v;
}

static double c_strtod_overflow(int32_t *err, int32_t *endoff) {
	errno = 0;
	const char *s = "1e99999";
	char *end = NULL;
	double v = strtod(s, &end);
	*err = (int32_t)errno;
	*endoff = (int32_t)(end - s);
	return v;
}

typedef struct {
	int32_t code;
	int32_t seen;
	pthread_barrier_t *bar;
} tls_arg_t;

static void *c_tls_worker(void *p) {
	tls_arg_t *a = (tls_arg_t *)p;
	errno = a->code;
	pthread_barrier_wait(a->bar);
	a->seen = errno;
	return NULL;
}

static int32_t c_errno_tls(int32_t code_a, int32_t code_b, int32_t *seen_a, int32_t *seen_b) {
	pthread_barrier_t bar;
	if (pthread_barrier_init(&bar, NULL, 2) != 0) {
		return -1;
	}
	tls_arg_t a = {code_a, 0, &bar};
	tls_arg_t b = {code_b, 0, &bar};
	pthread_t ta, tb;
	if (pthread_create(&ta, NULL, c_tls_worker, &a) != 0) {
		pthread_barrier_destroy(&bar);
		return -2;
	}
	if (pthread_create(&tb, NULL, c_tls_worker, &b) != 0) {
		pthread_join(ta, NULL);
		pthread_barrier_destroy(&bar);
		return -3;
	}
	pthread_join(ta, NULL);
	pthread_join(tb, NULL);
	*seen_a = a.seen;
	*seen_b = b.seen;
	pthread_barrier_destroy(&bar);
	return 0;
}
*/
import "C"

import (
	"fmt"
	"math"
	"os"
	"runtime"
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
	ck(int64(C.c_long_max()) == math.MaxInt64, "C long is 64-bit on this target")

	var errCode, endOff C.int32_t
	const overflowStr = "999999999999999999999999"
	v := int64(C.c_strtol_overflow(&errCode, &endOff))
	ck(v == int64(C.c_long_max()), "strtol overflow returns LONG_MAX")
	ck(int32(errCode) == int32(C.ERANGE), "strtol overflow errno ERANGE")
	ck(int(endOff) == len(overflowStr), "strtol overflow consumed all input")

	const underflowStr = "-999999999999999999999999"
	v = int64(C.c_strtol_underflow(&errCode, &endOff))
	ck(v == int64(C.c_long_min()), "strtol underflow returns LONG_MIN")
	ck(int32(errCode) == int32(C.ERANGE), "strtol underflow errno ERANGE")
	ck(int(endOff) == len(underflowStr), "strtol underflow consumed all input")

	const okStr = "42xyz"
	v = int64(C.c_strtol_ok(&errCode, &endOff))
	ck(v == 42, "strtol success value")
	ck(int32(errCode) == 0, "strtol success leaves errno clear")
	ck(int32(endOff) == 2, "strtol success end offset")

	const dblStr = "1e99999"
	dv := float64(C.c_strtod_overflow(&errCode, &endOff))
	ck(math.IsInf(dv, 1), "strtod overflow returns +Inf")
	ck(int32(errCode) == int32(C.ERANGE), "strtod overflow errno ERANGE")
	ck(int(endOff) == len(dblStr), "strtod overflow consumed all input")

	// errno 是线程局部量：两个 C pthread 在 barrier 前各写各的，
	// barrier 后各自读到的必须是自己的值，而不是对方的。
	var seenA, seenB C.int32_t
	rc := int32(C.c_errno_tls(C.EACCES, C.ENOENT, &seenA, &seenB))
	ck(rc == 0, "pthread TLS errno setup")
	ck(int32(seenA) == int32(C.EACCES), "pthread A sees EACCES")
	ck(int32(seenB) == int32(C.ENOENT), "pthread B sees ENOENT")
	ck(int32(seenA) != int32(seenB), "errno isolation between C threads")

	// errno 数值可映射回 Go syscall.Errno。
	ck(syscall.Errno(errCode) == syscall.ERANGE, "ERANGE maps to syscall.Errno")
	ck(syscall.Errno(errCode).Error() != "", "syscall.Errno has message")

	gcStress()
	var e2, off2 C.int32_t
	ck(int64(C.c_strtol_overflow(&e2, &off2)) == int64(C.c_long_max()), "strtol overflow after GC")
	ck(int32(e2) == int32(C.ERANGE), "ERANGE after GC")
	var sa, sb C.int32_t
	ck(int32(C.c_errno_tls(C.EINVAL, C.EPERM, &sa, &sb)) == 0, "pthread TLS after GC")
	ck(int32(sa) == int32(C.EINVAL) && int32(sb) == int32(C.EPERM), "thread-local errno after GC")

	finish("l09_cgo_errno_edge")
}
