// L02: 有界队列 + 取消（etcd rafthttp pipeline 与 quic-go 等待者取消场景）：
// 队列满/空时阻塞的 Push/Pop 必须可被各自 ctx 取消或统一 Close 唤醒，
// 关闭后先排空残留再拒绝新操作；多轮循环放大而不依赖墙钟。
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

const qCap = 4

var errQueueClosed = errors.New("queue closed")

type item struct {
	seq int
	v   int64
	tag string
}

type cqueue struct {
	ch     chan *item
	done   chan struct{}
	closed atomic.Bool
	once   sync.Once
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

func tortureRounds(def int) int {
	s := os.Getenv("TORTURE_ROUNDS")
	if s == "" {
		return def
	}
	n, err := strconv.Atoi(s)
	if err != nil || n <= 0 {
		return def
	}
	if n > 50 {
		n = 50
	}
	return n
}

func newCQueue(capacity int) *cqueue {
	return &cqueue{ch: make(chan *item, capacity), done: make(chan struct{})}
}

func (q *cqueue) Close() {
	q.once.Do(func() {
		q.closed.Store(true)
		close(q.done)
	})
}

func (q *cqueue) Push(ctx context.Context, it *item) error {
	if q.closed.Load() {
		return errQueueClosed
	}
	select {
	case q.ch <- it:
		return nil
	case <-q.done:
		return errQueueClosed
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (q *cqueue) Pop(ctx context.Context) (*item, error) {
	if q.closed.Load() {
		select {
		case it := <-q.ch:
			return it, nil
		default:
			return nil, errQueueClosed
		}
	}
	select {
	case it := <-q.ch:
		return it, nil
	case <-q.done:
		select {
		case it := <-q.ch:
			return it, nil
		default:
			return nil, errQueueClosed
		}
	case <-ctx.Done():
		return nil, ctx.Err()
	}
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

func expect(seed int64, seq int) int64 { return seed*17 + int64(seq)*5 }

func fill(q *cqueue, seed int64, n int) {
	for i := 0; i < n; i++ {
		err := q.Push(context.Background(), &item{seq: i, v: expect(seed, i), tag: "fill"})
		ck(err == nil, "prefill push failed")
	}
}

func canceledPush(q *cqueue, n int, seed int64) int {
	ctxs := make([]context.Context, n)
	cancels := make([]context.CancelFunc, n)
	errs := make([]error, n)
	var started int32
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		ctxs[i], cancels[i] = context.WithCancel(context.Background())
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			it := &item{seq: 200 + i, v: expect(seed, 200+i), tag: "prod"}
			atomic.AddInt32(&started, 1)
			errs[i] = q.Push(ctxs[i], it)
		}(i)
	}
	waitFor(func() bool { return atomic.LoadInt32(&started) == int32(n) }, "producers did not start")
	for _, c := range cancels {
		c()
	}
	wg.Wait()
	cnt := 0
	for _, err := range errs {
		if errors.Is(err, context.Canceled) {
			cnt++
		} else {
			ck(false, fmt.Sprintf("blocked producer error=%v want canceled", err))
		}
	}
	return cnt
}

func canceledPop(q *cqueue, n int) int {
	ctxs := make([]context.Context, n)
	cancels := make([]context.CancelFunc, n)
	errs := make([]error, n)
	var started int32
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		ctxs[i], cancels[i] = context.WithCancel(context.Background())
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			atomic.AddInt32(&started, 1)
			_, errs[i] = q.Pop(ctxs[i])
		}(i)
	}
	waitFor(func() bool { return atomic.LoadInt32(&started) == int32(n) }, "consumers did not start")
	for _, c := range cancels {
		c()
	}
	wg.Wait()
	cnt := 0
	for _, err := range errs {
		if errors.Is(err, context.Canceled) {
			cnt++
		} else {
			ck(false, fmt.Sprintf("blocked consumer error=%v want canceled", err))
		}
	}
	return cnt
}

