// P1-14 (报告风险 A10): 栈增长/morestack/深递归/大栈帧中的指针。
package main

import (
	"fmt"
	"os"
	"runtime"
	"sync/atomic"
)

type node struct{ v int64 }

var (
	failed int32
	gcSink [][]byte
	rrec   func(int, *node) int64
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
func rec(depth int, p *node) int64 {
	local := &node{v: int64(depth)}
	buf := [4]*node{p, local, p, local}
	if depth == 0 {
		gcStress()
		return int64(len(buf)) + local.v + p.v
	}
	r := rec(depth-1, local)
	if depth%997 == 0 {
		gcStress()
	}
	return r + local.v
}

//go:noinline
func gcHere() { gcStress() }

//go:nosplit
func nosplitHold(p *node) int64 {
	local := &node{v: p.v + 1}
	sl := []*node{local, p}
	gcHere()
	var s int64
	for _, q := range sl {
		s += q.v
	}
	return s
}

//go:noinline
func bigFrame(seed *node) int64 {
	var buf [1024]*node
	for i := range buf {
		buf[i] = &node{v: int64(i) + seed.v}
	}
	gcStress()
	return buf[1023].v
}

//go:noinline
func closureRec() int64 {
	rrec = func(d int, p *node) int64 {
		local := &node{v: int64(d)}
		if d == 0 {
			gcStress()
			return p.v + local.v
		}
		r := rrec(d-1, local)
		return r + local.v
	}
	return rrec(400, &node{v: 1})
}

func main() {
	var exp int64 = 5
	for d := 1; d <= 3000; d++ {
		exp += int64(d)
	}
	ck(rec(3000, &node{v: 100000}) == exp, "deep recursion with pointers")

	for i := 0; i < 20; i++ {
		ck(nosplitHold(&node{v: 1}) == 3, "nosplit frame across GC")
	}

	ck(bigFrame(&node{v: 500}) == 1523, "big stack frame with GC")

	expC := int64(1)
	for d := 1; d <= 400; d++ {
		expC += int64(d)
	}
	ck(closureRec() == expC, "recursive closure with GC")
	finish("p1_14_stack_growth")
}
