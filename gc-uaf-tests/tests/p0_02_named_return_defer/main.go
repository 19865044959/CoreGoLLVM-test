// P0-02 (报告风险 A1/A2/A11): 命名返回值 + defer 跨 GC。
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
func rvBasic() (r *node) {
	defer func() { r = &node{v: 99, name: "defer"} }()
	gcStress()
	r = &node{v: 1, name: "body"}
	return
}

//go:noinline
func rvDeferUses() (r *node, n int64) {
	r = &node{v: 5, name: "five"}
	n = 7
	defer func() {
		gcStress()
		n += r.v
	}()
	return
}

//go:noinline
func rvDeferCapture() (out *node) {
	p := &node{v: 8, name: "cap"}
	defer func() {
		gcStress()
		out = p
	}()
	return
}

//go:noinline
func rvPanicRecover() (r *node) {
	defer func() {
		if e := recover(); e != nil {
			gcStress()
			r = &node{v: 77}
		}
	}()
	panic("boom")
}

//go:noinline
func rvMulti(sel int) (r *node, n int64) {
	defer func() { gcStress(); n++ }()
	if sel == 0 {
		r = &node{v: 100}
		return
	}
	r = &node{v: 200}
	return
}

func main() {
	for i := 0; i < 10; i++ {
		a := rvBasic()
		gcStress()
		ck(a.v == 99 && a.name == "defer", "rvBasic")

		b, n := rvDeferUses()
		gcStress()
		ck(b.v == 5, "rvDeferUses pointer")
		ck(n == 12, "rvDeferUses n")

		c := rvDeferCapture()
		gcStress()
		ck(c.v == 8 && c.name == "cap", "rvDeferCapture")

		d := rvPanicRecover()
		gcStress()
		ck(d.v == 77, "rvPanicRecover")

		e0, n0 := rvMulti(0)
		e1, n1 := rvMulti(1)
		gcStress()
		ck(e0.v == 100 && n0 == 1, "rvMulti sel0")
		ck(e1.v == 200 && n1 == 1, "rvMulti sel1")
	}
	finish("p0_02_named_return_defer")
}
