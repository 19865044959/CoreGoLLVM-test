// L05: append 扩容路径上的指针元素：扩容、GC、子切片、copy 后全部必须有效。
package main

import (
	"fmt"
	"os"
	"runtime"
	"sync/atomic"
)

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

//go:noinline
func build(n int) []*node {
	s := make([]*node, 0, 4)
	for i := 0; i < n; i++ {
		s = append(s, &node{v: int64(i), name: "app"})
	}
	gcStress()
	return s
}

func main() {
	s := build(2000)
	gcStress()
	var sum int64
	for i, p := range s {
		if p == nil || p.v != int64(i) || p.name != "app" {
			ck(false, "appended pointer corrupted")
			break
		}
		sum += p.v
	}
	ck(sum == int64(1999*2000/2), "sum after growth")

	for i := 0; i < 200; i++ {
		s = append(s, &node{v: int64(10000 + i), name: "app2"})
	}
	gcStress()
	ck(len(s) == 2200 && s[2000].v == 10000 && s[2199].v == 10199, "append after GC")

	sub := s[500:1500]
	gcStress()
	ck(sub[0].v == 500 && sub[999].v == 1499, "sub-slice after GC")

	dst := make([]*node, len(sub))
	copy(dst, sub)
	gcStress()
	ck(dst[123].v == 623 && len(dst) == 1000, "copy after GC")

	runtime.KeepAlive(s)
	finish("l05_append_growth_gc")
}
