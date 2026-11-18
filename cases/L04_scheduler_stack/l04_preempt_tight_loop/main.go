// L04: 紧循环下的调度饥饿观察：GOMAXPROCS=1，多个 worker 在无显式让出的
// 固定迭代紧循环中计算；observer 以固定轮询预算确认所有 worker 完成，超出预算
// 即判定调度饥饿。紧循环期间插入 GC，校验栈上指针链。
package main

import (
	"fmt"
	"os"
	"runtime"
	"strconv"
	"sync"
	"sync/atomic"
)

type pnode struct {
	v    int64
	next *pnode
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

func envInt(name string, def, lo, hi int) int {
	s := os.Getenv(name)
	if s == "" {
		return def
	}
	n, err := strconv.Atoi(s)
	if err != nil || n < lo {
		return def
	}
	if n > hi {
		n = hi
	}
	return n
}

// tightAcc 是无显式调度点的纯算术循环，只依赖运行时抢占。
//
//go:noinline
func tightAcc(id, iters int) uint64 {
	x := uint64(id)*0x9e3779b97f4a7c15 + 0x243f6a8885a308d3
	var acc uint64
	for i := 0; i < iters; i++ {
		x = x*6364136223846793005 + 1442695040888963407
		acc += x >> 40
	}
	return acc
}

type observeResult struct {
	starved bool
	ordered bool
}

func observeProgress(finished *int64, workers, pollCap int) observeResult {
	last := int64(-1)
	ordered := true
	for i := 0; i < pollCap; i++ {
		v := atomic.LoadInt64(finished)
		if v < last || v < 0 || v > int64(workers) {
			ordered = false
		}
		last = v
		if v == int64(workers) {
			return observeResult{starved: false, ordered: ordered}
		}
		runtime.Gosched()
	}
	return observeResult{starved: true, ordered: ordered}
}

func runPreemptRound(round, workers, iters int, seed int64) {
	old := runtime.GOMAXPROCS(0)
	runtime.GOMAXPROCS(1)
	defer runtime.GOMAXPROCS(old)

	expected := make([]uint64, workers)
	for w := 0; w < workers; w++ {
		expected[w] = tightAcc(w, iters)
	}

	gate := make(chan struct{})
	ready := make(chan int, workers)
	chains := make([]*pnode, workers)
	got := make([]uint64, workers)
	var finished int64
	var bad int32
	var wg sync.WaitGroup

	wg.Add(workers)
	for w := 0; w < workers; w++ {
		go func(w int) {
			defer wg.Done()
			b := seed*1000003 + int64(round)*10007 + int64(w)*31
			head := &pnode{v: b, next: &pnode{v: b + 1, next: &pnode{v: b + 2}}}
			chains[w] = head
			ready <- w
			<-gate
			// 无 Gosched 的紧循环；GOMAXPROCS=1 下只能靠抢占让出。
			got[w] = tightAcc(w, iters)
			if head.next == nil || head.next.next == nil ||
				head.next.v != b+1 || head.next.next.v != b+2 {
				atomic.AddInt32(&bad, 1)
			}
			atomic.AddInt64(&finished, 1)
		}(w)
	}
	for i := 0; i < workers; i++ {
		<-ready
	}

	obsCh := make(chan observeResult, 1)
	go func() { obsCh <- observeProgress(&finished, workers, 1<<23) }()

	// 所有 worker 停在 gate、链仍活跃在各自栈上时 GC。
	gcStress()
	close(gate)
	wg.Wait()
	obs := <-obsCh

	ckf(!obs.starved, "round %d observer exhausted poll budget (scheduling starvation)", round)
	ckf(obs.ordered, "round %d progress counter observed out of order", round)
	ckf(atomic.LoadInt64(&finished) == int64(workers), "round %d finished=%d want=%d", round, finished, workers)
	ckf(atomic.LoadInt32(&bad) == 0, "round %d chain corrupted in tight loop", round)
	for w := 0; w < workers; w++ {
		ckf(got[w] == expected[w], "round %d worker %d checksum mismatch", round, w)
	}

	gcStress()
	for w := 0; w < workers; w++ {
		b := seed*1000003 + int64(round)*10007 + int64(w)*31
		h := chains[w]
		if h == nil || h.next == nil || h.next.next == nil {
			ckf(false, "round %d worker %d chain missing after final GC", round, w)
			continue
		}
		ckf(h.v == b && h.next.v == b+1 && h.next.next.v == b+2,
			"round %d worker %d chain corrupted after final GC", round, w)
	}
}

func main() {
	seed := envSeed()
	rounds := envInt("TORTURE_ROUNDS", 2, 1, 12)
	iters := envInt("TORTURE_ITERS", 2000000, 100000, 20000000)
	const workers = 4
	for r := 0; r < rounds; r++ {
		runPreemptRound(r, workers, iters, seed)
	}
	finish("l04_preempt_tight_loop")
}
