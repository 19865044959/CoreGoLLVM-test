// L04: mutex convoy：owner 持锁期间所有 waiter 依次排到锁队列上，各自栈上持有
// 私有指针链。main 在队列形成后 GC，再放行 owner 解锁；waiter 逐个获得锁，
// 校验共享链与私有链，且每次只有一个持有者。
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

type convoyState struct {
	mu         sync.Mutex
	served     int
	generation int
	head       *cnode
	keep       []*cnode
}

var (
	failed    int32
	gcSink    [][]byte
	convoyAct int32
	convoyMax int32
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

func bumpMax(v int32) {
	for {
		m := atomic.LoadInt32(&convoyMax)
		if v <= m || atomic.CompareAndSwapInt32(&convoyMax, m, v) {
			return
		}
	}
}

func runConvoy(seed int64, waiters int) {
	st := &convoyState{}
	ownerHeld := make(chan struct{})
	releaseOwner := make(chan struct{})
	ownerDone := make(chan struct{})
	attempting := make(chan int, waiters)
	orders := make(chan int, waiters)
	private := make([]*cnode, waiters)
	var bad int32

	b0 := seed*100003 + 7001
	st.head = &cnode{v: b0, next: &cnode{v: b0 + 1, next: &cnode{v: b0 + 2}}}
	st.keep = append(st.keep, st.head, st.head.next, st.head.next.next)

	go func() {
		st.mu.Lock()
		close(ownerHeld)
		<-releaseOwner
		st.generation = 1
		st.mu.Unlock()
		close(ownerDone)
	}()
	<-ownerHeld

	var wg sync.WaitGroup
	wg.Add(waiters)
	for w := 0; w < waiters; w++ {
		go func(w int) {
			defer wg.Done()
			b := seed*100003 + int64(w)*131
			h := &cnode{v: b, next: &cnode{v: b + 5}}
			private[w] = h
			attempting <- w
			st.mu.Lock()
			bumpMax(atomic.AddInt32(&convoyAct, 1))
			h2 := st.head
			if st.generation != 1 {
				atomic.AddInt32(&bad, 1)
			}
			if h2 == nil || h2.next == nil || h2.next.next == nil ||
				h2.v != b0 || h2.next.v != b0+1 || h2.next.next.v != b0+2 {
				atomic.AddInt32(&bad, 1)
			}
			st.served++
			my := st.served
			if h == nil || h.next == nil || h.next.v != b+5 {
				atomic.AddInt32(&bad, 1)
			}
			atomic.AddInt32(&convoyAct, -1)
			st.mu.Unlock()
			orders <- my
		}(w)
	}

	for i := 0; i < waiters; i++ {
		<-attempting
	}
	// owner 仍持锁、waiter 全部排在锁队列（或即将进入）时 GC。
	gcStress()
	close(releaseOwner)
	<-ownerDone
	wg.Wait()
	close(orders)

	st.mu.Lock()
	served := st.served
	gen := st.generation
	head := st.head
	st.mu.Unlock()

	ckf(served == waiters, "served=%d want=%d", served, waiters)
	ckf(gen == 1, "generation=%d want=1", gen)
	seen := make([]bool, waiters)
	count := 0
	for my := range orders {
		if my < 1 || my > waiters {
			ckf(false, "order %d out of range", my)
			continue
		}
		if seen[my-1] {
			ckf(false, "order %d duplicated", my)
			continue
		}
		seen[my-1] = true
		count++
	}
	ckf(count == waiters, "unique orders=%d want=%d", count, waiters)
	ckf(atomic.LoadInt32(&convoyMax) == 1, "max concurrent holders=%d want=1", convoyMax)
	ckf(atomic.LoadInt32(&bad) == 0, "waiter saw corrupted convoy state")

	gcStress()
	if head == nil || head.next == nil || head.next.next == nil ||
		head.v != b0 || head.next.v != b0+1 || head.next.next.v != b0+2 {
		ckf(false, "shared chain corrupted after convoy GC")
	}
	for w := 0; w < waiters; w++ {
		h := private[w]
		b := seed*100003 + int64(w)*131
		if h == nil || h.next == nil || h.v != b || h.next.v != b+5 {
			ckf(false, "waiter %d private chain corrupted after convoy GC", w)
		}
	}
	finish("l04_mutex_convoy_queue")
}

func main() {
	seed := envSeed()
	waiters := envInt("TORTURE_WAITERS", 10, 2, 32)
	runConvoy(seed, waiters)
}
