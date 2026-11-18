// L02: 混合锁 + channel 通知：互斥锁保护 FIFO 缓冲，channel 只做唤醒
// （容量 1 的边沿通知，允许合并）。验证空/满边界、关闭唤醒、通知丢失后
// 消费者重查、并发生产/消费不漏项且全局入队序可还原。
package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"runtime"
	"sort"
	"strconv"
	"sync"
	"sync/atomic"
)

const (
	hWakeConsumers = 4
	hWakeItems     = 4
	hProducers     = 4
	hPerProducer   = 30
	hConsumers     = 3
)

var errHQClosed = errors.New("hybrid queue closed")

var (
	failed  int32
	gcMu    sync.Mutex
	gcSink  [][]byte
	curSeed int64
)

type hitem struct {
	order    int
	producer int
	seq      int
	v        int64
	tag      int
}

type hybridQueue struct {
	mu     sync.Mutex
	buf    []*hitem
	next   int
	closed bool
	notify chan struct{}
	done   chan struct{}
	once   sync.Once
}

func newHybridQueue() *hybridQueue {
	return &hybridQueue{
		notify: make(chan struct{}, 1),
		done:   make(chan struct{}),
	}
}

func (q *hybridQueue) push(it *hitem) error {
	q.mu.Lock()
	if q.closed {
		q.mu.Unlock()
		return errHQClosed
	}
	it.order = q.next
	q.next++
	q.buf = append(q.buf, it)
	q.mu.Unlock()
	select {
	case q.notify <- struct{}{}:
	default: // 通知可合并，消费者每次唤醒后必须重新检查队列。
	}
	return nil
}

