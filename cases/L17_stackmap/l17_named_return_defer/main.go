// L17: 命名返回值 + defer：GC 落在返回/延迟调用路径上，返回值仍必须正确。
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
func namedBasic() (r *node) {
	defer func() { r = &node{v: 99, name: "defer"} }()
	gcStress()
	r = &node{v: 1, name: "body"}
	return
}

//go:noinline
func namedDeferUses() (r *node, n int64) {
	r = &node{v: 5, name: "five"}
	n = 7
	defer func() {
		gcStress()
		n += r.v
	}()
	return
}

//go:noinline
func namedMulti(sel int) (r *node, n int64) {
	defer func() { gcStress(); n++ }()
	if sel == 0 {
		r = &node{v: 100}
		return
	}
	r = &node{v: 200}
	return
}

//go:noinline
func namedPanicRecover() (r *node) {
	defer func() {
		if e := recover(); e != nil {
			gcStress()
			r = &node{v: 77, name: "recovered"}
		}
	}()
	panic("boom")
}

func main() {
	for i := 0; i < 20; i++ {
		a := namedBasic()
		gcStress()
		ck(a.v == 99 && a.name == "defer", "defer overwrote named return")

		b, n := namedDeferUses()
		gcStress()
		ck(b.v == 5 && n == 12, "defer read named return across GC")

		e0, n0 := namedMulti(0)
		e1, n1 := namedMulti(1)
		gcStress()
		ck(e0.v == 100 && n0 == 1, "namedMulti path0")
		ck(e1.v == 200 && n1 == 1, "namedMulti path1")

		d := namedPanicRecover()
		gcStress()
		ck(d.v == 77 && d.name == "recovered", "named return set during recover")
	}
	finish("l17_named_return_defer")
}
