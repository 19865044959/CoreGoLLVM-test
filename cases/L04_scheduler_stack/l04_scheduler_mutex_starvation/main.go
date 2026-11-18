// L04: 调度器+锁饥饿观察。GOMAXPROCS=1 下单 goroutine 紧循环反复 Lock/Unlock，
// waiter 在锁队列上等待并持有栈指针链。Hog 在固定迭代预算内必须让所有 waiter
// 获得临界区（Go mutex 的饥饿模式），否则判定饥饿；GC 落在 waiter 阻塞时。
package main

import (
	"fmt"
	"os"
	"runtime"
	"strconv"
	"sync"
	"sync/atomic"
)

type stNode struct {
	v    int64
	next *stNode
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

func finish(name string, seed int64) {
	if n := atomic.LoadInt32(&failed); n != 0 {
		fmt.Printf("FAILED %s (UAF_SEED=%d, %d failed checks)\n", name, seed, n)
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

func envRounds() int {
	s := os.Getenv("TORTURE_ROUNDS")
	if s == "" {
		return 1
	}
	v, err := strconv.Atoi(s)
	if err != nil || v < 1 {
		return 1
	}
	return v
}

func main() {
	seed := envSeed()
	rounds := 2 * envRounds()
	const waiters = 3
	const maxAcq = int32(4000000)

	oldProcs := runtime.GOMAXPROCS(1)
	defer runtime.GOMAXPROCS(oldProcs)

	for round := 0; round < rounds; round++ {
		var mu sync.Mutex
		var shared, served, attempts int32
		waitStart := make(chan struct{})
		hogStart := make(chan struct{})
		servedCh := make(chan int, waiters)
		hogDone := make(chan int32, 1)
		heads := make([]*stNode, waiters)

		for w := 0; w < waiters; w++ {
			go func(w int) {
				b := seed*100003 + int64(round)*7919 + int64(w)*977
				head := &stNode{v: b, next: &stNode{v: b + 1, next: &stNode{v: b + 2}}}
				heads[w] = head
				atomic.AddInt32(&attempts, 1)
				<-waitStart
				mu.Lock()
				if head.v != b || head.next == nil || head.next.v != b+1 || head.next.next == nil || head.next.next.v != b+2 {
					ckf(false, "round %d waiter %d chain corrupted in critical section", round, w)
				}
				if atomic.LoadInt32(&shared) <= 0 {
					ckf(false, "round %d waiter %d observed empty critical section", round, w)
				}
				atomic.AddInt32(&served, 1)
				mu.Unlock()
				servedCh <- w
			}(w)
		}

		go func() {
			<-hogStart
			var i int32
			for ; i < maxAcq; i++ {
				mu.Lock()
				atomic.AddInt32(&shared, 1)
				mu.Unlock()
				if atomic.LoadInt32(&served) == waiters {
					break
				}
				if i%4096 == 0 {
					// 显式让出，保证非 async-preempt 的 profile 下 waiter 也有运行机会。
					runtime.Gosched()
				}
			}
			hogDone <- i
		}()

		// 先让 hog 独占跑一段，再放 waiter 进入锁队列。
		close(hogStart)
		spins := 0
		for atomic.LoadInt32(&shared) < 1024 {
			runtime.Gosched()
			spins++
			if spins > 4000000 {
				ckf(false, "round %d hog made no progress", round)
				break
			}
		}
		for atomic.LoadInt32(&attempts) < waiters {
			runtime.Gosched()
		}
		close(waitStart)
		// waiter 已进入 Lock、hog 持续抢锁时显式 GC。
		gcStress()

		servedSet := make([]bool, waiters)
		for i := 0; i < waiters; i++ {
			w := <-servedCh
			if w < 0 || w >= waiters || servedSet[w] {
				ckf(false, "round %d bad/duplicate served waiter %d", round, w)
			}
			servedSet[w] = true
		}
		iters := <-hogDone
		ckf(atomic.LoadInt32(&served) == waiters, "round %d served=%d want=%d", round, atomic.LoadInt32(&served), waiters)
		ckf(iters < maxAcq, "round %d starvation: hog hit maxAcq=%d before all waiters", round, maxAcq)
		if iters <= 0 {
			ckf(false, "round %d hog acquired no lock", round)
		}
		for w := 0; w < waiters; w++ {
			b := seed*100003 + int64(round)*7919 + int64(w)*977
			h := heads[w]
			ok := h != nil && h.v == b && h.next != nil && h.next.v == b+1 &&
				h.next.next != nil && h.next.next.v == b+2
			ckf(ok, "round %d waiter %d chain corrupted after starvation round", round, w)
		}
	}

	gcStress()
	finish("l04_scheduler_mutex_starvation", seed)
}
