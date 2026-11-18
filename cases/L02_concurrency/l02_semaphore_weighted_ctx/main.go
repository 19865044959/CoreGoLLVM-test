// L02: 加权信号量（tailscale syncs.Semaphore 场景）：容量固定、FIFO 等待队列、
// TryAcquire 边界、已取消 ctx 立即失败；持令牌期间 GC，校验令牌 payload。
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
	semCap        = 3
	runWorkers    = 9
	cancelWorkers = 4
)

type token struct {
	seq int
	v   int64
	tag string
}

type waiter struct {
	ch      chan struct{}
	weight  int
	granted bool
}

type sem struct {
	mu      sync.Mutex
	size    int
	cur     int
	peak    int
	waiters []*waiter
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

func newSem(size int) *sem { return &sem{size: size} }

func (s *sem) wakeLocked() {
	for len(s.waiters) > 0 {
		w := s.waiters[0]
		if s.cur+w.weight > s.size {
			break
		}
		s.waiters = s.waiters[1:]
		w.granted = true
		s.cur += w.weight
		if s.cur > s.peak {
			s.peak = s.cur
		}
		close(w.ch)
	}
}

func (s *sem) Acquire(ctx context.Context, weight int) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	if weight <= 0 || weight > s.size {
		s.mu.Unlock()
		return fmt.Errorf("bad weight %d", weight)
	}
	if len(s.waiters) == 0 && s.cur+weight <= s.size {
		s.cur += weight
		if s.cur > s.peak {
			s.peak = s.cur
		}
		s.mu.Unlock()
		return nil
	}
	w := &waiter{ch: make(chan struct{}), weight: weight}
	s.waiters = append(s.waiters, w)
	s.mu.Unlock()

	select {
	case <-w.ch:
		return nil
	case <-ctx.Done():
		s.mu.Lock()
		if w.granted {
			// 令牌与取消同时到达：归还已授予的容量，保持记账平衡。
			s.cur -= w.weight
			s.wakeLocked()
			s.mu.Unlock()
			return ctx.Err()
		}
		for i, x := range s.waiters {
			if x == w {
				s.waiters = append(s.waiters[:i], s.waiters[i+1:]...)
				break
			}
		}
		s.mu.Unlock()
		return ctx.Err()
	}
}

func (s *sem) TryAcquire(weight int) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if weight <= 0 || weight > s.size || len(s.waiters) > 0 || s.cur+weight > s.size {
		return false
	}
	s.cur += weight
	if s.cur > s.peak {
		s.peak = s.cur
	}
	return true
}

func (s *sem) Release(weight int) {
	s.mu.Lock()
	s.cur -= weight
	if s.cur < 0 {
		s.cur = 0
	}
	s.wakeLocked()
	s.mu.Unlock()
}

func (s *sem) numWaiters() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.waiters)
}

func (s *sem) current() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.cur
}

func (s *sem) peakVal() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.peak
}

func waitFor(cond func() bool, msg string) {
	for i := 0; i < 4000000; i++ {
		if cond() {
			return
		}
		runtime.Gosched()
	}
	ck(false, msg)
}

func main() {
	seed := seedVal()
	s := newSem(semCap)

	ck(s.TryAcquire(2), "TryAcquire(2) must succeed on empty semaphore")
	ck(!s.TryAcquire(2), "TryAcquire beyond capacity must fail")
	ck(s.current() == 2, "TryAcquire accounting mismatch")
	s.Release(2)
	ck(s.current() == 0, "Release did not return capacity")

	ctx0, cancel0 := context.WithCancel(context.Background())
	cancel0()
	ck(s.Acquire(ctx0, 1) == context.Canceled, "pre-canceled Acquire must fail with context.Canceled")
	ck(s.current() == 0 && s.numWaiters() == 0, "canceled Acquire consumed capacity or left waiter")

	// 主 goroutine 先占用全部容量，随后提交 13 个等待者。
	for i := 0; i < semCap; i++ {
		ck(s.Acquire(context.Background(), 1) == nil, "main hold acquire failed")
	}

	var done int32
	var canceled int32
	var wg sync.WaitGroup
	runPayloads := make([]*token, runWorkers)
	for i := 0; i < runWorkers; i++ {
		pay := &token{seq: i, v: seed + int64(i)*3, tag: "run"}
		runPayloads[i] = pay
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := s.Acquire(context.Background(), 1); err != nil {
				ck(false, "run worker acquire failed")
				return
			}
			if pay.seq < 0 || pay.v != seed+int64(pay.seq)*3 || pay.tag != "run" {
				ck(false, "run worker payload corrupted before GC")
			}
			gcStress()
			if pay.v != seed+int64(pay.seq)*3 || pay.tag != "run" {
				ck(false, "run worker payload corrupted after GC")
			}
			runtime.Gosched()
			s.Release(1)
			atomic.AddInt32(&done, 1)
		}()
	}

	cancels := make([]context.CancelFunc, 0, cancelWorkers)
	var cwg sync.WaitGroup
	for i := 0; i < cancelWorkers; i++ {
		idx := runWorkers + i
		pay := &token{seq: idx, v: seed + int64(idx)*3, tag: "cancel"}
		runPayloads = append(runPayloads, pay)
		cctx, ccancel := context.WithCancel(context.Background())
		cancels = append(cancels, ccancel)
		cwg.Add(1)
		go func() {
			defer cwg.Done()
			err := s.Acquire(cctx, 1)
			switch err {
			case context.Canceled:
				atomic.AddInt32(&canceled, 1)
			case nil:
				ck(false, "canceled worker unexpectedly acquired a token")
				s.Release(1)
			default:
				ck(false, "unexpected acquire error: "+err.Error())
			}
			if pay.tag != "cancel" || pay.v != seed+int64(pay.seq)*3 {
				ck(false, "canceled worker payload corrupted")
			}
		}()
	}

	waitFor(func() bool { return s.numWaiters() == runWorkers+cancelWorkers },
		"not all workers enqueued as waiters")
	for _, c := range cancels {
		c()
	}
	waitFor(func() bool {
		return s.numWaiters() == runWorkers && atomic.LoadInt32(&canceled) == cancelWorkers
	}, "canceled waiters were not removed from the queue")

	// 逐一归还主 goroutine 持有的容量，唤醒 FIFO 等待者。
	for i := 0; i < semCap; i++ {
		s.Release(1)
	}
	wg.Wait()
	cwg.Wait()

	ck(atomic.LoadInt32(&done) == runWorkers, fmt.Sprintf("completed=%d want=%d", done, runWorkers))
	ck(atomic.LoadInt32(&canceled) == cancelWorkers, fmt.Sprintf("canceled=%d want=%d", canceled, cancelWorkers))
	ck(s.current() == 0, fmt.Sprintf("semaphore leaked capacity: cur=%d", s.current()))
	ck(s.numWaiters() == 0, "waiter queue not empty")
	ck(s.peakVal() == semCap, fmt.Sprintf("peak=%d want=%d", s.peakVal(), semCap))

	gcStress()
	for _, p := range runPayloads {
		if p.v != seed+int64(p.seq)*3 || p.tag == "" {
			ck(false, "payload corrupted by final GC")
		}
	}

	fmt.Printf("capacity=%d workers=%d canceled=%d peak=%d\n", semCap, runWorkers, cancelWorkers, s.peakVal())
	finish("l02_semaphore_weighted_ctx")
}
