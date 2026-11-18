// L17: 内联边界处的指针（内联 vs noinline 对照、内联聚合参数、内联分配）。
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

// 小函数，期望被内联。
func inlineTouch(p *node) int64 { return p.v + 1 }

//go:noinline
func noinlineTouch(p *node) int64 { return p.v + 1 }

//go:noinline
func gcOnly() { gcStress() }

func inlineSum2(a [2]*node) int64 { return a[0].v + a[1].v }

func inlineMake(tag string) *node { return &node{v: int64(len(tag)), name: tag} }

func idPtr(p *node) *node { return p }

func main() {
	for k := 0; k < 100; k++ {
		p := &node{v: 5, name: "inline"}
		x := inlineTouch(p)
		gcOnly()
		ck(x == 6 && p.v == 5 && p.name == "inline", "inlined leaf across GC")

		y := noinlineTouch(p)
		gcOnly()
		ck(y == 6, "noinline leaf across GC")

		q := &node{v: 7}
		r := &node{v: 8}
		ck(inlineSum2([2]*node{q, r}) == 15, "inlined aggregate arg")
		gcOnly()
		ck(q.v == 7 && r.v == 8, "aggregate pointers after GC")

		m := inlineMake("four")
		gcOnly()
		ck(m.v == 4 && m.name == "four", "inlined allocation result")

		s := idPtr(idPtr(p))
		gcOnly()
		ck(s.v == 5, "chained pointer passthrough")
	}
	finish("l17_inline_boundary")
}
