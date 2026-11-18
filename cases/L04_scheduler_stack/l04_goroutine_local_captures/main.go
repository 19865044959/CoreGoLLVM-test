// L04: 每个 goroutine 的闭包捕获独立指针链与堆切片，跨 Gosched/阻塞/GC
// 后仍只看到自己的数据，不串号、不丢失。
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

type captureResult struct {
	id  int
	acc int64
	bad int32
}

func captureWorker(id, steps int, seed int64, ready chan<- int, release <-chan struct{}, out chan<- captureResult) {
	base := int64(id)*1000 + seed
	head := &node{v: base}
	head.next = &node{v: base + 1}
	head.next.next = &node{v: base + 2}
	vals := make([]int64, 4)
	chainSum := base*3 + 3

	var bad int32
	// inner 闭包按值/按引用捕获本 goroutine 独有的 head 与 vals。
	inner := func(step int) int64 {
		vals[step%4] = base + int64(step)
		var s int64
		for p := head; p != nil; p = p.next {
			s += p.v
		}
		return s + vals[step%4]
	}
	verify := func(half int) {
		var s int64
		n := 0
		for p := head; p != nil; p = p.next {
			s += p.v
			n++
		}
		if n != 3 || s != chainSum {
			bad = 1
		}
		for j := 0; j < len(vals); j++ {
			last := half - len(vals) + j
			if vals[j] != base+int64(last) {
				bad = 1
			}
		}
	}

	var acc int64
	for step := 0; step < steps; step++ {
		if step == steps/2 {
			verify(steps / 2)
			ready <- id
			<-release
			// 所有 worker 阻塞时主协程已 GC；唤醒后闭包捕获的指针必须仍有效。
			verify(steps / 2)
		}
		acc += inner(step)
		if step%8 == 7 {
			runtime.Gosched()
		}
	}
	out <- captureResult{id: id, acc: acc, bad: bad}
}

func main() {
	seed := envSeed()
	const workers = 24
	const steps = 48

	ready := make(chan int, workers)
	release := make(chan struct{})
	out := make(chan captureResult, workers)
	for id := 0; id < workers; id++ {
		go captureWorker(id, steps, seed, ready, release, out)
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
		chainSum := base*3 + 3
		var want int64
		for step := 0; step < steps; step++ {
			want += chainSum + base + int64(step)
		}
		ckf(r.bad == 0, "worker %d captured pointers mixed up across GC", r.id)
		ckf(r.acc == want, "worker %d acc=%d want=%d", r.id, r.acc, want)
	}
	for id, ok := range seen {
		ckf(ok, "worker %d missing result", id)
	}
	finish("l04_goroutine_local_captures")
}
