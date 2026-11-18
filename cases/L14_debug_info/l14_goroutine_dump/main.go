// L14: runtime.Stack 全量 goroutine 栈 dump 中包含阻塞函数符号，且阻塞期间的指针在 GC 后有效。
package main

import (
	"fmt"
	"os"
	"runtime"
	"strings"
	"sync"
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
func blockedWorker(ch <-chan struct{}, ready chan<- struct{}, p *node, errs *int32) {
	ready <- struct{}{}
	<-ch
	if p == nil || p.v != 7 || p.name != "blocked" {
		atomic.AddInt32(errs, 1)
	}
}

func main() {
	const n = 8
	ch := make(chan struct{})
	ready := make(chan struct{}, n)
	errs := int32(0)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		p := &node{v: 7, name: "blocked"}
		go func(p *node) {
			defer wg.Done()
			blockedWorker(ch, ready, p, &errs)
		}(p)
	}
	for i := 0; i < n; i++ {
		<-ready
	}
	gcStress()

	buf := make([]byte, 1<<20)
	got := runtime.Stack(buf, true)
	s := string(buf[:got])
	ck(strings.Contains(s, "blockedWorker"), "goroutine dump has blockedWorker")
	ck(strings.Contains(s, "goroutine "), "goroutine dump header")
	ck(strings.Contains(s, "main.main"), "goroutine dump has main.main")
	ck(strings.Contains(s, "chan receive"), "goroutine dump has chan receive state")

	close(ch)
	wg.Wait()
	ck(atomic.LoadInt32(&errs) == 0, "blocked worker pointers after GC")
	finish("l14_goroutine_dump")
}
