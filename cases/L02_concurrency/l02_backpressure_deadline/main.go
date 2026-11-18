// L02: 背压 + 超时：有界队列满时，带已过期 deadline 的 Push 必须确定性失败；
// 带活跃 ctx 的 Push 在消费者释放空位后恰好成功一次，其余取消；空队列上的
// Pop 超时同样确定性返回 DeadlineExceeded。全程用协调而非墙钟阈值。
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
	"time"
)

const (
	bqCap        = 4
	bqK          = 3
	bqRaceK      = 4
	bqTestedPush = bqK + bqK + 1 + bqRaceK
)

var (
	failed  int32
	gcMu    sync.Mutex
	gcSink  [][]byte
	curSeed int64
)

type bitem struct {
	id    int
	phase int
	v     int64
}

type deadlineQueue struct {
	ch       chan *bitem
	enqueued atomic.Int64
	popped   atomic.Int64
	resident atomic.Int64
}

func newDeadlineQueue(cap int) *deadlineQueue {
	return &deadlineQueue{ch: make(chan *bitem, cap)}
}

func (q *deadlineQueue) push(ctx context.Context, it *bitem) error {
	select {
	case q.ch <- it:
		q.resident.Add(1)
		q.enqueued.Add(1)
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (q *deadlineQueue) pop(ctx context.Context) (*bitem, error) {
	select {
	case it := <-q.ch:
		q.resident.Add(-1)
		q.popped.Add(1)
		return it, nil
	case <-ctx.Done():
		return nil, ctx.Err()
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

func waitFor(cond func() bool, msg string) {
	for i := 0; i < 4000000; i++ {
		if cond() {
			return
		}
		runtime.Gosched()
	}
	ck(false, msg)
}

func bqVal(seed int64, phase, id int) int64 {
	return seed*307 + int64(phase)*10000 + int64(id)*23 + 7
}

func itemOK(it *bitem) bool {
	if it == nil {
		return false
	}
	switch it.phase {
	case 0:
		return it.id >= 0 && it.id < bqCap && it.v == bqVal(curSeed, 0, it.id)
	case 2:
		i := it.id - 100
		return i >= 0 && i < bqK && it.v == bqVal(curSeed, 2, i)
	case 3:
		return it.id == 900 && it.v == bqVal(curSeed, 3, 0)
	case 4:
		return it.id >= 0 && it.v == bqVal(curSeed, 4, it.id)
	}
	return false
}

func main() {
	curSeed = envSeed()
	q := newDeadlineQueue(bqCap)

	// 阶段一：满队列 + 已过期 deadline，Push 只能观察到 ctx。
	expired, cancelExpired := context.WithDeadline(context.Background(), time.Unix(0, 0))
	defer cancelExpired()
	if expired.Err() != context.DeadlineExceeded {
		ck(false, "expired context did not report DeadlineExceeded")
	}
	for i := 0; i < bqCap; i++ {
		if err := q.push(context.Background(), &bitem{id: i, phase: 0, v: bqVal(curSeed, 0, i)}); err != nil {
			ck(false, "prefill push failed")
		}
	}
	var expiredPush int32
	var epWG sync.WaitGroup
	for i := 0; i < bqK; i++ {
		epWG.Add(1)
		go func(i int) {
			defer epWG.Done()
			err := q.push(expired, &bitem{id: i, phase: 1, v: bqVal(curSeed, 1, i)})
			if errors.Is(err, context.DeadlineExceeded) {
				atomic.AddInt32(&expiredPush, 1)
			} else {
				ckSeed(false, fmt.Sprintf("full-queue expired push error=%v", err))
			}
		}(i)
	}
	epWG.Wait()
	ck(atomic.LoadInt32(&expiredPush) == bqK, fmt.Sprintf("expired pushes=%d want=%d", expiredPush, bqK))
	ck(q.resident.Load() == bqCap, "resident changed on expired pushes")
	ck(len(q.ch) == bqCap, "queue depth changed on expired pushes")

	// 阶段二：满队列 + 活跃 ctx 阻塞；释放一个槽位后恰好一个成功，其余取消。
	liveBase, liveCancelBase := context.WithCancel(context.Background())
	live, liveCancel := context.WithTimeout(liveBase, time.Hour)
	defer liveCancel()
	var started int32
	var accepted int32
	var canceled int32
	var lpWG sync.WaitGroup
	for i := 0; i < bqK; i++ {
		lpWG.Add(1)
		go func(i int) {
			defer lpWG.Done()
			it := &bitem{id: 100 + i, phase: 2, v: bqVal(curSeed, 2, i)}
			atomic.AddInt32(&started, 1)
			err := q.push(live, it)
			switch {
			case err == nil:
				atomic.AddInt32(&accepted, 1)
			case errors.Is(err, context.Canceled):
				atomic.AddInt32(&canceled, 1)
			default:
				ckSeed(false, fmt.Sprintf("blocked push error=%v", err))
			}
		}(i)
	}
	waitFor(func() bool { return atomic.LoadInt32(&started) == bqK }, "live producers did not start")
	first, err := q.pop(context.Background())
	if err != nil || !itemOK(first) || first.phase != 0 {
		ckSeed(false, "pop to relieve backpressure failed")
	}
	waitFor(func() bool { return atomic.LoadInt32(&accepted) == 1 }, "no blocked producer filled the freed slot")
	liveCancelBase()
	lpWG.Wait()
	ck(atomic.LoadInt32(&accepted) == 1, fmt.Sprintf("accepted after relief=%d want=1", accepted))
	ck(atomic.LoadInt32(&canceled) == bqK-1, fmt.Sprintf("canceled after relief=%d want=%d", canceled, bqK-1))
	ck(q.resident.Load() == bqCap, fmt.Sprintf("resident=%d want=%d after relief", q.resident.Load(), bqCap))

	// 阶段三：独立空队列验证 Pop 超时与活跃 Pop 的唤醒。
	qe := newDeadlineQueue(bqCap)
	if _, err := qe.pop(expired); !errors.Is(err, context.DeadlineExceeded) {
		ck(false, fmt.Sprintf("empty-queue expired pop error=%v", err))
	}
	popCtx, popCancel := context.WithCancel(context.Background())
	var popStarted int32
	var popErr error
	var popped *bitem
	var ppWG sync.WaitGroup
	ppWG.Add(1)
	go func() {
		defer ppWG.Done()
		atomic.AddInt32(&popStarted, 1)
		popped, popErr = qe.pop(popCtx)
	}()
	waitFor(func() bool { return atomic.LoadInt32(&popStarted) == 1 }, "live consumer did not start")
	newItem := &bitem{id: 900, phase: 3, v: bqVal(curSeed, 3, 0)}
	if err := qe.push(context.Background(), newItem); err != nil {
		ck(false, "push to blocked consumer failed")
	}
	ppWG.Wait()
	popCancel()
	if popErr != nil || !itemOK(popped) || popped.phase != 3 {
		ckSeed(false, "live consumer did not receive pushed item")
	}

	// 阶段四：满队列 + 取消竞态，所有阻塞 Push 必须取消，不得入队。
	raceCtx, raceCancel := context.WithCancel(context.Background())
	var raceStarted, raceCanceled int32
	var rpWG sync.WaitGroup
	for i := 0; i < bqRaceK; i++ {
		rpWG.Add(1)
		go func(i int) {
			defer rpWG.Done()
			atomic.AddInt32(&raceStarted, 1)
			err := q.push(raceCtx, &bitem{id: 200 + i, phase: 5, v: bqVal(curSeed, 5, i)})
			if errors.Is(err, context.Canceled) {
				atomic.AddInt32(&raceCanceled, 1)
			} else {
				ckSeed(false, fmt.Sprintf("race push error=%v", err))
			}
		}(i)
	}
	waitFor(func() bool { return atomic.LoadInt32(&raceStarted) == bqRaceK }, "race producers did not start")
	raceCancel()
	rpWG.Wait()
	ck(atomic.LoadInt32(&raceCanceled) == bqRaceK, fmt.Sprintf("race canceled=%d want=%d", raceCanceled, bqRaceK))
	ck(q.resident.Load() == bqCap, "race phase changed queue occupancy")

	// 收尾对账：enqueued == drained + popped，所有条目唯一且载荷正确。
	drained := 0
	seen := make(map[[2]int]bool)
drainLoop:
	for {
		select {
		case it := <-q.ch:
			q.resident.Add(-1)
			if !itemOK(it) || it.phase == 5 {
				ckSeed(false, "drained item invalid")
			}
			tok := [2]int{it.phase, it.id}
			if seen[tok] {
				ckSeed(false, "drained duplicate item")
			}
			seen[tok] = true
			drained++
			if drained%3 == 0 {
				gcStress()
			}
		default:
			break drainLoop
		}
	}
	poppedCount := int(q.popped.Load())
	enq := q.enqueued.Load()
	if int64(drained+poppedCount) != enq {
		ckSeed(false, fmt.Sprintf("accounting drained=%d popped=%d enqueued=%d", drained, poppedCount, enq))
	}
	ck(q.resident.Load() == 0, fmt.Sprintf("resident=%d want=0", q.resident.Load()))
	ck(q.popped.Load() == 1, fmt.Sprintf("popped=%d want=1", q.popped.Load()))
	ck(drained == bqCap, fmt.Sprintf("drained=%d want=%d", drained, bqCap))

	gcStress()
	if !itemOK(first) {
		ckSeed(false, "relieving pop item corrupted by GC")
	}
	if !itemOK(popped) {
		ckSeed(false, "live-popped item corrupted by GC")
	}

	fmt.Printf("capacity=%d push_attempts=%d accepted=2 push_expired=3 canceled=6 pop_expired=1 drained=%d\n",
		bqCap, bqTestedPush, drained)
	finish("l02_backpressure_deadline")
}
