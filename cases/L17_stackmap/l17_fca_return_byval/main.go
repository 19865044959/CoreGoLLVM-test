// L17: 聚合返回值/参数（FCA）中的指针跨 GC。
package main

import (
	"fmt"
	"os"
	"runtime"
	"sync/atomic"
)

type node struct{ v int64 }

type pair struct{ a, b *node }

type nested struct {
	A [2]*node
	B [2][2]*node
	C pair
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
func retArr2() [2]*node { return [2]*node{{v: 1}, {v: 2}} }

//go:noinline
func retM2() [2][2]*node {
	return [2][2]*node{{{v: 11}, {v: 12}}, {{v: 21}, {v: 22}}}
}

//go:noinline
func retPair() pair { return pair{a: &node{v: 3}, b: &node{v: 4}} }

//go:noinline
func retNested() nested {
	return nested{
		A: [2]*node{{v: 5}, {v: 6}},
		B: [2][2]*node{{{v: 7}, {v: 8}}, {{v: 9}, {v: 10}}},
		C: pair{a: &node{v: 13}, b: &node{v: 14}},
	}
}

//go:noinline
func takeArr2(a [2]*node) int64 { gcStress(); return a[0].v + a[1].v }

//go:noinline
func takeNested(n nested) int64 {
	gcStress()
	var s int64
	for _, p := range n.A {
		s += p.v
	}
	for _, r := range n.B {
		for _, p := range r {
			s += p.v
		}
	}
	s += n.C.a.v + n.C.b.v
	return s
}

func main() {
	for i := 0; i < 10; i++ {
		a := retArr2()
		gcStress()
		ck(a[0].v == 1 && a[1].v == 2, "retArr2 after GC")

		m := retM2()
		gcStress()
		ck(m[0][0].v == 11 && m[1][1].v == 22, "retM2 after GC")

		p := retPair()
		gcStress()
		ck(p.a.v == 3 && p.b.v == 4, "retPair after GC")

		n := retNested()
		gcStress()
		ck(n.A[1].v == 6 && n.B[1][0].v == 9 && n.C.b.v == 14, "retNested after GC")

		ck(takeArr2([2]*node{{v: 50}, {v: 51}}) == 101, "byval array arg")
		ck(takeNested(n) == 72, "byval nested arg across GC")
	}
	finish("l17_fca_return_byval")
}
