// L09: C 返回 const char*：静态版本串/空串/查找表/300 字节长串，
// Go 用 C.GoString 复制后跨 GC 读取；另验证 malloc 副本在 free 后
// Go 字符串仍完整（GoString 是拷贝语义）。
package main

import (
	"fmt"
	"os"
	"runtime"
	"strings"
	"sync/atomic"
	"unsafe"
)

/*
#include <stddef.h>
#include <stdint.h>
#include <stdlib.h>
#include <string.h>

static const char *c_version(void) { return "cgo-const-v1.24"; }
static const char *c_empty(void) { return ""; }

static const char *c_slot(int32_t i) {
	static const char *tab[3] = {"zero", "one", "two"};
	if (i < 0 || i > 2) return NULL;
	return tab[i];
}

static char c_long[301];
static int32_t c_long_ready = 0;
static const char *c_long_str(void) {
	if (!c_long_ready) {
		char *p = (char *)c_long;
		for (int i = 0; i < 300; i++) p[i] = (char)('a' + (i % 26));
		p[300] = 0;
		c_long_ready = 1;
	}
	return c_long;
}

static char *c_dup(const char *s) {
	size_t n = strlen(s);
	char *p = (char *)malloc(n + 1);
	if (p) memcpy(p, s, n + 1);
	return p;
}

static void c_free_str(char *p) { free(p); }
*/
import "C"

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
	const version = "cgo-const-v1.24"
	ck(C.GoString(C.c_version()) == version, "static version string")
	ck(C.GoString(C.c_empty()) == "", "static empty string")
	for i, want := range []string{"zero", "one", "two"} {
		ck(C.GoString(C.c_slot(C.int32_t(i))) == want, fmt.Sprintf("static table slot=%d", i))
	}

	// 静态指针每次返回同一地址（不在输出中打印地址）。
	p1 := C.c_version()
	p2 := C.c_version()
	ck(uintptrOf(p1) == uintptrOf(p2), "static const char* pointer stable")

	// 300 字节静态串：GoString 复制并按字节比较。
	var sb strings.Builder
	for i := 0; i < 300; i++ {
		sb.WriteByte(byte('a' + i%26))
	}
	wantLong := sb.String()
	long1 := C.GoString(C.c_long_str())
	ck(long1 == wantLong, "300-byte C string")
	ck(len(long1) == 300, "300-byte length")

	// malloc 副本：GoString 后立即 free，Go 字符串必须是独立拷贝。
	dup := C.c_dup(C.c_version())
	ck(dup != nil, "malloc duplicate")
	dupGo := C.GoString(dup)
	C.c_free_str(dup)
	ck(dupGo == version, "GoString copy before free")
	gcStress()
	ck(dupGo == version, "GoString copy survives C free + GC")

	// 长串副本 free 后跨多轮 GC 仍完整。
	dup2 := C.c_dup(C.c_long_str())
	dup2Go := C.GoString(dup2)
	C.c_free_str(dup2)
	for round := 0; round < 32; round++ {
		gcStress()
		if round%8 == 0 {
			ck(dup2Go == wantLong, fmt.Sprintf("long copy after GC round=%d", round))
		}
	}

	// 静态串在反复 GC 后仍可读。
	for round := 0; round < 64; round++ {
		ck(C.GoString(C.c_slot(C.int32_t(round%3))) == []string{"zero", "one", "two"}[round%3],
			fmt.Sprintf("static table after GC round=%d", round))
		gcStress()
	}
	ck(C.GoString(C.c_slot(C.int32_t(-1))) == "", "NULL const char* -> empty GoString")

	finish("l09_cgo_const_string")
}

func uintptrOf(p *C.char) uintptr {
	return uintptr(unsafe.Pointer(p))
}
