// L17: 方法值/方法表达式/嵌入 wrapper/尾位置调用中的指针跨 GC。
package main

import (
	"fmt"
	"os"
	"runtime"
	"sync/atomic"
)

type node struct {
	v    int64
	next *node
}

type area interface{ Area() int64 }

type base struct {
	w, h int64
	tag  *node
}

func (b *base) Area() int64 { gcStress(); return b.w*b.h + b.tag.v }

type l1 struct{ *base }
type l2 struct{ *l1 }
type l3 struct{ *l2 }

type valrec struct {
	w   int64
	tag *node
}

func (v valrec) Area() int64 { gcStress(); return v.w + v.tag.v }

var (
	failed int32
	gcSink [][]byte
	gfn    func() int64
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
func consumeNode(p *node) int64 { gcStress(); return p.v + p.next.v }

//go:noinline
func tailCall(p *node) int64 { return consumeNode(p) }

//go:noinline
func ifaceTailDeep(p *node) int64 {
	var a area = &l3{&l2{&l1{base: &base{w: 3, h: 4, tag: p}}}}
	return a.Area()
}

func main() {
	for round := 0; round < 10; round++ {
		b := &base{w: 3, h: 4, tag: &node{v: 7}}

		f := b.Area
		gcStress()
		ck(f() == 19, "method value")

		expr := (*base).Area
		gcStress()
		ck(expr(b) == 19, "method expression")

		var a area = &l3{&l2{&l1{base: b}}}
		gcStress()
		ck(a.Area() == 19, "embedded wrapper chain")

		var a2 area = valrec{w: 10, tag: &node{v: 5}}
		gcStress()
		ck(a2.Area() == 15, "value receiver boxed in interface")

		gfn = b.Area
		gcStress()
		ck(gfn() == 19, "method value in global func var")

		p := &node{v: 30, next: &node{v: 12}}
		ck(tailCall(p) == 42, "tail-position call with pointers")

		ck(ifaceTailDeep(&node{v: 7}) == 19, "deep wrapper tail call")
	}
	finish("l17_method_wrapper_tail")
}