func (q *hybridQueue) pop(ctx context.Context) (*hitem, error) {
	for {
		q.mu.Lock()
		if len(q.buf) > 0 {
			it := q.buf[0]
			q.buf[0] = nil
			q.buf = q.buf[1:]
			q.mu.Unlock()
			return it, nil
		}
		closed := q.closed
		q.mu.Unlock()
		if closed {
			return nil, errHQClosed
		}
		select {
		case <-q.notify:
		case <-q.done:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
}

func (q *hybridQueue) close() {
	q.once.Do(func() {
		q.mu.Lock()
		q.closed = true
		q.mu.Unlock()
		close(q.done)
	})
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

func itemVal(seed int64, producer, seq int) int64 {
	return seed*97 + int64(producer)*1000 + int64(seq)*13 + 5
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
	curSeed = envSeed()

	// 边界一：空队列上的已取消 Pop 必须立即返回 ctx 错误。
	q0 := newHybridQueue()
	cctx, ccancel := context.WithCancel(context.Background())
	ccancel()
	if _, err := q0.pop(cctx); !errors.Is(err, context.Canceled) {
		ck(false, fmt.Sprintf("canceled pop error=%v want canceled", err))
	}
	// 边界二：关闭后 Push 拒绝、残留项仍可排空、排空后 Pop 返回 closed。
	if err := q0.push(&hitem{producer: 0, seq: 0, v: 1, tag: 1}); err != nil {
		ck(false, "push before close failed")
	}
	q0.close()
	q0.close() // 幂等
	if err := q0.push(&hitem{producer: 0, seq: 1, v: 2, tag: 1}); !errors.Is(err, errHQClosed) {
		ck(false, fmt.Sprintf("push after close error=%v want closed", err))
	}
	if it, err := q0.pop(context.Background()); err != nil || it == nil || it.seq != 0 {
		ck(false, "residual item not drained after close")
	}
	if _, err := q0.pop(context.Background()); !errors.Is(err, errHQClosed) {
		ck(false, fmt.Sprintf("pop after drain error=%v want closed", err))
	}

	// 阶段一：多个消费者阻塞在空队列，单条通知被合并后仍必须全部消费。
	q1 := newHybridQueue()
	got := make(chan *hitem, hWakeItems)
	var started int32
	var consumed int32
	var cwg sync.WaitGroup
	for c := 0; c < hWakeConsumers; c++ {
		cwg.Add(1)
		go func() {
			defer cwg.Done()
			atomic.AddInt32(&started, 1)
			for {
				it, err := q1.pop(context.Background())
				if err != nil {
					if !errors.Is(err, errHQClosed) {
						ck(false, "blocked consumer got unexpected error")
					}
					return
				}
				got <- it
				atomic.AddInt32(&consumed, 1)
			}
		}()
	}
	waitFor(func() bool { return atomic.LoadInt32(&started) == hWakeConsumers }, "consumers did not start")
	for i := 0; i < hWakeItems; i++ {
		if err := q1.push(&hitem{producer: 7, seq: i, v: itemVal(curSeed, 7, i), tag: 1}); err != nil {
			ck(false, "wake phase push failed")
		}
	}
	waitFor(func() bool { return atomic.LoadInt32(&consumed) == hWakeItems }, "coalesced notification lost an item")
	q1.close()
	cwg.Wait()
	close(got)
	woken := 0
	seenWake := make(map[int]bool)
	for it := range got {
		if it == nil || it.producer != 7 || it.seq < 0 || it.seq >= hWakeItems ||
			it.v != itemVal(curSeed, 7, it.seq) || it.tag != 1 {
			ckSeed(false, "woken item corrupted")
			continue
		}
		if seenWake[it.seq] {
			ck(false, "woken item duplicated")
		}
		seenWake[it.seq] = true
		woken++
	}
	ck(woken == hWakeItems, fmt.Sprintf("woken items=%d want=%d", woken, hWakeItems))

	// 阶段二：并发生产者/消费者；order 在锁内分配，事后按键还原每条生产者的 FIFO。
	q2 := newHybridQueue()
	results := make(chan *hitem, hProducers*hPerProducer)
	var pwg, cwg2 sync.WaitGroup
	for p := 0; p < hProducers; p++ {
		pwg.Add(1)
		go func(p int) {
			defer pwg.Done()
			for s := 0; s < hPerProducer; s++ {
				it := &hitem{producer: p, seq: s, v: itemVal(curSeed, p, s), tag: 2}
				if s%17 == 0 {
					gcStress()
					if it.v != itemVal(curSeed, p, s) {
						ckSeed(false, "producer item corrupted by GC")
					}
				}
				if err := q2.push(it); err != nil {
					ck(false, "concurrent push failed")
					return
				}
			}
		}(p)
	}
	for c := 0; c < hConsumers; c++ {
		cwg2.Add(1)
		go func() {
			defer cwg2.Done()
			for {
				it, err := q2.pop(context.Background())
				if err != nil {
					if !errors.Is(err, errHQClosed) {
						ck(false, "consumer got unexpected error")
					}
					return
				}
				results <- it
			}
		}()
	}
	pwg.Wait()
	q2.close()
	cwg2.Wait()
	close(results)

	all := make([]*hitem, 0, hProducers*hPerProducer)
	seen := make([][]bool, hProducers)
	for i := range seen {
		seen[i] = make([]bool, hPerProducer)
	}
	for it := range results {
		if it == nil || it.producer < 0 || it.producer >= hProducers ||
			it.seq < 0 || it.seq >= hPerProducer {
			ck(false, "consumer received out-of-range item")
			continue
		}
		if seen[it.producer][it.seq] {
			ck(false, fmt.Sprintf("duplicate item p=%d seq=%d", it.producer, it.seq))
		}
		seen[it.producer][it.seq] = true
		if it.v != itemVal(curSeed, it.producer, it.seq) || it.tag != 2 {
			ckSeed(false, "consumed item corrupted")
		}
		all = append(all, it)
	}
	if len(all) != hProducers*hPerProducer {
		ck(false, fmt.Sprintf("consumed=%d want=%d", len(all), hProducers*hPerProducer))
	}
	sort.Slice(all, func(i, j int) bool { return all[i].order < all[j].order })
	for i := 1; i < len(all); i++ {
		if all[i].order == all[i-1].order {
			ck(false, "duplicate queue order")
		}
	}
	last := make([]int, hProducers)
	for i := range last {
		last[i] = -1
	}
	for _, it := range all {
		if it.seq != last[it.producer]+1 {
			ck(false, fmt.Sprintf("producer %d FIFO broken: seq=%d last=%d", it.producer, it.seq, last[it.producer]))
		}
		last[it.producer] = it.seq
	}
	missing := 0
	for p := 0; p < hProducers; p++ {
		for s := 0; s < hPerProducer; s++ {
			if !seen[p][s] {
				missing++
			}
		}
	}
	ck(missing == 0, fmt.Sprintf("missing items=%d", missing))

	gcStress()
	if len(all) > 0 {
		it := all[len(all)-1]
		if it.v != itemVal(curSeed, it.producer, it.seq) {
			ck(false, "item corrupted by final GC")
		}
	}

	fmt.Printf("wake_consumers=%d wake_items=%d producers=%d items=%d consumers=%d\n",
		hWakeConsumers, hWakeItems, hProducers, hProducers*hPerProducer, hConsumers)
	finish("l02_lock_chan_hybrid")
}
