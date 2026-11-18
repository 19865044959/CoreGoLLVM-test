// L02: 取消与关闭的竞态：队列满/有空位两种边界下，Close 与共享 ctx 的
// Cancel 同时发生。每个阻塞发送者必须恰好以 closed 或 canceled 之一退出，
// 不得 panic / 双重唤醒；成功进入队列的条目与拒绝条目严格对账。
package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"runtime"
	"strconv"
	"sync"
	"sync/atomic"
)

const (
	rcCap        = 4
	rcSenders    = 8
	rcBaseRounds = 6
	rcMaxTorture = 50
)

var errRCClosed = errors.New("race queue closed")

var (
	failed  int32
	gcMu    sync.Mutex
	gcSink  [][]byte
	curSeed int64
)

type ritem struct {
	id    int
	phase int
	v     int64
}

type raceQueue struct {
	ch     chan *ritem
	done   chan struct{}
	closed atomic.Bool
	once   sync.Once
}

func newRaceQueue(cap int) *raceQueue {
	return &raceQueue{ch: make(chan *ritem, cap), done: make(chan struct{})}
}

func (q *raceQueue) close() {
	q.once.Do(func() {
		q.closed.Store(true)
		close(q.done)
	})
}

func (q *raceQueue) send(ctx context.Context, it *ritem) error {
	if q.closed.Load() {
		return errRCClosed
	}
	select {
	case q.ch <- it:
		return nil
	case <-q.done:
		return errRCClosed
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (q *raceQueue) drain() []*ritem {
	var out []*ritem
	for {
		select {
		case it := <-q.ch:
			out = append(out, it)
		default:
			return out
		}
	}
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

func tortureRounds(def int) int {
	s := os.Getenv("TORTURE_ROUNDS")
	if s == "" {
		return def
	}
	n, err := strconv.Atoi(s)
	if err != nil || n <= 0 {
		return def
	}
	if n > rcMaxTorture {
		n = rcMaxTorture
	}
	return n
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

// closeAndCancel 让 Close 与 Cancel 在两个 goroutine 中同一时刻起跑，
// 制造真实的唤醒竞态；发送者观察到的终态由 select 决定，只要求二选一。
func closeAndCancel(q *raceQueue, cancel context.CancelFunc) {
	gate := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		<-gate
		q.close()
	}()
	go func() {
		defer wg.Done()
		<-gate
		cancel()
	}()
	close(gate)
	wg.Wait()
}

func raceVal(seed int64, phase, id int) int64 {
	return seed*211 + int64(phase)*1000 + int64(id)*19 + 3
}

func main() {
	curSeed = envSeed()
	rounds := tortureRounds(rcBaseRounds)

	// 阶段 A：满队列上所有发送者都不可能成功，Close/Cancel 竞态下只能二选一。
	// 阶段 B：有空位时允许部分发送成功，成功项必须全部出现在排空结果中。
	for r := 0; r < rounds; r++ {
		q := newRaceQueue(rcCap)
		ctx, cancel := context.WithCancel(context.Background())

		pre := make(map[int]bool)
		for i := 0; i < rcCap; i++ {
			it := &ritem{id: 1000 + i, phase: 0, v: raceVal(curSeed, 0, i)}
			q.ch <- it
			pre[it.id] = true
		}

		errs := make([]error, rcSenders)
		var started int32
		var wg sync.WaitGroup
		for i := 0; i < rcSenders; i++ {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				defer func() {
					if p := recover(); p != nil {
						ckSeed(false, "sender panicked during close/cancel race")
						errs[i] = fmt.Errorf("panic: %v", p)
					}
				}()
				it := &ritem{id: i, phase: 0, v: raceVal(curSeed, 0, i)}
				atomic.AddInt32(&started, 1)
				errs[i] = q.send(ctx, it)
			}(i)
		}
		waitFor(func() bool { return atomic.LoadInt32(&started) == rcSenders }, "phase A senders did not start")
		gcStress()
		closeAndCancel(q, cancel)
		wg.Wait()

		aClosed, aCanceled, aSent, aOther := 0, 0, 0, 0
		for _, err := range errs {
			switch {
			case err == nil:
				aSent++
			case errors.Is(err, errRCClosed):
				aClosed++
			case errors.Is(err, context.Canceled):
				aCanceled++
			default:
				aOther++
				ck(false, fmt.Sprintf("phase A unexpected error: %v", err))
			}
		}
		ckSeed(aSent == 0, fmt.Sprintf("phase A sent=%d want=0", aSent))
		ckSeed(aClosed+aCanceled+aSent == rcSenders, "phase A terminal outcome count mismatch")
		ckSeed(aOther == 0, "phase A sender observed non-terminal error")
		itemsA := q.drain()
		ckSeed(len(itemsA) == rcCap, fmt.Sprintf("phase A drained=%d want=%d", len(itemsA), rcCap))
		for _, it := range itemsA {
			if it == nil || !pre[it.id] || it.v != raceVal(curSeed, 0, it.id-1000) {
				ckSeed(false, "phase A drained item corrupted")
			}
		}
		if err := q.send(context.Background(), &ritem{id: 9999, phase: 0, v: 0}); !errors.Is(err, errRCClosed) {
			ck(false, fmt.Sprintf("send after close error=%v want closed", err))
		}

		// 阶段 B：空队列，允许最多 rcCap 个发送者先成功。
		q2 := newRaceQueue(rcCap)
		ctx2, cancel2 := context.WithCancel(context.Background())
		errs2 := make([]error, rcSenders)
		var started2 int32
		var wg2 sync.WaitGroup
		for i := 0; i < rcSenders; i++ {
			wg2.Add(1)
			go func(i int) {
				defer wg2.Done()
				defer func() {
					if p := recover(); p != nil {
						ckSeed(false, "phase B sender panicked")
						errs2[i] = fmt.Errorf("panic: %v", p)
					}
				}()
				it := &ritem{id: i, phase: 1, v: raceVal(curSeed, 1, i)}
				atomic.AddInt32(&started2, 1)
				errs2[i] = q2.send(ctx2, it)
			}(i)
		}
		waitFor(func() bool { return atomic.LoadInt32(&started2) == rcSenders }, "phase B senders did not start")
		waitFor(func() bool { return len(q2.ch) == rcCap }, "phase B queue did not fill")
		gcStress()
		closeAndCancel(q2, cancel2)
		wg2.Wait()

		sentIDs := make(map[int]bool)
		bSent, bClosed, bCanceled, bOther := 0, 0, 0, 0
		for i, err := range errs2 {
			switch {
			case err == nil:
				bSent++
				sentIDs[i] = true
			case errors.Is(err, errRCClosed):
				bClosed++
			case errors.Is(err, context.Canceled):
				bCanceled++
			default:
				bOther++
				ck(false, fmt.Sprintf("phase B unexpected error: %v", err))
			}
		}
		ckSeed(bSent >= 0 && bSent <= rcCap, fmt.Sprintf("phase B sent=%d out of range", bSent))
		ckSeed(bSent+bClosed+bCanceled+bOther == rcSenders, "phase B terminal outcome count mismatch")
		ckSeed(bSent <= rcCap, "phase B accepted more than capacity")
		itemsB := q2.drain()
		ckSeed(len(itemsB) == bSent, fmt.Sprintf("phase B drained=%d want=%d", len(itemsB), bSent))
		seenB := make(map[int]bool)
		for _, it := range itemsB {
			if it == nil || it.phase != 1 || it.id < 0 || it.id >= rcSenders ||
				it.v != raceVal(curSeed, 1, it.id) {
				ckSeed(false, "phase B drained item corrupted")
				continue
			}
			if !sentIDs[it.id] {
				ckSeed(false, "phase B item in queue but sender reported failure")
			}
			if seenB[it.id] {
				ckSeed(false, "phase B duplicate item")
			}
			seenB[it.id] = true
			gcStress()
			if it.v != raceVal(curSeed, 1, it.id) {
				ckSeed(false, "phase B item corrupted after GC")
			}
		}
		if err := q2.send(context.Background(), &ritem{id: 9998, phase: 1, v: 0}); !errors.Is(err, errRCClosed) {
			ck(false, "phase B send after close must be rejected")
		}
	}

	fmt.Printf("rounds=%d capacity=%d senders=%d\n", rounds, rcCap, rcSenders)
	finish("l02_cancel_close_race")
}
