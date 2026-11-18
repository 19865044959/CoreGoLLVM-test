// L04: sync.Cond 睡眠/唤醒：waiters 在 gen==0 上 Wait，主协程确认全部入睡后
// 先 GC 再持锁逐个 Signal；每个 waiter 只被唤醒一次，栈上私有链跨睡眠存活。
package main

import (
	"fmt"
	"os"
	"runtime"
	"strconv"
	"sync"
	"sync/atomic"
)

type cnode struct {
	v    int64
	next *cnode
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

func runCondRound(round, n int, seed int64) {
	var mu sync.Mutex
	cond := sync.NewCond(&mu)
	sharedBase := seed*1000003 + int64(round)*517
	shared := &cnode{v: sharedBase, next: &cnode{v: sharedBase + 1, next: &cnode{v: sharedBase + 2}}}
	state := make([]int32, n)
	waiting := 0
	gen := 0
	var bad int32
	var wg sync.WaitGroup

	wg.Add(n)
	for i := 0; i < n; i++ {
		go func(i int) {
			defer wg.Done()
			b := sharedBase + int64(i)*29
			priv := &cnode{v: b, next: &cnode{v: b + 1, next: &cnode{v: b + 2}}}
			mu.Lock()
			waiting++
			for gen == 0 {
				cond.Wait()
			}
			if shared.next == nil || shared.next.next == nil ||
				shared.v != sharedBase || shared.next.v != sharedBase+1 || shared.next.next.v != sharedBase+2 {
				atomic.AddInt32(&bad, 1)
			}
			atomic.StoreInt32(&state[i], 1)
			mu.Unlock()
			// 唤醒后复验睡眠期间一直被栈引用的私有链。
			if priv.next == nil || priv.next.next == nil ||
				priv.v != b || priv.next.v != b+1 || priv.next.next.v != b+2 {
				atomic.AddInt32(&bad, 1)
			}
		}(i)
	}

	// 持锁确认全部 waiter 已进入 Wait：waiting 在 Wait 前于锁内递增。
	mu.Lock()
	spins := 0
	for waiting < n {
		mu.Unlock()
		runtime.Gosched()
		spins++
		mu.Lock()
		if spins > 1<<24 {
			ckf(false, "round %d only %d/%d waiters slept", round, waiting, n)
			break
		}
	}
	mu.Unlock()

	// 全部 waiter 睡在 cond 的信号量队列上时 GC。
	gcStress()

	mu.Lock()
	gen = 1
	for i := 0; i < n; i++ {
		cond.Signal()
	}
	mu.Unlock()
	wg.Wait()

	gcStress()
	for i := 0; i < n; i++ {
		ckf(atomic.LoadInt32(&state[i]) == 1, "round %d waiter %d not woken", round, i)
	}
	ckf(atomic.LoadInt32(&bad) == 0, "round %d chain corrupted across sleep/wake", round)
	ckf(waiting == n, "round %d waiting=%d want=%d", round, waiting, n)
	if shared.next == nil || shared.next.next == nil ||
		shared.v != sharedBase || shared.next.v != sharedBase+1 || shared.next.next.v != sharedBase+2 {
		ckf(false, "round %d shared chain corrupted after final GC", round)
	}
}

func main() {
	seed := envSeed()
	rounds := envInt("TORTURE_ROUNDS", 2, 1, 12)
	waiters := envInt("TORTURE_WAITERS", 24, 4, 128)
	for r := 0; r < rounds; r++ {
		runCondRound(r, waiters, seed)
	}
	finish("l04_cond_sleep_wake")
}
