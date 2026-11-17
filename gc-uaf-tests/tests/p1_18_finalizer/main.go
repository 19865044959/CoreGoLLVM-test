// P1-18 (报告风险 A1 漏标探针): finalizer 探针。
// 仍可达的对象 finalizer 不得执行；失活对象 finalizer 必须在超时内执行。
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

	kept      *node
	keptIface interface{}
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
		if q.v != 123 {
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
		if q.v != 456 {
			atomic.AddInt32(&finBad, 1)
		}
		atomic.AddInt32(&finDropped, 1)
	})
}

//go:noinline
func installIfaceDropped() {
	p := &node{v: 789}
	runtime.SetFinalizer(p, func(q *node) {
		atomic.AddInt32(&finDropped, 1)
	})
	keptIface = p
	keptIface = nil
}

func main() {
	installKept()
	for i := 0; i < 3; i++ {
		gcStress()
	}
	ck(atomic.LoadInt32(&finKept) == 0, "finalizer ran while object reachable")

	installIfaceDropped()
	installDropped()

	deadline := time.Now().Add(10 * time.Second)
	for atomic.LoadInt32(&finDropped) < 2 && time.Now().Before(deadline) {
		runtime.GC()
		time.Sleep(2 * time.Millisecond)
	}
	ck(atomic.LoadInt32(&finDropped) == 2, "dropped finalizers ran")
	ck(atomic.LoadInt32(&finKept) == 0, "kept finalizer still not run")
	ck(atomic.LoadInt32(&finBad) == 0, "finalizer payload corrupted")
	runtime.KeepAlive(kept)
	runtime.KeepAlive(keptIface)
	finish("p1_18_finalizer")
}
