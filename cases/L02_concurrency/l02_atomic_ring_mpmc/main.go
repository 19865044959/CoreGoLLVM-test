// L02: 原子环形队列计数（quic-go ringbuffer / grpc-go profiling buffer 场景）：
// 在固定容量 MPMC 环上并发存取，序号原子发布；多生产者/多消费者校验不丢不重、
// 逐生产者校验和、原子入/出队计数守恒；GC 穿插在槽位指针 payload 上。
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
	ringSize  = 16
	producers = 4
	consumers = 3
)

type item struct {
	prod int
	seq  int
	v    int64
	tag  string
}

type slot struct {
	seq atomic.Uint64
	val *item
}

type ring struct {
	buf  []slot
	mask uint64
	head atomic.Uint64
	tail atomic.Uint64
	enq  atomic.Int64
	deq  atomic.Int64
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
	if n > 16 {
		n = 16
	}
	return n
}

func newRing(n int) *ring {
	r := &ring{buf: make([]slot, n), mask: uint64(n - 1)}
	for i := range r.buf {
		r.buf[i].seq.Store(uint64(i))
	}
	return r
}

func (r *ring) push(it *item) bool {
	for {
		pos := r.tail.Load()
		s := &r.buf[pos&r.mask]
		seq := s.seq.Load()
		diff := int64(seq - pos)
		if diff == 0 {
			if r.tail.CompareAndSwap(pos, pos+1) {
				s.val = it
				s.seq.Store(pos + 1)
				r.enq.Add(1)
				return true
			}
		} else if diff < 0 {
			return false
		} else {
			runtime.Gosched()
		}
	}
}

func (r *ring) pop() (*item, bool) {
	for {
		pos := r.head.Load()
		s := &r.buf[pos&r.mask]
		seq := s.seq.Load()
		diff := int64(seq - (pos + 1))
		if diff == 0 {
			if r.head.CompareAndSwap(pos, pos+1) {
				it := s.val
				s.val = nil
				s.seq.Store(pos + uint64(len(r.buf)))
				r.deq.Add(1)
				return it, true
			}
		} else if diff < 0 {
			return nil, false
		} else {
			runtime.Gosched()
		}
	}
}

func expect(seed int64, prod, seq int) int64 {
	return seed*100003 + int64(prod)*7919 + int64(seq)*13
}

func main() {
	seed := seedVal()
	per := 120 * tortureRounds(1)
	total := int64(producers * per)
	r := newRing(ringSize)
	seen := make([]int32, producers*per)
	sum := make([]atomic.Int64, producers)
	var wantSum [producers]int64
	for p := 0; p < producers; p++ {
		for i := 0; i < per; i++ {
			wantSum[p] += expect(seed, p, i)
		}
	}

	var prodDone atomic.Bool
	var pwg sync.WaitGroup
	for p := 0; p < producers; p++ {
		pwg.Add(1)
		go func(p int) {
			defer pwg.Done()
			spins := 0
			for i := 0; i < per; i++ {
				it := &item{prod: p, seq: i, v: expect(seed, p, i), tag: "ring"}
				for !r.push(it) {
					spins++
					if spins%64 == 0 {
						gcStress()
					}
					runtime.Gosched()
				}
				if i%40 == 0 {
					gcStress()
					if it.v != expect(seed, it.prod, it.seq) || it.tag != "ring" {
						ck(false, "producer payload corrupted by GC")
					}
				}
			}
		}(p)
	}

	var consumed atomic.Int64
	var cwg sync.WaitGroup
	for c := 0; c < consumers; c++ {
		cwg.Add(1)
		go func(c int) {
			defer cwg.Done()
			local := 0
			for {
				if consumed.Load() >= total {
					return
				}
				it, ok := r.pop()
				if !ok {
					if prodDone.Load() && r.enq.Load() == total && r.deq.Load() == total {
						if consumed.Load() >= total {
							return
						}
					}
					runtime.Gosched()
					continue
				}
				if it == nil || it.tag != "ring" || it.v != expect(seed, it.prod, it.seq) {
					ck(false, "consumer observed invalid item")
					continue
				}
				idx := it.prod*per + it.seq
				if idx < 0 || idx >= len(seen) {
					ck(false, "item index out of range")
					continue
				}
				if !atomic.CompareAndSwapInt32(&seen[idx], 0, 1) {
					ck(false, "duplicate item from ring")
				}
				sum[it.prod].Add(it.v)
				local++
				if local%64 == 0 {
					gcStress()
					if it.v != expect(seed, it.prod, it.seq) {
						ck(false, "consumed item corrupted by GC")
					}
				}
				consumed.Add(1)
			}
		}(c)
	}

	pwg.Wait()
	prodDone.Store(true)
	cwg.Wait()

	ck(consumed.Load() == total, fmt.Sprintf("consumed=%d want=%d", consumed.Load(), total))
	ck(r.enq.Load() == total, fmt.Sprintf("enqueued=%d want=%d", r.enq.Load(), total))
	ck(r.deq.Load() == total, fmt.Sprintf("dequeued=%d want=%d", r.deq.Load(), total))
	missing := 0
	for _, v := range seen {
		if v != 1 {
			missing++
		}
	}
	ck(missing == 0, fmt.Sprintf("missing items=%d", missing))
	for p := 0; p < producers; p++ {
		ck(sum[p].Load() == wantSum[p], fmt.Sprintf("producer %d checksum mismatch", p))
	}
	gcStress()

	fmt.Printf("ring=%d producers=%d items=%d consumed=%d\n", ringSize, producers, total, consumed.Load())
	finish("l02_atomic_ring_mpmc")
}
