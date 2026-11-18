// L02: 有界队列背压：容量固定，生产者填满后阻塞（可观测 blocked 信号），
// 消费者受 gate 控制后开始排空；队列用 sync.Cond 实现，GC 穿插在队列项上。
package main

import (
	"fmt"
	"os"
	"runtime"
	"strconv"
	"sync"
	"sync/atomic"
)

const (
	qCap      = 4
	baseItems = 12
)

var (
	failed int32
	gcMu   sync.Mutex
	gcSink [][]byte
)

type item struct {
	seq  int
	v    int64
	name string
}

type queue struct {
	mu        sync.Mutex
	cond      *sync.Cond
	buf       []*item
	head      int
	count     int
	cap       int
	closed    bool
	blocked   int32
	maxDepth  int
	blockedCh chan struct{}
}

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

func newQueue(capacity int) *queue {
	q := &queue{
		buf:       make([]*item, capacity),
		cap:       capacity,
		blockedCh: make(chan struct{}, 1),
	}
	q.cond = sync.NewCond(&q.mu)
	return q
}

func (q *queue) push(it *item) {
	q.mu.Lock()
	for q.count == q.cap {
		atomic.AddInt32(&q.blocked, 1)
		select {
		case q.blockedCh <- struct{}{}:
		default:
		}
		q.cond.Wait()
	}
	pos := (q.head + q.count) % q.cap
	q.buf[pos] = it
	q.count++
	if q.count > q.maxDepth {
		q.maxDepth = q.count
	}
	q.cond.Broadcast()
	q.mu.Unlock()
}

func (q *queue) pop() (*item, bool) {
	q.mu.Lock()
	for q.count == 0 && !q.closed {
		q.cond.Wait()
	}
	if q.count == 0 {
		q.mu.Unlock()
		return nil, false
	}
	it := q.buf[q.head]
	q.buf[q.head] = nil
	q.head = (q.head + 1) % q.cap
	q.count--
	q.cond.Broadcast()
	q.mu.Unlock()
	return it, true
}

func (q *queue) close() {
	q.mu.Lock()
	q.closed = true
	q.cond.Broadcast()
	q.mu.Unlock()
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
	if n > 200 {
		n = 200
	}
	return n
}

func expectVal(seq int) int64 { return int64(seq*5 + 3) }

func main() {
	total := baseItems * tortureRounds(1)
	q := newQueue(qCap)
	gate := make(chan struct{})
	prodDone := make(chan struct{})
	consDone := make(chan struct{})
	seen := make([]int32, total)

	go func() {
		for i := 0; i < total; i++ {
			it := &item{seq: i, v: expectVal(i), name: "q"}
			if i%5 == 0 {
				gcStress()
				ck(it.seq == i && it.v == expectVal(i), "producer item corrupted by GC")
			}
			q.push(it)
		}
		q.close()
		close(prodDone)
	}()

	go func() {
		<-gate
		consumed := 0
		var sum int64
		for {
			it, ok := q.pop()
			if !ok {
				break
			}
			if it == nil || it.name != "q" || it.seq != consumed || it.v != expectVal(it.seq) {
				ck(false, "consumer observed invalid or out-of-order item")
				continue
			}
			if !atomic.CompareAndSwapInt32(&seen[it.seq], 0, 1) {
				ck(false, "duplicate item from queue")
			}
			sum += it.v
			consumed++
			if consumed%4 == 0 {
				gcStress()
				if it.seq < 0 || it.v != expectVal(it.seq) || it.name != "q" {
					ck(false, "consumed item corrupted after GC")
				}
			}
		}
		ck(consumed == total, fmt.Sprintf("consumed=%d want=%d", consumed, total))
		var want int64
		for i := 0; i < total; i++ {
			want += expectVal(i)
		}
		ck(sum == want, "queue checksum mismatch")
		close(consDone)
	}()

	select {
	case <-q.blockedCh:
	case <-prodDone:
		ck(false, "producer finished without ever blocking")
	}
	close(gate)
	<-consDone
	<-prodDone

	missing := 0
	for _, v := range seen {
		if v != 1 {
			missing++
		}
	}
	ck(missing == 0, fmt.Sprintf("missing items=%d", missing))
	ck(atomic.LoadInt32(&q.blocked) >= 1, "queue never recorded producer backpressure")
	q.mu.Lock()
	maxDepth := q.maxDepth
	q.mu.Unlock()
	ck(maxDepth == qCap, fmt.Sprintf("max queue depth got=%d want=%d", maxDepth, qCap))

	fmt.Printf("capacity=%d items=%d max_depth=%d\n", qCap, total, maxDepth)
	finish("l02_bounded_queue_backpressure")
}