func closedPushes(q *cqueue, n int, seed int64) int {
	errs := make([]error, n)
	var started int32
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			it := &item{seq: 300 + i, v: expect(seed, 300+i), tag: "closed"}
			atomic.AddInt32(&started, 1)
			errs[i] = q.Push(context.Background(), it)
		}(i)
	}
	waitFor(func() bool { return atomic.LoadInt32(&started) == int32(n) }, "close producers did not start")
	q.Close()
	wg.Wait()
	cnt := 0
	for _, err := range errs {
		if errors.Is(err, errQueueClosed) {
			cnt++
		} else {
			ck(false, fmt.Sprintf("closed producer error=%v want closed", err))
		}
	}
	return cnt
}

func closedPops(q *cqueue, n int) int {
	errs := make([]error, n)
	var started int32
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			atomic.AddInt32(&started, 1)
			_, errs[i] = q.Pop(context.Background())
		}(i)
	}
	waitFor(func() bool { return atomic.LoadInt32(&started) == int32(n) }, "close consumers did not start")
	q.Close()
	wg.Wait()
	cnt := 0
	for _, err := range errs {
		if errors.Is(err, errQueueClosed) {
			cnt++
		} else {
			ck(false, fmt.Sprintf("closed consumer error=%v want closed", err))
		}
	}
	return cnt
}

func main() {
	seed := seedVal()
	rounds := tortureRounds(2)
	var canceledProducers, canceledConsumers, closedProducers, closedConsumers, drained int

	for r := 0; r < rounds; r++ {
		// 阶段一：满队列上阻塞的生产者被 ctx 取消。
		q1 := newCQueue(qCap)
		fill(q1, seed+int64(r), qCap)
		gcStress()
		canceledProducers += canceledPush(q1, 3, seed+int64(r))

		// 阶段二：空队列上阻塞的消费者被 ctx 取消。
		q2 := newCQueue(qCap)
		canceledConsumers += canceledPop(q2, 3)

		// 阶段三：Close 唤醒阻塞生产者，残留项仍可排空。
		q3 := newCQueue(qCap)
		fill(q3, seed+int64(r), qCap)
		gcStress()
		closedProducers += closedPushes(q3, 3, seed+int64(r))
		for i := 0; i < qCap; i++ {
			it, err := q3.Pop(context.Background())
			if err != nil || it == nil {
				ck(false, fmt.Sprintf("drain pop %d failed: %v", i, err))
				continue
			}
			if it.seq != i || it.v != expect(seed+int64(r), i) || it.tag != "fill" {
				ck(false, "drained item corrupted")
			}
			gcStress()
			if it.v != expect(seed+int64(r), it.seq) {
				ck(false, "drained item corrupted by GC")
			}
			drained++
		}
		if _, err := q3.Pop(context.Background()); !errors.Is(err, errQueueClosed) {
			ck(false, fmt.Sprintf("drained queue error=%v want closed", err))
		}
		err := q3.Push(context.Background(), &item{seq: 999, v: 0, tag: "late"})
		ck(errors.Is(err, errQueueClosed), "push after close must be rejected")

		// 阶段四：Close 唤醒空队列上阻塞的消费者。
		q4 := newCQueue(qCap)
		closedConsumers += closedPops(q4, 3)

		gcStress()
	}

	ck(canceledProducers == 3*rounds, fmt.Sprintf("canceled producers=%d want=%d", canceledProducers, 3*rounds))
	ck(canceledConsumers == 3*rounds, fmt.Sprintf("canceled consumers=%d want=%d", canceledConsumers, 3*rounds))
	ck(closedProducers == 3*rounds, fmt.Sprintf("closed producers=%d want=%d", closedProducers, 3*rounds))
	ck(closedConsumers == 3*rounds, fmt.Sprintf("closed consumers=%d want=%d", closedConsumers, 3*rounds))
	ck(drained == qCap*rounds, fmt.Sprintf("drained=%d want=%d", drained, qCap*rounds))

	fmt.Printf("rounds=%d canceled_producers=%d canceled_consumers=%d closed_producers=%d closed_consumers=%d drained=%d\n",
		rounds, canceledProducers, canceledConsumers, closedProducers, closedConsumers, drained)
	finish("l02_bounded_queue_cancel")
}
