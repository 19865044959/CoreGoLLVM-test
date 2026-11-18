// L18: finalizer 探针：仍可达的对象不得被终结；失活对象必须被终结。
package main

import (
	"fmt"
	"os"
	"runtime"
	"sync/atomic"
	"time"
)

type node struct {
	v    int64
	name string
}

var (
	failed int32
	gcSink [][]byte

	finKept    int32
	finDropped int32
	finBad     int32

	kept *node
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
func installKept() {
	p := &node{v: 123, name: "kept"}
	runtime.SetFinalizer(p, func(q *node) {
		if q.v != 123 || q.name != "kept" {
			atomic.AddInt32(&finBad, 1)
		}
		atomic.AddInt32(&finKept, 1)
	})
	kept = p
}

//go:noinline
func installDropped() {
	p := &node{v: 456, name: "dropped"}
	runtime.SetFinalizer(p, func(q *node) {
		if q.v != 456 || q.name != "dropped" {
			atomic.AddInt32(&finBad, 1)
		}
		atomic.AddInt32(&finDropped, 1)
	})
}

func main() {
	installKept()
	for i := 0; i < 3; i++ {
		gcStress()
	}
	ck(atomic.LoadInt32(&finKept) == 0, "finalizer ran while object reachable")

	installDropped()
	deadline := time.Now().Add(10 * time.Second)
	for atomic.LoadInt32(&finDropped) == 0 && time.Now().Before(deadline) {
		runtime.GC()
		time.Sleep(2 * time.Millisecond)
	}
	ck(atomic.LoadInt32(&finDropped) == 1, "dropped object finalizer did not run")
	ck(atomic.LoadInt32(&finKept) == 0, "kept object finalized prematurely")
	ck(atomic.LoadInt32(&finBad) == 0, "finalizer saw corrupted payload")

	runtime.KeepAlive(kept)
	finish("l18_finalizer_reachable")
}
