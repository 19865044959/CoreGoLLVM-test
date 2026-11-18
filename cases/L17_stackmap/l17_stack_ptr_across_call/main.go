// L17: 局部指针/字符串/切片/map/chan/interface 跨 noinline 调用 + GC 后仍有效。
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
func midCall(p *node, str string, sl []*node, m map[string]*node, ch chan *node, ifv interface{}) int64 {
	gcStress()
	if p.next == nil {
		return -1
	}
	sum := p.v + p.next.v + int64(len(str))
	sum += sl[0].v + sl[1].v + sl[2].v
	sum += m["k"].v
	sum += (<-ch).v
	sum += ifv.(*node).v
	return sum
}

func main() {
	for i := 0; i < 50; i++ {
		a := &node{v: 1, name: "a", next: &node{v: 2, name: "b"}}
		s := "hello-world" // len 11
		sl := []*node{{v: 10}, {v: 20}, {v: 32}}
		m := map[string]*node{"k": {v: 11}}
		ch := make(chan *node, 1)
		ch <- &node{v: 44}
		var ifv interface{} = &node{v: 55}

		got := midCall(a, s, sl, m, ch, ifv)
		ck(got == 186, "midCall sum")
		gcStress()
		ck(a.next != nil && a.next.v == 2 && a.name == "a", "linked pointers after GC")
		ck(s == "hello-world", "string after GC")
		ck(sl[2].v == 32 && m["k"].v == 11, "slice/map after GC")
		ck(ifv.(*node).v == 55, "interface after GC")
	}
	finish("l17_stack_ptr_across_call")
}
