// L09: cgo 调用纯 C 函数（整数运算），GC 前后结果一致；Go 指针在 cgo 调用点存活。
package main

import (
	"fmt"
	"os"
	"runtime"
	"sync/atomic"
)

/*
#include <stdint.h>

static int64_t c_mix(int64_t a, int64_t b) {
	return (a * 31 + b) ^ (a << 3);
}
*/
import "C"

type node struct {
	v    int64
	name string
}

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
	for i := int64(0); i < 300; i++ {
		want := (i*31 + 2) ^ (i << 3)
		got := int64(C.c_mix(C.int64_t(i), C.int64_t(2)))
		ck(got == want, "cgo pure call result")
	}

	p := &node{v: 99, name: "cgo"}
	got := int64(C.c_mix(C.int64_t(p.v), C.int64_t(1)))
	gcStress()
	ck(got == (99*31+1)^(99<<3), "cgo result computed before GC")
	ck(p.v == 99 && p.name == "cgo", "Go object across cgo call and GC")

	// cgo 调用位于循环中，循环内持指针。
	var sum, wantSum int64
	for i := 0; i < 50; i++ {
		wantSum += (99*31 + int64(i)) ^ (99 << 3)
		sum += int64(C.c_mix(C.int64_t(p.v), C.int64_t(i)))
	}
	ck(sum == wantSum, "cgo loop checksum")
	gcStress()
	ck(p.v == 99, "pointer after cgo loop + GC")
	finish("l09_cgo_pure_calls")
}
