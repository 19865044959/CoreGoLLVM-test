// L09: C 函数指针回调 Go（//export）：C 侧把 goCallback 地址存入
// 函数指针并在调用期内同步回调；回调里访问 Go 全局、分配垃圾并触发 GC。
// C 不保留函数指针/Go 指针到调用结束之后。
package main

/*
#include <stdint.h>
int32_t c_invoke_go(int32_t x);
int32_t c_invoke_go_n(int32_t x, int32_t n);
*/
import "C"

import (
	"fmt"
	"os"
	"runtime"
	"sync/atomic"
)

var (
	failed    int32
	gcSink    [][]byte
	cbCalls   int
	cbLast    int64
	cbSum     int64
	cbGarbage []byte
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

//export goCallback
func goCallback(x C.int32_t) C.int32_t {
	cbCalls++
	cbLast = int64(x)
	cbSum += int64(x)
	// 在 C→Go 回调里制造垃圾并显式 GC，验证回调帧/寄存器可被扫描。
	if cbCalls%16 == 0 {
		b := make([]byte, 4096)
		b[0] = byte(x)
		cbGarbage = append(cbGarbage[:0], b...)
		runtime.GC()
	}
	return x*3 + 1
}

func main() {
	for i := 0; i < 50; i++ {
		got := int32(C.c_invoke_go(C.int32_t(i)))
		ck(got == int32(i)*3+1, fmt.Sprintf("C callback result i=%d", i))
		ck(cbCalls == i+1, fmt.Sprintf("callback count i=%d", i))
		ck(cbLast == int64(i), fmt.Sprintf("callback last arg i=%d", i))
	}
	ck(cbSum == 49*50/2, "callback arg sum after 50 calls")

	// 一次 C 调用内多次回调（C 循环持有函数指针）。
	got := int32(C.c_invoke_go_n(10, 5))
	var want int32
	for i := 0; i < 5; i++ {
		want += int32(10+i)*3 + 1
	}
	ck(got == want, "C loop callback sum")
	ck(cbCalls == 55, "callback count after C loop")
	ck(cbLast == 14, "callback last arg after C loop")

	gcStress()
	ck(cbCalls == 55 && cbLast == 14 && cbSum == 49*50/2+60, "callback state after GC")

	// 再次回调，确认 GC 后 C 侧函数指针仍有效（每次调用前由 C 重新取地址）。
	for i := 0; i < 8; i++ {
		ck(int32(C.c_invoke_go(C.int32_t(100+i))) == int32(100+i)*3+1,
			fmt.Sprintf("post-GC callback i=%d", i))
	}
	finish("l09_cgo_callback")
}
