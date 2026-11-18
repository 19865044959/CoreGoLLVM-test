// L04: 多 goroutine 高频 Gosched，局部指针链跨让出与 GC 后仍有效；
// 所有 worker 在让出中途阻塞时主协程强制 GC。
package main

import (
	"fmt"
	"os"
	"runtime"
	"strconv"
	"sync/atomic"
)

type node struct {
	v    int64
	next *node
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

func ckf(ok bool, format string, args ...any) {
	if !ok {
		atomic.AddInt32(&failed, 1)
		fmt.Printf("FAIL: "+format+"\n", args...)
	}
}

func gcStress() {
	for i := 0; i < 8; i++ {
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

func envSeed() int64 {
	s := os.Getenv("UAF_SEED")
	if s == "" {
		return 1
	}
	v, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return 1
	}
	return v
}

type yieldResult struct {
	id  int
	acc int64
	bad int32
}

func yieldWorker(id, steps int, seed int64, ready chan<- int, release <-chan struct{}, out chan<- yieldResult) {
	base := int64(id)*1000 + seed
	head := &node{v: base}
	head.next = &node{v: base + 7}
	head.next.next = &node{v: base + 14}
	wantSum := base*3 + 21

	var bad int32
	check := func() {
		var sum int64
		n := 0
		for p := head; p != nil; p = p.next {
			sum += p.v
			n++
		}
		if n != 3 || sum != wantSum {
			bad = 1
		}
	}

	var acc int64
	for step := 0; step < steps; step++ {
		if step == steps/2 {
			check()
			ready <- id
			<-release
			// release 阻塞点是 GC 安全点：唤醒后指针链与累加器必须完整。
			check()
		}
		check()
		acc += int64(step) + wantSum
		// 每一步都主动让出，扩大调度器在安全点扫描栈的机会。
		runtime.Gosched()
	}
	out <- yieldResult{id: id, acc: acc, bad: bad}
}

func main() {
	seed := envSeed()
	const workers = 16
	const steps = 64

	ready := make(chan int, workers)
	release := make(chan struct{})
	out := make(chan yieldResult, workers)
	for id := 0; id < workers; id++ {
		go yieldWorker(id, steps, seed, ready, release, out)
	}
	for i := 0; i < workers; i++ {
		<-ready
	}
	gcStress()
	close(release)

	seen := make([]bool, workers)
	for i := 0; i < workers; i++ {
		r := <-out
		if r.id < 0 || r.id >= workers {
			ckf(false, "bad worker id %d", r.id)
			continue
		}
		seen[r.id] = true
		base := int64(r.id)*1000 + seed
		wantSum := base*3 + 21
		wantAcc := int64(steps*(steps-1)/2) + int64(steps)*wantSum
		ckf(r.bad == 0, "worker %d pointer chain broken across Gosched/GC", r.id)
		ckf(r.acc == wantAcc, "worker %d acc=%d want=%d", r.id, r.acc, wantAcc)
	}
	for id, ok := range seen {
		ckf(ok, "worker %d missing result", id)
	}
	finish("l04_gosched_fairness")
}
