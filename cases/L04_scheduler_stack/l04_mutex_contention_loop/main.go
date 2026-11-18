// L04: mutex 争用：多 goroutine 固定次数抢同一把锁，临界区内更新计数、
// 周期性换入新的指针链并在持有锁时 Gosched 放大争用；争用中与结束后各显式 GC。
package main

import (
	"fmt"
	"os"
	"runtime"
	"strconv"
	"sync"
	"sync/atomic"
)

type mnode struct {
	v    int64
	next *mnode
}

type sharedState struct {
	mu    sync.Mutex
	total int64
	head  *mnode
	keep  []*mnode
}

var (
	failed    int32
	gcSink    [][]byte
	active    int32
	maxActive int32
)

const replaceEvery = 17

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

func updateMax(v int32) {
	for {
		m := atomic.LoadInt32(&maxActive)
		if v <= m || atomic.CompareAndSwapInt32(&maxActive, m, v) {
			return
		}
	}
}

// hitShared 完成一次完整的临界区访问；返回值表示进入后读到的链是否完好。
func hitShared(sh *sharedState, seed int64, round int) bool {
	sh.mu.Lock()
	updateMax(atomic.AddInt32(&active, 1))
	sh.total++
	ok := true
	if sh.total%replaceEvery == 0 {
		b := seed*100003 + int64(round)*1000 + sh.total
		h := &mnode{v: b, next: &mnode{v: b + 1, next: &mnode{v: b + 2}}}
		sh.head = h
		sh.keep = append(sh.keep, h, h.next, h.next.next)
	}
	h := sh.head
	if h == nil || h.next == nil || h.next.next == nil ||
		h.next.v != h.v+1 || h.next.next.v != h.v+2 {
		ok = false
	}
	// 持锁让出，制造更多排队与交接。
	runtime.Gosched()
	atomic.AddInt32(&active, -1)
	sh.mu.Unlock()
	return ok
}

func runContention(round, workers, iters int, seed int64) {
	initBase := seed*100003 + int64(round)*1000
	sh := &sharedState{head: &mnode{v: initBase, next: &mnode{v: initBase + 1, next: &mnode{v: initBase + 2}}}}
	atomic.StoreInt32(&active, 0)
	atomic.StoreInt32(&maxActive, 0)
	var wg sync.WaitGroup
	var bad int32

	wg.Add(workers)
	for w := 0; w < workers; w++ {
		go func() {
			defer wg.Done()
			for k := 0; k < iters; k++ {
				if !hitShared(sh, seed, round) {
					atomic.AddInt32(&bad, 1)
				}
			}
		}()
	}

	// 争用进行中 GC：多数工作表/等待队列上都有私有数据。
	gcStress()

	// 主协程也作为竞争者，直到所有 worker 汇合。
	for k := 0; k < iters; k++ {
		if !hitShared(sh, seed, round) {
			atomic.AddInt32(&bad, 1)
		}
	}
	wg.Wait()

	totalMax := int64((workers + 1) * iters)
	ckf(sh.total == totalMax, "round %d total=%d want=%d", round, sh.total, totalMax)
	ckf(atomic.LoadInt32(&bad) == 0, "round %d critical section saw corrupted chain", round)
	ckf(atomic.LoadInt32(&maxActive) == 1, "round %d max active=%d want=1", round, maxActive)

	repl := int(totalMax) / replaceEvery
	ckf(len(sh.keep) == repl*3, "round %d keep=%d want=%d", round, len(sh.keep), repl*3)
	for k := 1; k <= repl; k++ {
		want := seed*100003 + int64(round)*1000 + int64(k*replaceEvery)
		idx := 3 * (k - 1)
		if idx+2 >= len(sh.keep) {
			break
		}
		ckf(sh.keep[idx].v == want && sh.keep[idx].next == sh.keep[idx+1] &&
			sh.keep[idx+1].next == sh.keep[idx+2] &&
			sh.keep[idx+1].v == want+1 && sh.keep[idx+2].v == want+2,
			"round %d replacement %d corrupted", round, k)
	}

	// 锁外 GC 后复验最新链与全部换入链仍存活。
	gcStress()
	if sh.head == nil || sh.head.next == nil || sh.head.next.next == nil {
		ckf(false, "round %d head missing after final GC", round)
	} else {
		ckf(sh.head.next.v == sh.head.v+1 && sh.head.next.next.v == sh.head.v+2,
			"round %d head corrupted after final GC", round)
	}
	for i, n := range sh.keep {
		ckf(n != nil, "round %d keep[%d] missing after final GC", round, i)
	}
}

func main() {
	seed := envSeed()
	rounds := envInt("TORTURE_ROUNDS", 2, 1, 10)
	workers := 12
	iters := 256
	for r := 0; r < rounds; r++ {
		runContention(r, workers, iters, seed)
	}
	finish("l04_mutex_contention_loop")
}
