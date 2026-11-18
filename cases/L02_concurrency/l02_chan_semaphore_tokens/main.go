// L02: channel 信号量（tailscale Semaphore / kitex writeQuota 场景）：
// 令牌即 channel 元素；Acquire 接收、Release 归还，TryAcquire 走 select/default，
// 已取消 ctx 或取消竞态都不得吞掉令牌。
package main

import (
	"context"
	"fmt"
	"os"
	"runtime"
	"strconv"
	"sync"
	"sync/atomic"
)

const (
	csCap     = 3
	csWorkers = 6
	csIters   = 4
	csCancel  = 4
)

type csPayload struct {
	key int
	val int64
	tag string
}

type csToken struct {
	seq int
	pay *csPayload
}

func (t csToken) valid(seed int64) bool {
	if t.seq < 0 || t.seq >= csCap || t.pay == nil {
		return false
	}
	return t.pay.key == t.seq && t.pay.val == seed+int64(t.seq)*7 && t.pay.tag == "token"
}

type chanSem struct {
	ch       chan csToken
	capacity int
	inFlight int32
	peak     int32
}

func newChanSem(n int, seed int64) *chanSem {
	s := &chanSem{ch: make(chan csToken, n), capacity: n}
	for i := 0; i < n; i++ {
		s.ch <- csToken{seq: i, pay: &csPayload{key: i, val: seed + int64(i)*7, tag: "token"}}
	}
	return s
}

func (s *chanSem) enter() {
	n := atomic.AddInt32(&s.inFlight, 1)
	for {
		p := atomic.LoadInt32(&s.peak)
		if n <= p || atomic.CompareAndSwapInt32(&s.peak, p, n) {
			return
		}
	}
}

func (s *chanSem) acquire(ctx context.Context) (*csToken, error) {
	select {
	case t := <-s.ch:
		if err := ctx.Err(); err != nil {
			s.ch <- t
			return nil, err
		}
		s.enter()
		return &t, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (s *chanSem) tryAcquire() (*csToken, bool) {
	select {
	case t := <-s.ch:
		s.enter()
		return &t, true
	default:
		return nil, false
	}
}

func (s *chanSem) release(t csToken) {
	atomic.AddInt32(&s.inFlight, -1)
	s.ch <- t
}

var (
	failed int32
	gcMu   sync.Mutex
	gcSink [][]byte
)

func ck(ok bool, msg string) {
	if !ok {
		atomic.AddInt32(&failed, 1)
		fmt.Println("FAIL:", msg)
	}
}

func gcStress() {
	gcMu.Lock()
	for i := 0; i < 8; i++ {
		b := make([]byte, 2048)
		b[0] = byte(i)
		gcSink = append(gcSink[:0], b)
	}
	gcSink = gcSink[:0]
	gcMu.Unlock()
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

func seedVal() int64 {
	if s := os.Getenv("UAF_SEED"); s != "" {
		if n, err := strconv.Atoi(s); err == nil {
			return int64(n)
		}
	}
	return 1
}

func checkTryAcquire(seed int64) {
	s := newChanSem(csCap, seed)
	toks := make([]csToken, 0, csCap)
	for i := 0; i < csCap; i++ {
		t, ok := s.tryAcquire()
		if !ok {
			ck(false, "TryAcquire must succeed while tokens remain")
			continue
		}
		toks = append(toks, *t)
	}
	if _, ok := s.tryAcquire(); ok {
		ck(false, "TryAcquire must fail on empty semaphore")
	}
	if n := atomic.LoadInt32(&s.inFlight); n != csCap {
		ck(false, fmt.Sprintf("TryAcquire in-flight=%d want=%d", n, csCap))
	}
	for _, t := range toks {
		if !t.valid(seed) {
			ck(false, "token corrupted across TryAcquire")
		}
		s.release(t)
	}
	if n := atomic.LoadInt32(&s.inFlight); n != 0 {
		ck(false, fmt.Sprintf("in-flight after release=%d want=0", n))
	}
	if n := len(s.ch); n != csCap {
		ck(false, fmt.Sprintf("tokens=%d want=%d", n, csCap))
	}
}

func checkCapConcurrency(seed int64) {
	s := newChanSem(csCap, seed)
	held := make(chan csToken, csCap)
	goRelease := make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < csCap; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			t, err := s.acquire(context.Background())
			if err != nil {
				ck(false, fmt.Sprintf("holder %d acquire failed: %v", i, err))
				return
			}
			if !t.valid(seed) {
				ck(false, fmt.Sprintf("holder %d token corrupted", i))
			}
			held <- *t
			<-goRelease
			s.release(*t)
		}(i)
	}
	for i := 0; i < csCap; i++ {
		t := <-held
		if !t.valid(seed) {
			ck(false, "held token corrupted")
		}
	}
	if n := atomic.LoadInt32(&s.inFlight); n != csCap {
		ck(false, fmt.Sprintf("held in-flight=%d want=%d", n, csCap))
	}
	gcStress()
	close(goRelease)
	wg.Wait()
	if n := atomic.LoadInt32(&s.inFlight); n != 0 {
		ck(false, fmt.Sprintf("holder in-flight after release=%d", n))
	}
	if n := len(s.ch); n != csCap {
		ck(false, fmt.Sprintf("holder tokens=%d want=%d", n, csCap))
	}
	for i := 0; i < csCap; i++ {
		t := <-s.ch
		if !t.valid(seed) {
			ck(false, "token payload corrupted by GC")
		}
	}
}

func checkLoop(seed int64) int32 {
	s := newChanSem(csCap, seed)
	var served int32
	var wg sync.WaitGroup
	for w := 0; w < csWorkers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < csIters; i++ {
				t, err := s.acquire(context.Background())
				if err != nil {
					ck(false, fmt.Sprintf("worker %d acquire failed: %v", w, err))
					return
				}
				if !t.valid(seed) {
					ck(false, fmt.Sprintf("worker %d token corrupted", w))
				}
				runtime.Gosched()
				if (w+i)%3 == 0 {
					gcStress()
					if !t.valid(seed) {
						ck(false, fmt.Sprintf("worker %d token corrupted after GC", w))
					}
				}
				s.release(*t)
				atomic.AddInt32(&served, 1)
			}
		}(w)
	}
	wg.Wait()
	if n := atomic.LoadInt32(&s.inFlight); n != 0 {
		ck(false, fmt.Sprintf("loop in-flight=%d want=0", n))
	}
	if n := len(s.ch); n != csCap {
		ck(false, fmt.Sprintf("loop tokens=%d want=%d", n, csCap))
	}
	for i := 0; i < csCap; i++ {
		t := <-s.ch
		if !t.valid(seed) {
			ck(false, "loop token payload corrupted")
		}
	}
	if p := atomic.LoadInt32(&s.peak); p < 1 || p > csCap {
		ck(false, fmt.Sprintf("loop peak=%d want in [1,%d]", p, csCap))
	}
	return served
}

