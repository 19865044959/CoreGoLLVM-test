// L04: sync.Mutex + 双 sync.Cond 的有界缓冲：生产者/消费者在 notFull/notEmpty
// 上睡眠。先让消费者全部睡在空队列上 GC，再让生产者填满队列并全部睡在
// notFull 上 GC，然后放行消费；校验每个生产者的序号不重不漏。
package main

import (
	"fmt"
	"os"
	"runtime"
	"strconv"
	"sync"
	"sync/atomic"
)

type cbItem struct {
	prod int
	seq  int
	v    int64
}

var (
	failed int32
	gcSink [][]byte
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

func finish(name string, seed int64) {
	if n := atomic.LoadInt32(&failed); n != 0 {
		fmt.Printf("FAILED %s (UAF_SEED=%d, %d failed checks)\n", name, seed, n)
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

func main() {
	seed := envSeed()
	const capacity = 6
	const producers = 4
	const consumers = 3
	const perProducer = 12
	const total = producers * perProducer

	var mu sync.Mutex
	buf := make([]*cbItem, 0, capacity)
	notFull := sync.NewCond(&mu)
	notEmpty := sync.NewCond(&mu)
	closed := false
	pause := true // 消费者在 notEmpty 上保持睡眠，直到队满 GC 完成

	var emptyWaiters, fullWaiters int32
	seqCh := make([]chan int, producers)
	itemLocks := make([]sync.Mutex, producers)
	seen := make([]map[int]bool, producers)
	for p := 0; p < producers; p++ {
		seqCh[p] = make(chan int, perProducer)
		seen[p] = make(map[int]bool, perProducer)
	}

	var cwg sync.WaitGroup
	cwg.Add(consumers)
	for c := 0; c < consumers; c++ {
		go func() {
			defer cwg.Done()
			for {
				mu.Lock()
				for (len(buf) == 0 || pause) && !closed {
					atomic.AddInt32(&emptyWaiters, 1)
					notEmpty.Wait()
					atomic.AddInt32(&emptyWaiters, -1)
				}
				if len(buf) == 0 && closed {
					mu.Unlock()
					break
				}
				it := buf[0]
				buf = buf[1:]
				notFull.Signal()
				mu.Unlock()

				if it == nil {
					ck(false, "consumer observed nil buffered item")
					continue
				}
				want := seed*100003 + int64(it.prod)*7919 + int64(it.seq)*131
				if it.v != want {
					ckf(false, "consumer prod=%d seq=%d value=%d want=%d", it.prod, it.seq, it.v, want)
				}
				itemLocks[it.prod].Lock()
				if seen[it.prod][it.seq] {
					ckf(false, "duplicate item prod=%d seq=%d", it.prod, it.seq)
				}
				seen[it.prod][it.seq] = true
				itemLocks[it.prod].Unlock()
				seqCh[it.prod] <- it.seq
			}
		}()
	}

	// 消费者全部睡在空队列的 notEmpty 上时 GC。
	fullEmpty := false
	for i := 0; i < 2000000; i++ {
		if atomic.LoadInt32(&emptyWaiters) == consumers {
			fullEmpty = true
			break
		}
		runtime.Gosched()
	}
	ck(fullEmpty, "consumers did not all block on notEmpty")
	gcStress()

	var pwg sync.WaitGroup
	pwg.Add(producers)
	for p := 0; p < producers; p++ {
		go func(p int) {
			defer pwg.Done()
			for seq := 0; seq < perProducer; seq++ {
				it := &cbItem{
					prod: p,
					seq:  seq,
					v:    seed*100003 + int64(p)*7919 + int64(seq)*131,
				}
				mu.Lock()
				for len(buf) == capacity {
					atomic.AddInt32(&fullWaiters, 1)
					notFull.Wait()
					atomic.AddInt32(&fullWaiters, -1)
				}
				buf = append(buf, it)
				notEmpty.Signal()
				mu.Unlock()
			}
		}(p)
	}

	// 队列填满且所有生产者都睡在 notFull 上时 GC（buf 中全是指针）。
	full := false
	for i := 0; i < 2000000; i++ {
		mu.Lock()
		atCap := len(buf) == capacity
		n := len(buf)
		mu.Unlock()
		if atCap && atomic.LoadInt32(&fullWaiters) == producers {
			full = true
			break
		}
		if n > capacity {
			ckf(false, "buffer overflow len=%d cap=%d", n, capacity)
			break
		}
		runtime.Gosched()
	}
	ck(full, "producers did not all block on notFull")
	gcStress()
	mu.Lock()
	for _, it := range buf {
		ok := it != nil && it.v == seed*100003+int64(it.prod)*7919+int64(it.seq)*131
		ckf(ok, "buffered item corrupted during full-queue GC")
	}
	mu.Unlock()

	mu.Lock()
	pause = false
	notEmpty.Broadcast()
	mu.Unlock()

	go func() {
		pwg.Wait()
		mu.Lock()
		closed = true
		notEmpty.Broadcast()
		mu.Unlock()
	}()
	cwg.Wait()
	pwg.Wait()

	for p := 0; p < producers; p++ {
		close(seqCh[p])
		var got int
		for seq := range seqCh[p] {
			_ = seq
			got++
		}
		ckf(got == perProducer, "producer %d delivered %d items want=%d", p, got, perProducer)
		for seq := 0; seq < perProducer; seq++ {
			ckf(seen[p][seq], "producer %d seq %d missing", p, seq)
		}
	}
	mu.Lock()
	left := len(buf)
	mu.Unlock()
	ckf(left == 0, "buffer not empty at shutdown len=%d", left)

	gcStress()
	for p := 0; p < producers; p++ {
		for seq := 0; seq < perProducer; seq++ {
			ckf(seen[p][seq], "after final GC producer %d seq %d lost", p, seq)
		}
	}
	finish("l04_cond_bounded_buffer", seed)
}
