// L04: 信号量唤醒：容量 C 的 FIFO 信号量，main 先占满全部名额，M 个 waiter 排队；
// 队列形成后 GC，再取消其中 K 个（必须从队列摘除且不消耗名额），随后逐个 release
// 交接唤醒。断言每个非取消 waiter 恰获得一次、唤醒顺序 FIFO、峰值不超容量。
package main

import (
	"fmt"
	"os"
	"runtime"
	"strconv"
	"sync"
	"sync/atomic"
)

type semNode struct {
	v    int64
	next *semNode
}

type semWaiter struct {
	id    int
	ch    chan struct{}
	grant int
}

type fifoSem struct {
	mu     sync.Mutex
	free   int
	q      []*semWaiter
	order  []int
	grants []int
}

var (
	failed int32
	gcSink [][]byte
	semAct int32
	semMax int32
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

func bumpSemMax(v int32) {
	for {
		m := atomic.LoadInt32(&semMax)
		if v <= m || atomic.CompareAndSwapInt32(&semMax, m, v) {
			return
		}
	}
}

func (s *fifoSem) take() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.free == 0 {
		return false
	}
	s.free--
	return true
}

func (s *fifoSem) release() {
	s.mu.Lock()
	if len(s.q) > 0 {
		w := s.q[0]
		s.q = s.q[1:]
		lenQ := len(s.grants) + 1
		w.grant = lenQ
		s.grants = append(s.grants, w.id)
		close(w.ch)
	} else {
		s.free++
	}
	s.mu.Unlock()
}

// acquire 排队等待名额；done 关闭后从队列摘除并返回 false。
// 若取消与交接竞争且名额已授予，则吞下授予并返回 true（调用方负责归还）。
func (s *fifoSem) acquire(id int, done <-chan struct{}, queued chan<- struct{}) bool {
	s.mu.Lock()
	w := &semWaiter{id: id, ch: make(chan struct{})}
	s.q = append(s.q, w)
	s.order = append(s.order, id)
	s.mu.Unlock()
	queued <- struct{}{}
	select {
	case <-w.ch:
		return true
	case <-done:
		s.mu.Lock()
		idx := -1
		for i, x := range s.q {
			if x == w {
				idx = i
				break
			}
		}
		if idx >= 0 {
			s.q = append(s.q[:idx], s.q[idx+1:]...)
			oi := -1
			for i, oid := range s.order {
				if oid == id {
					oi = i
					break
				}
			}
			if oi >= 0 {
				s.order = append(s.order[:oi], s.order[oi+1:]...)
			}
			s.mu.Unlock()
			return false
		}
		s.mu.Unlock()
		<-w.ch
		return true
	}
}

func runSemRound(r int, seed int64, total, capacity, canceled int) {
	s := &fifoSem{free: capacity}
	for i := 0; i < capacity; i++ {
		if !s.take() {
			ckf(false, "round %d initial take %d failed", r, i)
		}
	}

	queued := make(chan struct{}, total)
	results := make(chan int, total)
	dones := make([]chan struct{}, total)
	kept := make([]*semNode, total)
	var bad int32

	for id := 0; id < total; id++ {
		dones[id] = make(chan struct{})
		b := seed*100003 + int64(r)*1000 + int64(id)*17
		h := &semNode{v: b, next: &semNode{v: b + 3}}
		kept[id] = h
	}
	var wg sync.WaitGroup
	wg.Add(total)
	for id := 0; id < total; id++ {
		go func(id int) {
			defer wg.Done()
			ok := s.acquire(id, dones[id], queued)
			if !ok {
				results <- -1 - id
				return
			}
			bumpSemMax(atomic.AddInt32(&semAct, 1))
			b := seed*100003 + int64(r)*1000 + int64(id)*17
			h := kept[id]
			if h == nil || h.next == nil || h.v != b || h.next.v != b+3 {
				atomic.AddInt32(&bad, 1)
			}
			atomic.AddInt32(&semAct, -1)
			s.release()
			results <- id
		}(id)
	}

	for i := 0; i < total; i++ {
		<-queued
	}
	// 全部 waiter 排队、名额被 main 占满时 GC。
	gcStress()

	for k := 0; k < canceled; k++ {
		close(dones[k])
	}
	canceledSeen := 0
	for canceledSeen < canceled {
		res := <-results
		if res >= 0 {
			ckf(false, "round %d non-canceled waiter %d returned early", r, res)
			continue
		}
		id := -1 - res
		if id < 0 || id >= canceled {
			ckf(false, "round %d unexpected canceled id %d", r, id)
		}
		canceledSeen++
	}

	s.mu.Lock()
	expectOrder := append([]int(nil), s.order...)
	s.mu.Unlock()

	for i := 0; i < capacity; i++ {
		s.release()
	}
	acquired := 0
	for acquired < total-canceled {
		res := <-results
		if res < 0 {
			ckf(false, "round %d canceled waiter %d granted after release", r, -1-res)
			continue
		}
		acquired++
	}
	wg.Wait()
	close(results)

	s.mu.Lock()
	grants := append([]int(nil), s.grants...)
	free := s.free
	s.mu.Unlock()

	ckf(acquired == total-canceled, "round %d acquired=%d want=%d", r, acquired, total-canceled)
	ckf(len(grants) == total-canceled, "round %d grants=%d want=%d", r, len(grants), total-canceled)
	ckf(len(expectOrder) == total-canceled, "round %d queue order=%d want=%d", r, len(expectOrder), total-canceled)
	if len(grants) == len(expectOrder) {
		for i := range grants {
			ckf(grants[i] == expectOrder[i], "round %d grant[%d]=%d want FIFO %d", r, i, grants[i], expectOrder[i])
		}
	}
	ckf(free == capacity, "round %d free=%d want=%d", r, free, capacity)
	ckf(atomic.LoadInt32(&semMax) <= int32(capacity), "round %d max active=%d cap=%d", r, semMax, capacity)
	ckf(atomic.LoadInt32(&semMax) > 0, "round %d max active never observed", r)
	ckf(atomic.LoadInt32(&bad) == 0, "round %d private chain corrupted", r)

	gcStress()
	for id := 0; id < total; id++ {
		b := seed*100003 + int64(r)*1000 + int64(id)*17
		h := kept[id]
		if h == nil || h.next == nil || h.v != b || h.next.v != b+3 {
			ckf(false, "round %d kept[%d] corrupted after semaphore GC", r, id)
		}
	}
}

func main() {
	seed := envSeed()
	rounds := envInt("TORTURE_ROUNDS", 2, 1, 12)
	waiters := envInt("TORTURE_WAITERS", 12, 4, 32)
	capacity := envInt("TORTURE_CAPACITY", 3, 1, 8)
	canceled := envInt("TORTURE_CANCEL", 2, 1, 6)
	for r := 0; r < rounds; r++ {
		runSemRound(r, seed+int64(r), waiters, capacity, canceled)
	}
	finish("l04_semaphore_release_wake")
}
