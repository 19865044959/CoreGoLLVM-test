// L09: C malloc/free/realloc 循环：200 轮分配-校验-释放，穿插 realloc、
// 长期存活块与显式 GC；C 侧统计 alloc/free/live 字节，最终必须回到 0。
// 所有块都是 C 内存，Go 通过 unsafe.Slice 读写，C 不保留 Go 指针。
package main

/*
#include <stdint.h>
#include <stdlib.h>

static int64_t g_allocs = 0;
static int64_t g_frees = 0;
static int64_t g_live_bytes = 0;

static void *c_alloc(size_t n) {
	void *p = malloc(n);
	if (p != NULL) {
		g_allocs++;
		g_live_bytes += (int64_t)n;
	}
	return p;
}

static void c_free(void *p, size_t n) {
	if (p != NULL) {
		free(p);
		g_frees++;
		g_live_bytes -= (int64_t)n;
	}
}

static void *c_realloc(void *p, size_t oldn, size_t newn) {
	void *q = realloc(p, newn);
	if (q != NULL) {
		if (p == NULL) {
			g_allocs++;
			g_live_bytes += (int64_t)newn;
		} else {
			g_live_bytes += (int64_t)newn - (int64_t)oldn;
		}
	}
	return q;
}

static void c_stats(int64_t *allocs, int64_t *frees, int64_t *live) {
	*allocs = g_allocs;
	*frees = g_frees;
	*live = g_live_bytes;
}
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

type block struct {
	p    unsafe.Pointer
	size int
	seed byte
}

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

func fill(p unsafe.Pointer, n int, seed byte) {
	b := unsafe.Slice((*byte)(p), n)
	for i := range b {
		b[i] = seed + byte(i*7)
	}
}

func verify(p unsafe.Pointer, n int, seed byte, msg string) bool {
	b := unsafe.Slice((*byte)(p), n)
	for i := range b {
		if b[i] != seed+byte(i*7) {
			ck(false, fmt.Sprintf("%s byte[%d]", msg, i))
			return false
		}
	}
	return true
}

func verifyPrefix(p unsafe.Pointer, n int, seed byte, msg string) bool {
	b := unsafe.Slice((*byte)(p), n)
	for i := range b {
		if b[i] != seed+byte(i*7) {
			ck(false, fmt.Sprintf("%s prefix[%d]", msg, i))
			return false
		}
	}
	return true
}

func alloc(n int, seed byte) unsafe.Pointer {
	p := C.c_alloc(C.size_t(n))
	if p == nil {
		ck(false, fmt.Sprintf("malloc(%d)", n))
		return nil
	}
	fill(p, n, seed)
	return p
}

func main() {
	var allocs, frees, live C.int64_t
	C.c_stats(&allocs, &frees, &live)
	ck(int64(allocs) == 0 && int64(frees) == 0 && int64(live) == 0, "C allocator stats start at zero")

	// 长期存活块：分配后跨所有轮次与 GC，最后统一释放。
	longSpecs := []struct {
		size int
		seed byte
	}{{33, 0x21}, {129, 0x43}, {1025, 0x65}, {8193, 0x87}}
	longs := make([]block, len(longSpecs))
	var longBytes int64
	for i, sp := range longSpecs {
		longs[i] = block{p: alloc(sp.size, sp.seed), size: sp.size, seed: sp.seed}
		longBytes += int64(sp.size)
	}

	const rounds = 200
	for round := 0; round < rounds; round++ {
		sizes := []int{16, 257, 4096}
		ps := make([]unsafe.Pointer, len(sizes))
		seeds := make([]byte, len(sizes))
		actual := make([]int, len(sizes))
		for j, n := range sizes {
			seeds[j] = byte(round*31 + j*7 + 1)
			actual[j] = n
			ps[j] = alloc(n, seeds[j])
		}

		// 每 7 轮把第二块 realloc 放大，前缀必须保留。
		if round%7 == 0 && ps[1] != nil {
			np := C.c_realloc(ps[1], C.size_t(257), C.size_t(4096))
			if np == nil {
				ck(false, fmt.Sprintf("realloc round=%d", round))
			} else {
				ps[1] = np
				actual[1] = 4096
				verifyPrefix(np, 257, seeds[1], fmt.Sprintf("realloc round=%d", round))
				b := unsafe.Slice((*byte)(np), 4096)
				for k := 257; k < 4096; k++ {
					b[k] = seeds[1] + byte(k*7)
				}
				verify(np, 4096, seeds[1], fmt.Sprintf("realloc tail round=%d", round))
			}
		}

		for j, p := range ps {
			if p != nil {
				verify(p, actual[j], seeds[j], fmt.Sprintf("transient round=%d j=%d", round, j))
				C.c_free(p, C.size_t(actual[j]))
			}
		}

		// 每轮释放完瞬时块后，live 字节应只剩长期块。
		C.c_stats(&allocs, &frees, &live)
		ck(int64(live) == longBytes, fmt.Sprintf("live bytes round=%d", round))
		ck(int64(allocs)-int64(frees) == int64(len(longs)), fmt.Sprintf("alloc/free balance round=%d", round))

		if round%10 == 9 {
			gcStress()
			for i := range longs {
				if longs[i].p != nil {
					verify(longs[i].p, longs[i].size, longs[i].seed,
						fmt.Sprintf("long block %d after GC round=%d", i, round))
				}
			}
			// 在 GC 后重新读取分配器统计，确认 C 内存没有被 Go GC 影响。
			C.c_stats(&allocs, &frees, &live)
			ck(int64(live) == longBytes, fmt.Sprintf("live bytes after GC round=%d", round))
		}
		runtime.KeepAlive(longs)
	}

	// free(NULL) 不改变统计。
	C.c_stats(&allocs, &frees, &live)
	a0, f0, l0 := int64(allocs), int64(frees), int64(live)
	C.c_free(nil, 0)
	C.c_stats(&allocs, &frees, &live)
	ck(int64(allocs) == a0 && int64(frees) == f0 && int64(live) == l0, "free(NULL) is a no-op")

	// 释放长期块，统计回到 0。
	for i := range longs {
		if longs[i].p != nil {
			C.c_free(longs[i].p, C.size_t(longs[i].size))
			longs[i].p = nil
		}
	}
	C.c_stats(&allocs, &frees, &live)
	ck(int64(live) == 0, "live bytes zero after freeing all")
	ck(int64(allocs) == int64(frees), "alloc count equals free count")

	gcStress()
	C.c_stats(&allocs, &frees, &live)
	ck(int64(allocs) == int64(frees) && int64(live) == 0, "allocator stats stable after GC")
	finish("l09_cgo_malloc_free_churn")
}
