// P1-19 (报告风险 A4/A5): 多返回值各分支、可变参数。
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
func multi(sel int) (*node, int64) {
	a := &node{v: 10}
	b := &node{v: 20}
	if sel == 0 {
		gcStress()
		return a, a.v
	}
	gcStress()
	return b, b.v
}

//go:noinline
func multi3(sel int) (a, b, c *node) {
	a = &node{v: 1}
	b = &node{v: 2}
	c = &node{v: 3}
	if sel == 0 {
		gcStress()
		return
	}
	gcStress()
	a, b, c = c, b, a
	return
}

//go:noinline
func varUse(prefix *node, xs ...*node) int64 {
	gcStress()
	sum := prefix.v
	for _, p := range xs {
		sum += p.v
	}
	return sum
}

//go:noinline
func varForward(prefix *node, xs ...*node) int64 {
	gcStress()
	return varUse(prefix, xs...)
}

//go:noinline
func varIface(xs ...interface{}) int64 {
	gcStress()
	var sum int64
	for _, x := range xs {
		if p, ok := x.(*node); ok {
			sum += p.v
		}
	}
	return sum
}

func main() {
	for i := 0; i < 10; i++ {
		a, n := multi(0)
		b, m := multi(1)
		gcStress()
		ck(a.v == 10 && n == 10, "multi return path0")
		ck(b.v == 20 && m == 20, "multi return path1")

		x, y, z := multi3(0)
		u, v, w := multi3(1)
		gcStress()
		ck(x.v == 1 && y.v == 2 && z.v == 3, "multi3 path0")
		ck(u.v == 3 && v.v == 2 && w.v == 1, "multi3 path1")

		ck(varUse(&node{v: 1}, &node{v: 2}, &node{v: 3}, &node{v: 4}) == 10, "variadic")
		xs := []*node{{v: 2}, {v: 3}, {v: 4}}
		ck(varForward(&node{v: 1}, xs...) == 10, "variadic forward")
		ck(varIface(&node{v: 7}, "skip", &node{v: 8}, 5) == 15, "interface variadic")
	}
	finish("p1_19_multi_return_variadic")
}
