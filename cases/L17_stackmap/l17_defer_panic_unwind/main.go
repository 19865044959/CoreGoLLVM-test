// L17: defer/panic/recover 展开路径上的指针与命名返回值。
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
func panicDeep(depth int, p *node) int64 {
	local := &node{v: int64(depth), name: "deep"}
	if depth == 0 {
		gcStress()
		panic(wrap{P: p})
	}
	r := panicDeep(depth-1, local)
	return r + local.v
}

//go:noinline
func recoverDeep() (sum int64, ok bool) {
	defer func() {
		if e := recover(); e != nil {
			gcStress()
			if w, is := e.(wrap); is && w.P != nil && w.P.name == "deep" {
				sum = w.P.v
				ok = true
			}
		}
	}()
	return panicDeep(1500, &node{v: 77, name: "deep"}), false
}

//go:noinline
func deferArg(p *node, out *int64) {
	gcStress()
	*out = p.v
}

//go:noinline
func useDeferArg() (out int64) {
	p := &node{v: 314, name: "arg"}
	defer deferArg(p, &out)
	gcStress()
	return 0
}

//go:noinline
func loopDefers() (sum int64) {
	for i := 0; i < 8; i++ {
		p := &node{v: int64(i), name: "loop"}
		defer func() {
			gcStress()
			sum += p.v
		}()
	}
	return
}

func main() {
	sum, ok := recoverDeep()
	ck(ok && sum == 1, "deep panic/recover pointer")
	ck(useDeferArg() == 314, "defer arg evaluated before GC")
	ck(loopDefers() == 28, "deferred closures in loop")
	finish("l17_defer_panic_unwind")
}
