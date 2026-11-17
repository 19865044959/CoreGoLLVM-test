// P1-12 (报告风险 A11): defer/panic/recover 中的指针。
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

type wrap struct{ P *node }

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
func deferArg(p *node, out *int64) {
	gcStress()
	*out = p.v
}

//go:noinline
func useDeferArg() (out int64) {
	p := &node{v: 314, name: "dg"}
	defer deferArg(p, &out)
	gcStress()
	return 0
}

//go:noinline
func panicky() (r int64, ok bool) {
	p := &node{v: 42}
	defer func() {
		if e := recover(); e != nil {
			gcStress()
			r = p.v
			if w, is := e.(wrap); is {
				r += w.P.v
			}
			ok = true
		}
	}()
	p = &node{v: 43}
	gcStress()
	panic(wrap{P: &node{v: 9}})
}

//go:noinline
func loopDefer() (sum int64) {
	for i := 0; i < 8; i++ {
		p := &node{v: int64(i)}
		defer func() {
			gcStress()
			sum += p.v
		}()
	}
	return
}

//go:noinline
func noPanic() (r int64, ok bool) {
	defer func() {
		if e := recover(); e != nil {
			ok = true
		}
	}()
	gcStress()
	return 5, false
}

func main() {
	for i := 0; i < 10; i++ {
		ck(useDeferArg() == 314, "defer arg across GC")
		r, ok := panicky()
		ck(ok && r == 52, "panic/recover with pointer payload")
		ck(loopDefer() == 28, "deferred closures in loop")
		n, pok := noPanic()
		ck(n == 5 && !pok, "recover without panic")
	}
	finish("p1_12_defer_panic_recover")
}
