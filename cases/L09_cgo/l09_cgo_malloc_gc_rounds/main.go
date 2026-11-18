// L09: C.malloc/calloc 数组跨多轮 GC 存活：256 条记录与 8 块 4KiB
// 缓冲由 C 持有地址，Go 侧只保存 unsafe.Pointer；每轮 GC 后校验内容，最后 free。
package main

/*
#include <stdint.h>
#include <stdlib.h>

typedef struct {
	int64_t id;
	int64_t a;
	int64_t b;
} rec_t;

static rec_t *c_recs_new(int n) {
	return (rec_t *)calloc((size_t)n, sizeof(rec_t));
}

static void c_recs_init(rec_t *r, int n) {
	for (int i = 0; i < n; i++) {
		r[i].id = (int64_t)i;
		r[i].a = (int64_t)i * 3;
		r[i].b = (int64_t)i * 5;
	}
}

static int64_t c_recs_total(const rec_t *r, int n) {
	int64_t s = 0;
	for (int i = 0; i < n; i++) {
		s += r[i].id + r[i].a + r[i].b;
	}
	return s;
}

static int64_t c_rec_field(const rec_t *r, int i) {
	return r[i].id * 1000000 + r[i].a * 1000 + r[i].b;
}

static void *c_alloc(size_t n) { return malloc(n); }
static void c_free(void *p) { free(p); }
*/
import "C"

import (
	"fmt"
	"os"
	"runtime"
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

func main() {
	const n = 256
	p := C.c_recs_new(n)
	ck(p != nil, "C.calloc records")
	if p == nil {
		finish("l09_cgo_malloc_gc_rounds")
		return
	}
	C.c_recs_init(p, n)

	recs := unsafe.Slice(p, n)
	var wantTotal int64
	for i := 0; i < n; i++ {
		wantTotal += int64(i) * 9
	}
	ck(int64(C.c_recs_total(p, n)) == wantTotal, "C total after init")
	for i := 0; i < n; i += 37 {
		ck(recs[i].id == C.int64_t(i) && recs[i].a == C.int64_t(i*3) && recs[i].b == C.int64_t(i*5),
			fmt.Sprintf("record %d after init", i))
	}

	// 多个 C 缓冲的地址保存在 Go 堆中的 []unsafe.Pointer 里，跨 GC 有效。
	const nb = 8
	bufs := make([]unsafe.Pointer, nb)
	for i := range bufs {
		bufs[i] = C.c_alloc(4096)
		ck(bufs[i] != nil, fmt.Sprintf("C.alloc buf %d", i))
		*(*byte)(bufs[i]) = byte(i + 1)
	}

	for round := 0; round < 100; round++ {
		// 制造 Go 堆垃圾，诱发写屏障/清扫。
		trash := make([][]byte, 64)
		for i := range trash {
			trash[i] = make([]byte, 1024)
			trash[i][0] = byte(round + i)
		}
		gcSink = append(gcSink[:0], trash...)
		runtime.GC()
		gcSink = gcSink[:0]

		ck(int64(C.c_recs_total(p, n)) == wantTotal, fmt.Sprintf("C total after GC round=%d", round))
		ck(int64(C.c_rec_field(p, 255)) == 255*1000000+255*3000+255*5,
			fmt.Sprintf("C record after GC round=%d", round))
		ck(recs[128].id == 128, fmt.Sprintf("Go view of C record round=%d", round))
		for i := range bufs {
			ck(*(*byte)(bufs[i]) == byte(i+1), fmt.Sprintf("C buf %d after GC round=%d", i, round))
		}
		runtime.KeepAlive(bufs)
		runtime.KeepAlive(p)
	}

	for i := range bufs {
		C.c_free(bufs[i])
	}
	C.c_free(unsafe.Pointer(p))
	finish("l09_cgo_malloc_gc_rounds")
}
