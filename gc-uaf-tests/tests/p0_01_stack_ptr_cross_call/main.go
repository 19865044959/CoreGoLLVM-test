// P0-01 (报告风险 A1/A2): 栈上指针/字符串/切片/map/chan 跨 GC 调用后仍有效。
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
	next *node
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
func midCall(p *node, s string, sl []*node, m map[string]*node, ch chan *node) int64 {
	gcStress()
	if p.next == nil {
		return -1
	}
	sum := p.v + p.next.v + int64(len(s))
	sum += sl[0].v + sl[1].v + sl[2].v
	sum += m["k"].v
	sum += (<-ch).v
	return sum
}

//go:noinline
func loneAlloc() int64 {
	p := &node{v: 55, name: "lone"}
	runtime.GC()
	return p.v + int64(len(p.name))
}

func main() {
	for round := 0; round < 20; round++ {
		a := &node{v: 1, name: "a", next: &node{v: 2, name: "b"}}
		s := "hello-world" // len 11
		sl := []*node{{v: 10}, {v: 20}, {v: 32}}
		m := map[string]*node{"k": {v: 11}}
		ch := make(chan *node, 1)
		ch <- &node{v: 44}

		got := midCall(a, s, sl, m, ch)
		// 1 + 2 + 11 + 10 + 20 + 32 + 11 + 44
		ck(got == 131, "midCall sum")

		gcStress()
		ck(a.next != nil && a.next.v == 2, "a.next after GC")
		ck(s == "hello-world", "string after GC")
		ck(len(sl) == 3 && sl[2].v == 32, "slice after GC")
		ck(m["k"].v == 11, "map value after GC")

		ck(loneAlloc() == 59, "lone pointer across GC")
	}
	finish("p0_01_stack_ptr_cross_call")
}
