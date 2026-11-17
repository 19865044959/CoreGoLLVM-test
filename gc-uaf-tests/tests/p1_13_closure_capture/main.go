// P1-13 (报告风险 A11/A13): 闭包捕获指针跨 GC。
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
func makeCounter(seed *node) func() int64 {
	count := int64(0)
	str := "closure"
	sl := []*node{seed, {v: 2}}
	return func() int64 {
		gcStress()
		count++
		return seed.v + int64(len(str)) + sl[1].v + count
	}
}

//go:noinline
func makePairClosure() (func() int64, func() int64) {
	a := &node{v: 100}
	b := &node{v: 200}
	fa := func() int64 { gcStress(); return a.v }
	fb := func() int64 { gcStress(); return b.v }
	return fa, fb
}

//go:noinline
func captureAddr() func() int64 {
	x := int64(5)
	p := &x
	return func() int64 {
		gcStress()
		*p += 10
		return *p
	}
}

func main() {
	for round := 0; round < 5; round++ {
		f := makeCounter(&node{v: 1, name: "seed"})
		var sum int64
		for i := 0; i < 10; i++ {
			sum += f()
		}
		ck(sum == 155, "closure counter sum")

		fa, fb := makePairClosure()
		ck(fa() == 100 && fb() == 200, "two closures over distinct objects")

		g := captureAddr()
		ck(g() == 15 && g() == 25, "closure over escaped address")

		sl := []*node{{v: 1}, {v: 2}, {v: 3}}
		fn := func() int64 {
			gcStress()
			var s int64
			for _, p := range sl {
				s += p.v
			}
			return s
		}
		gfn = fn
		gcStress()
		ck(gfn() == 6, "closure in global func var")

		var i interface{} = fn
		gcStress()
		ck(i.(func() int64)() == 6, "closure through interface")
	}
	finish("p1_13_closure_capture")
}
