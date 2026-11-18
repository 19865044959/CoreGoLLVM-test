// L09: C 结构体数组：C.calloc 分配 item_t 数组并在 C 侧填充，
// Go 用 unsafe.Slice 直接遍历/改字段，C 侧求和立即看到修改；
// 数组跨多轮 GC 保持不变，最后显式释放。
package main

import (
	"fmt"
	"os"
	"runtime"
	"sync/atomic"
	"unsafe"
)

/*
#include <stddef.h>
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>

typedef struct {
	int32_t id;
	int32_t pad;
	int64_t score;
	char name[16];
} item_t;

static item_t *c_make_items(int32_t n) {
	item_t *p = (item_t *)calloc((size_t)n, sizeof(item_t));
	if (!p) return NULL;
	for (int32_t i = 0; i < n; i++) {
		p[i].id = i;
		p[i].pad = 7;
		p[i].score = (int64_t)i * 1000 - 500;
		snprintf(p[i].name, sizeof p[i].name, "item-%d", (int)i);
	}
	return p;
}

static void c_free_items(item_t *p) { free(p); }

static int64_t c_items_score(const item_t *p, int32_t n) {
	int64_t s = 0;
	for (int32_t i = 0; i < n; i++) s += p[i].score;
	return s;
}

static int64_t c_items_ids(const item_t *p, int32_t n) {
	int64_t s = 0;
	for (int32_t i = 0; i < n; i++) s += p[i].id + p[i].pad;
	return s;
}

static size_t c_item_size(void) { return sizeof(item_t); }
static size_t c_item_off_name(void) { return offsetof(item_t, name); }
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
	ck(uint64(unsafe.Sizeof(C.item_t{})) == uint64(C.c_item_size()), "sizeof item_t")
	ck(uint64(unsafe.Offsetof(C.item_t{}.name)) == uint64(C.c_item_off_name()), "offsetof name")

	const n = 64
	p := C.c_make_items(C.int32_t(n))
	ck(p != nil, "C array allocated")
	items := unsafe.Slice(p, n)

	var wantScore int64
	var wantIDs int64
	for i := 0; i < n; i++ {
		ck(int32(items[i].id) == int32(i), fmt.Sprintf("id i=%d", i))
		ck(int32(items[i].pad) == 7, fmt.Sprintf("pad i=%d", i))
		ck(int64(items[i].score) == int64(i)*1000-500, fmt.Sprintf("score i=%d", i))
		ck(C.GoString(&items[i].name[0]) == fmt.Sprintf("item-%d", i), fmt.Sprintf("name i=%d", i))
		wantScore += int64(i)*1000 - 500
		wantIDs += int64(i) + 7
	}
	ck(int64(C.c_items_score(p, C.int32_t(n))) == wantScore, "C score sum")
	ck(int64(C.c_items_ids(p, C.int32_t(n))) == wantIDs, "C id+pad sum")

	// Go 直接改 C 数组字段，C 求和立即可见。
	items[3].score = -12345
	items[17].id = 999
	items[17].pad = 1
	ck(int64(C.c_items_score(p, C.int32_t(n))) == wantScore-1000*3+500-12345,
		"C sees Go field write (score)")
	ck(int64(C.c_items_ids(p, C.int32_t(n))) == wantIDs-17-7+999+1,
		"C sees Go field write (id/pad)")

	// 跨多轮 GC，C 堆数组内容不变。
	for round := 0; round < 64; round++ {
		gcStress()
		if round%8 == 0 {
			ck(int64(C.c_items_score(p, C.int32_t(n))) == wantScore-1000*3+500-12345,
				fmt.Sprintf("score after GC round=%d", round))
			ck(C.GoString(&items[63].name[0]) == "item-63", fmt.Sprintf("name after GC round=%d", round))
		}
	}
	ck(int32(items[0].pad) == 7 && int32(items[17].id) == 999, "fields after GC rounds")
	ck(uint64(unsafe.Sizeof(*p)) == uint64(C.c_item_size()), "element size via pointer")

	C.c_free_items(p)
	finish("l09_cgo_struct_array")
}
