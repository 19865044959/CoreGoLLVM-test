// P0-06 (报告风险 A4/A8/A9): 大结构 byval + 大返回值 + 超出寄存器的参数。
package main

import (
	"fmt"
	"os"
	"runtime"
	"sync/atomic"
)

type node struct{ v int64 }

type big struct {
	p   [10]*node
	s   string
	sl  []*node
	m   map[string]*node
	i   interface{}
	pad [8]int64
}

type bigOut struct {
	a  [8]*node
	b  [8]int64
	s  string
	sl []*node
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
func consumeBig(b big, x1, x2, x3, x4, x5, x6, x7, x8, x9 int64,
	p1, p2, p3, p4, p5, p6, p7, p8 *node) int64 {
	gcStress()
	var sum int64
	for _, q := range b.p {
		sum += q.v
	}
	sum += int64(len(b.s)) + b.sl[3].v + b.m["k"].v + b.i.(*node).v
	sum += x1 + x9 + p1.v + p8.v
	return sum
}

//go:noinline
func makeBigOut() bigOut {
	o := bigOut{s: "goret", sl: make([]*node, 4)}
	for i := range o.a {
		o.a[i] = &node{v: int64(1000 + i)}
	}
	for i := range o.b {
		o.b[i] = int64(i)
	}
	for i := range o.sl {
		o.sl[i] = &node{v: int64(2000 + i)}
	}
	gcStress()
	return o
}

//go:noinline
func consumeBigOut(o bigOut) int64 {
	gcStress()
	return o.a[7].v + o.sl[3].v + o.b[7] + int64(len(o.s))
}

func main() {
	for round := 0; round < 5; round++ {
		b := big{
			s:  "five!",
			sl: []*node{{v: 190}, {v: 191}, {v: 192}, {v: 200}},
			m:  map[string]*node{"k": {v: 300}},
			i:  interface{}(&node{v: 400}),
		}
		for i := range b.p {
			b.p[i] = &node{v: int64(100 + i)}
		}
		ps := make([]*node, 8)
		for i := range ps {
			ps[i] = &node{v: int64(500 + i)}
		}
		got := consumeBig(b, 1, 2, 3, 4, 5, 6, 7, 8, 9,
			ps[0], ps[1], ps[2], ps[3], ps[4], ps[5], ps[6], ps[7])
		ck(got == 2967, "consumeBig sum")

		gcStress()
		ck(b.p[9].v == 109 && b.sl[3].v == 200 && b.m["k"].v == 300, "caller big after GC")

		o := makeBigOut()
		gcStress()
		ck(o.a[0].v == 1000 && o.a[7].v == 1007, "bigOut array")
		ck(o.sl[3].v == 2003 && o.b[7] == 7 && o.s == "goret", "bigOut fields")
		ck(consumeBigOut(o) == 3022, "bigOut byval across GC")
	}
	finish("p0_06_byval_largeframe")
}
