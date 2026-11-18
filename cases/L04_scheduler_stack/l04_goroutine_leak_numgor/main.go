// L04: goroutine 泄漏检测：短生命周期 worker + 阻塞 worker 结束后用
// runtime.NumGoroutine 前后对比、runtime.Stack 标记串与活跃计数三重确认收敛。
package main

import (
	"fmt"
	"os"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
)

type node struct {
	v    int64
	next *node
}

var (
	failed        int32
	gcSink        [][]byte
	activeWorkers int32
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

func shortWorker(id int, seed int64, out chan<- int64) {
	base := int64(id)*1000 + seed*19
	head := &node{v: base, next: &node{v: base + 3}}
	if head.next.v != head.v+3 {
		out <- -1
		runtime.KeepAlive(head)
		return
	}
	out <- head.v + head.next.v
	runtime.KeepAlive(head)
}

//go:noinline
func leakWorkerMarker(id int, seed int64, ready chan<- int, gate <-chan struct{}, out chan<- int64) {
	base := int64(id)*7 + seed*23
	head := &node{v: base, next: &node{v: base + 1}}
	atomic.AddInt32(&activeWorkers, 1)
	ready <- id
	<-gate
	if head.v != base || head.next.v != base+1 {
		out <- -1
		atomic.AddInt32(&activeWorkers, -1)
		return
	}
	out <- head.v + head.next.v
	atomic.AddInt32(&activeWorkers, -1)
}

func main() {
	seed := envSeed()
	const (
		rounds    = 4
		short     = 32
		lingering = 8
	)
	runtime.GC()
	before := runtime.NumGoroutine()

	var total int64
	var wantTotal int64
	for r := 0; r < rounds; r++ {
		gate := make(chan struct{})
		ready := make(chan int, lingering)
		out := make(chan int64, short+lingering)
		var wg sync.WaitGroup
		wg.Add(short + lingering)
		for i := 0; i < short; i++ {
			go func(id int) {
				defer wg.Done()
				shortWorker(id, seed+int64(r), out)
			}(i)
		}
		for i := 0; i < lingering; i++ {
			go func(id int) {
				defer wg.Done()
				leakWorkerMarker(id, seed+int64(r), ready, gate, out)
			}(i)
		}
		for i := 0; i < lingering; i++ {
			<-ready
		}
		// 探测器自检：此时标记 goroutine 必须以 leakWorkerMarker 的栈帧存在。
		buf := make([]byte, 1<<20)
		n := runtime.Stack(buf, true)
		ckf(strings.Contains(string(buf[:n]), "main.leakWorkerMarker"), "leak detector did not observe tagged worker (round %d)", r)
		gcStress()
		close(gate)
		wg.Wait()
		close(out)
		for v := range out {
			ckf(v >= 0, "worker result corrupted (round %d)", r)
			if v > 0 {
				total += v
			}
		}
		for id := 0; id < short; id++ {
			base := int64(id)*1000 + (seed+int64(r))*19
			wantTotal += 2*base + 3
		}
		for id := 0; id < lingering; id++ {
			base := int64(id)*7 + (seed+int64(r))*23
			wantTotal += 2*base + 1
		}
		gcStress()
	}

	after := runtime.NumGoroutine()
	for attempt := 0; attempt < 200000; attempt++ {
		after = runtime.NumGoroutine()
		if after <= before+2 && atomic.LoadInt32(&activeWorkers) == 0 {
			break
		}
		runtime.Gosched()
	}
	after = runtime.NumGoroutine()
	buf := make([]byte, 1<<20)
	n := runtime.Stack(buf, true)
	ckf(!strings.Contains(string(buf[:n]), "main.leakWorkerMarker"), "tagged worker goroutine leaked")
	ckf(atomic.LoadInt32(&activeWorkers) == 0, "active worker count=%d after join", atomic.LoadInt32(&activeWorkers))
	ckf(after <= before+2, "NumGoroutine grew before=%d after=%d", before, after)
	ckf(total == wantTotal, "result sum=%d want=%d", total, wantTotal)
	finish("l04_goroutine_leak_numgor")
}
