// L03: 长循环内每 N 轮调用一次 noinline GC 函数，循环其余部分是持指针的无调用块。
package main

import (
	"fmt"
	"os"
	"runtime"
	"sync/atomic"
)

var (
	failed   int32
	tickSink []byte
	tickN    int64
)

const (
	slots     = 128
	iters     = 4096
	tickEvery = 64
)

type node struct {
	v   int64
	tag uint64
}

func ck(ok bool, msg string) {
	if !ok {
		atomic.AddInt32(&failed, 1)
		fmt.Println("FAIL:", msg)
	}
}

func finish(name string) {
	if n := atomic.LoadInt32(&failed); n != 0 {
		fmt.Printf("FAILED %s (%d failed checks)\n", name, n)
		os.Exit(1)
	}
	fmt.Println("ALL PASS " + name)
}

//go:noinline
func gcTick(round int) {
	b := make([]byte, 4096)
	b[0] = byte(round)
	b[len(b)-1] = byte(round * 7)
	tickSink = b
	runtime.GC()
	atomic.AddInt64(&tickN, 1)
}

func main() {
	backing := make([]node, slots)
	for i := range backing {
		backing[i] = node{v: int64(i)*3 + 5, tag: uint64(i)<<32 | 7}
	}
	p := (*[slots]node)(backing)

	var sum int64
	for i := 0; i < iters; i++ {
		idx := i & (slots - 1)
		sum += p[idx].v + int64(idx)
		if i%tickEvery == 0 {
			gcTick(i / tickEvery)
		}
	}

	var expected int64
	for i := 0; i < iters; i++ {
		idx := i & (slots - 1)
		expected += int64(idx)*3 + 5 + int64(idx)
	}
	ck(sum == expected, "checksum after periodic noinline GC")
	ck(atomic.LoadInt64(&tickN) == int64(iters/tickEvery), "noinline GC call count")

	corrupt := false
	for i := range backing {
		if p[i].v != int64(i)*3+5 || p[i].tag != uint64(i)<<32|7 {
			corrupt = true
		}
	}
	ck(!corrupt, "node array intact after GC")
	runtime.KeepAlive(backing)
	runtime.KeepAlive(tickSink)
	finish("l03_tight_loop_rare_gc")
}
