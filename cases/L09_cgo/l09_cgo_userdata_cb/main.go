// L09: C 回调带 userdata：C 分配上下文（int64 三元组）并把同一指针
// 传入每次 //export 回调；回调累加 calls/sum、按上下文 mul 返回结果，
// 偶尔分配垃圾并 GC。两个上下文交替调用，验证互不串扰且 C 不保留指针。
package main

import (
	"fmt"
	"os"
	"runtime"
	"sync/atomic"
	"unsafe"
)

/*
#include <stdint.h>

typedef struct ud_s ud_t;

ud_t *c_ud_new(int64_t mul);
void c_ud_free(ud_t *p);
int64_t c_apply_ud(ud_t *ud, int64_t x);
int64_t c_apply_ud_n(ud_t *ud, int64_t x, int32_t n);
int64_t c_ud_calls(const ud_t *p);
int64_t c_ud_sum(const ud_t *p);
int64_t c_ud_mul(const ud_t *p);
int64_t c_ud_size(void);
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

// udLayout 必须与 userdata.c 中 struct ud_s 的前三个 int64 字段一致。
type udLayout struct {
	calls int64
	sum   int64
	mul   int64
}

//export goUDApply
func goUDApply(ud unsafe.Pointer, x C.int64_t) C.int64_t {
	p := (*udLayout)(ud)
	p.calls++
	p.sum += int64(x)
	if p.calls%32 == 0 {
		junk := make([]byte, 1024)
		junk[0] = byte(p.calls)
		gcSink = append(gcSink[:0], junk)
		runtime.GC()
	}
	return C.int64_t(int64(x) * p.mul)
}

func main() {
	ck(uint64(C.c_ud_size()) == uint64(unsafe.Sizeof(udLayout{})), "userdata layout size")

	u1 := C.c_ud_new(3)
	u2 := C.c_ud_new(-2)
	ck(u1 != nil && u2 != nil, "C userdata allocated")
	ck(int64(C.c_ud_mul(u1)) == 3 && int64(C.c_ud_mul(u2)) == -2, "userdata mul")

	ck(int64(C.c_apply_ud(u1, 10)) == 30, "callback result ud1")
	ck(int64(C.c_apply_ud(u2, 100)) == -200, "callback result ud2")
	ck(int64(C.c_ud_calls(u1)) == 1 && int64(C.c_ud_calls(u2)) == 1, "calls isolated")
	ck(int64(C.c_ud_sum(u1)) == 10 && int64(C.c_ud_sum(u2)) == 100, "sums isolated")

	// C 循环内多次回调，每次传入同一 userdata。
	got := int64(C.c_apply_ud_n(u1, 10, 5))
	var want int64
	for i := int64(0); i < 5; i++ {
		want += (10 + i) * 3
	}
	ck(got == want, "c_apply_ud_n result")
	ck(int64(C.c_ud_calls(u1)) == 6, "calls after C loop")
	ck(int64(C.c_ud_sum(u1)) == 10+10+11+12+13+14, "sum after C loop")

	// 两个上下文交替 200 次，回调内触发 GC。
	var want1, want2, n1, n2 int64
	for i := int64(0); i < 200; i++ {
		ck(int64(C.c_apply_ud(u1, C.int64_t(i))) == i*3, fmt.Sprintf("ud1 i=%d", i))
		ck(int64(C.c_apply_ud(u2, C.int64_t(-i))) == (-i)*-2, fmt.Sprintf("ud2 i=%d", i))
		want1 += i
		want2 += -i
		n1++
		n2++
	}
	ck(int64(C.c_ud_calls(u1)) == 6+n1 && int64(C.c_ud_calls(u2)) == 1+n2, "interleaved calls")
	ck(int64(C.c_ud_sum(u1)) == 70+want1, "interleaved sum ud1")
	ck(int64(C.c_ud_sum(u2)) == 100+want2, "interleaved sum ud2")

	gcStress()
	ck(int64(C.c_apply_ud(u1, 7)) == 21, "ud1 after GC")
	ck(int64(C.c_apply_ud(u2, 7)) == -14, "ud2 after GC")
	ck(int64(C.c_ud_calls(u1)) == 6+n1+1 && int64(C.c_ud_calls(u2)) == 1+n2+1,
		"calls after GC")

	C.c_ud_free(u1)
	C.c_ud_free(u2)
	finish("l09_cgo_userdata_cb")
}
