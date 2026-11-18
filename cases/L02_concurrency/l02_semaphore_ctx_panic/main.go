// L02: 信号量 + context + panic 安全释放：容量固定的 channel 信号量，
// 等待者被取消时不得漏/多占令牌；持有者 panic 时 defer 必须归还令牌并
// recover。校验并发峰值不超过容量、最终令牌与计数完全归零。
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
	csCap        = 4
	csWaiters    = 3
	csChurn      = 8
	csChurnIters = 30
)

var (
	failed  int32
	gcMu    sync.Mutex
	gcSink  [][]byte
	curSeed int64
)

type csem struct {
	tokens   chan struct{}
	inFlight atomic.Int32
	peak     atomic.Int32
	acquired atomic.Int64
	released atomic.Int64
}

func newCSem(n int) *csem { return &csem{tokens: make(chan struct{}, n)} }

func (s *csem) notePeak(n int32) {
	for {
		p := s.peak.Load()
		if n <= p || s.peak.CompareAndSwap(p, n) {
			return
		}
	}
}

func (s *csem) tryAcquire() bool {
	select {
	case s.tokens <- struct{}{}:
		n := s.inFlight.Add(1)
		s.notePeak(n)
		s.acquired.Add(1)
		return true
	default:
		return false
	}
}

func (s *csem) acquire(ctx context.Context) error {
	select {
	case s.tokens <- struct{}{}:
		n := s.inFlight.Add(1)
		s.notePeak(n)
		s.acquired.Add(1)
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (s *csem) release() {
	// 先减在途计数再归还令牌：若先归还，新的 Acquire 可能先发送并加计数，
	// 导致 in-flight 观测值瞬时超过容量。
	s.inFlight.Add(-1)
	<-s.tokens
	s.released.Add(1)
}

func ck(ok bool, msg string) {
	if !ok {
		atomic.AddInt32(&failed, 1)
		fmt.Println("FAIL:", msg)
	}
}

func ckSeed(ok bool, msg string) {
	if !ok {
		atomic.AddInt32(&failed, 1)
		fmt.Printf("FAIL: %s (seed=%d)\n", msg, curSeed)
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

func waitFor(cond func() bool, msg string) {
	for i := 0; i < 4000000; i++ {
		if cond() {
			return
		}
		runtime.Gosched()
	}
	ck(false, msg)
}

// holdAndMaybePanic 在 panic 路径上先归还令牌再 recover，验证 defer 顺序。
func (s *csem) holdAndMaybePanic(i int, panics *int32) {
	defer func() {
		s.release()
		if r := recover(); r != nil {
			atomic.AddInt32(panics, 1)
		}
	}()
	if i%5 == 0 {
		gcStress()
	}
	if i%7 == 3 {
		panic("holder panic")
	}
}

func main() {
	curSeed = envSeed()
	s := newCSem(csCap)

	// 边界：占满后 TryAcquire 失败、已取消 ctx 立即失败、峰值恰为容量。
	for i := 0; i < csCap; i++ {
		if !s.tryAcquire() {
			ck(false, "tryAcquire on empty semaphore failed")
		}
	}
	if s.tryAcquire() {
		ck(false, "tryAcquire beyond capacity must fail")
	}
	cctx, ccancel := context.WithCancel(context.Background())
	ccancel()
	if err := s.acquire(cctx); err != context.Canceled {
		ck(false, fmt.Sprintf("pre-canceled acquire error=%v want canceled", err))
	}
	if s.peak.Load() != csCap {
		ck(false, fmt.Sprintf("peak=%d want=%d", s.peak.Load(), csCap))
	}
	ck(s.inFlight.Load() == csCap, "in-flight accounting mismatch at capacity")
	for i := 0; i < csCap; i++ {
		s.release()
	}
	ck(len(s.tokens) == 0 && s.inFlight.Load() == 0, "capacity leaked after boundary release")

	// 取消竞态：释放一个令牌后恰好一个等待者持有，其余取消。
	for i := 0; i < csCap; i++ {
		if !s.tryAcquire() {
			ck(false, "refill hold failed")
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	holdRelease := make(chan struct{})
	var started, accepted, canceled int32
	var wg sync.WaitGroup
	for i := 0; i < csWaiters; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			atomic.AddInt32(&started, 1)
			err := s.acquire(ctx)
			switch {
			case err == nil:
				atomic.AddInt32(&accepted, 1)
				<-holdRelease
				s.release()
			case err == context.Canceled:
				atomic.AddInt32(&canceled, 1)
			default:
				ck(false, fmt.Sprintf("waiter unexpected error=%v", err))
			}
		}(i)
	}
	waitFor(func() bool { return atomic.LoadInt32(&started) == csWaiters }, "waiters did not start")
	s.release() // 归还主 goroutine 的一个令牌
	waitFor(func() bool { return atomic.LoadInt32(&accepted) == 1 }, "no waiter acquired the released token")
	cancel()
	// 等全部取消者退出 select 后再让持有者归还令牌，避免“取消已就绪 + 新槽位”
	// 同时可选中导致取消者反而获取令牌。
	waitFor(func() bool { return atomic.LoadInt32(&canceled) == csWaiters-1 }, "canceled waiters did not converge")
	close(holdRelease)
	wg.Wait()
	ck(atomic.LoadInt32(&accepted) == 1, fmt.Sprintf("accepted=%d want=1", accepted))
	ck(atomic.LoadInt32(&canceled) == csWaiters-1, fmt.Sprintf("canceled=%d want=%d", canceled, csWaiters-1))
	for i := 0; i < csCap-1; i++ {
		s.release()
	}
	ck(len(s.tokens) == 0 && s.inFlight.Load() == 0, "capacity leaked after cancel race")

	// panic 路径：持有者 panic，defer 必须先归还令牌再 recover。
	var panics int32
	var pwg sync.WaitGroup
	for i := 0; i < csCap; i++ {
		pwg.Add(1)
		go func(i int) {
			defer pwg.Done()
			if err := s.acquire(context.Background()); err != nil {
				ck(false, "panic holder acquire failed")
				return
			}
			s.holdAndMaybePanic(3, &panics) // i%7==3，必 panic
		}(i)
	}
	pwg.Wait()
	ck(atomic.LoadInt32(&panics) == csCap, fmt.Sprintf("panic recovered=%d want=%d", panics, csCap))
	ck(len(s.tokens) == 0 && s.inFlight.Load() == 0, "token leaked on panic path")

	// 并发churn：每 7 次触发一次 panic，其余正常释放。
	baseAcq, baseRel := s.acquired.Load(), s.released.Load()
	var churnPanics int32
	var cwg sync.WaitGroup
	for w := 0; w < csChurn; w++ {
		cwg.Add(1)
		go func(w int) {
			defer cwg.Done()
			for i := 0; i < csChurnIters; i++ {
				if err := s.acquire(context.Background()); err != nil {
					ck(false, "churn acquire failed")
					return
				}
				s.holdAndMaybePanic(i, &churnPanics)
			}
		}(w)
	}
	cwg.Wait()
	// 0..29 中 i%7==3 为 3,10,17,24，共 4 次/worker。
	expectPanics := int32(csChurn * 4)
	ck(atomic.LoadInt32(&churnPanics) == expectPanics,
		fmt.Sprintf("churn panics=%d want=%d", churnPanics, expectPanics))
	totalPanics := atomic.LoadInt32(&panics) + atomic.LoadInt32(&churnPanics)
	ck(totalPanics == csCap+expectPanics, "total panic count mismatch")
	ck(len(s.tokens) == 0 && s.inFlight.Load() == 0, "token leaked after churn")
	ck(s.peak.Load() <= csCap, fmt.Sprintf("peak=%d exceeds capacity", s.peak.Load()))
	ck(s.acquired.Load()-baseAcq == s.released.Load()-baseRel, "acquire/release imbalance in churn")
	ck(s.acquired.Load() == s.released.Load(), "acquire/release imbalance overall")

	gcStress()
	ck(len(s.tokens) == 0 && s.inFlight.Load() == 0, "token leaked after final GC")

	fmt.Printf("capacity=%d canceled_waiters=%d panics=%d churn_ops=%d\n",
		csCap, canceled, totalPanics, csChurn*csChurnIters)
	finish("l02_semaphore_ctx_panic")
}