func checkCanceledWaiters(seed int64) {
	s := newChanSem(csCap, seed)
	drained := make([]csToken, 0, csCap)
	for i := 0; i < csCap; i++ {
		drained = append(drained, <-s.ch)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	started := make(chan struct{}, csCancel)
	var canceled int32
	var wg sync.WaitGroup
	for i := 0; i < csCancel; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			started <- struct{}{}
			if _, err := s.acquire(ctx); err == context.Canceled {
				atomic.AddInt32(&canceled, 1)
			} else {
				ck(false, fmt.Sprintf("cancel waiter %d got err=%v want=context.Canceled", i, err))
			}
		}(i)
	}
	for i := 0; i < csCancel; i++ {
		<-started
	}
	wg.Wait()
	if n := atomic.LoadInt32(&canceled); n != csCancel {
		ck(false, fmt.Sprintf("canceled=%d want=%d", n, csCancel))
	}
	if n := atomic.LoadInt32(&s.inFlight); n != 0 {
		ck(false, fmt.Sprintf("canceled acquire changed in-flight=%d", n))
	}
	for _, t := range drained {
		if !t.valid(seed) {
			ck(false, "drained token corrupted")
		}
		s.ch <- t
	}
	if n := len(s.ch); n != csCap {
		ck(false, fmt.Sprintf("cancel phase tokens=%d want=%d", n, csCap))
	}
}

func main() {
	seed := seedVal()
	checkTryAcquire(seed)
	checkCapConcurrency(seed)
	served := checkLoop(seed)
	if served != csWorkers*csIters {
		ck(false, fmt.Sprintf("served=%d want=%d", served, csWorkers*csIters))
	}
	checkCanceledWaiters(seed)
	gcStress()

	fmt.Printf("capacity=%d workers=%d served=%d canceled=%d\n",
		csCap, csWorkers, csWorkers*csIters, csCancel)
	finish("l02_chan_semaphore_tokens")
}
