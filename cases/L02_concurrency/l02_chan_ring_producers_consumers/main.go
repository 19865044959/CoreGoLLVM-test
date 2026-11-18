// L02: N 生产者 / M 消费者经有界 channel 传递指针消息，GC 穿插，
// 用位图去重 + 固定校验和验证不丢、不重、不坏。
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
	nProducers  = 4
	nConsumers  = 3
	chanCap     = 16
	basePerProd = 120
)

var (
	failed  int32
	gcMu    sync.Mutex
	gcSink  [][]byte
	curSeed int64
)

type payload struct {
	prod int
	seq  int
	tag  int64
}

type message struct {
	p *payload
}

type result struct {
	count int
	sum   uint64
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
	if n > 50 {
		n = 50
	}
	return n
}

func tagFor(seed int64, prod, seq int) int64 {
	x := uint64(seed)*0x9E3779B97F4A7C15 + uint64(prod+1)*0xBF58476D1CE4E5B9 + uint64(seq+1)
	x ^= x >> 30
	x *= 0x94D049BB133111EB
	x ^= x >> 31
	return int64(x >> 1)
}

func producer(id, perProd int, ch chan<- message, start <-chan struct{}) {
	<-start
	for seq := 0; seq < perProd; seq++ {
		p := &payload{prod: id, seq: seq, tag: tagFor(curSeed, id, seq)}
		if seq%41 == 0 {
			gcStress()
			ckSeed(p.prod == id && p.seq == seq, "producer payload corrupted before send")
		}
		ch <- message{p: p}
	}
}

func consumer(ch <-chan message, seen []int32, perProd int, res *result, start <-chan struct{}) {
	<-start
	local := result{}
	n := 0
	for m := range ch {
		p := m.p
		if p == nil {
			ck(false, "consumer received nil payload")
			continue
		}
		if p.prod < 0 || p.prod >= nProducers || p.seq < 0 || p.seq >= perProd {
			ckSeed(false, "consumer received payload with out-of-range fields")
			continue
		}
		idx := p.prod*perProd + p.seq
		if !atomic.CompareAndSwapInt32(&seen[idx], 0, 1) {
			ckSeed(false, fmt.Sprintf("duplicate message prod=%d seq=%d", p.prod, p.seq))
		}
		local.count++
		local.sum += uint64(p.tag)
		n++
		if n%32 == 0 {
			gcStress()
			ckSeed(p.prod >= 0 && p.prod < nProducers && p.seq >= 0 && p.seq < perProd,
				"payload corrupted after GC")
		}
	}
	*res = local
}

func main() {
	curSeed = envSeed()
	perProd := basePerProd * tortureRounds(1)

	ch := make(chan message, chanCap)
	seen := make([]int32, nProducers*perProd)
	results := make([]result, nConsumers)
	start := make(chan struct{})

	var pw, cw sync.WaitGroup
	for i := 0; i < nProducers; i++ {
		pw.Add(1)
		go func(id int) {
			defer pw.Done()
			producer(id, perProd, ch, start)
		}(i)
	}
	for i := 0; i < nConsumers; i++ {
		cw.Add(1)
		go func(idx int) {
			defer cw.Done()
			consumer(ch, seen, perProd, &results[idx], start)
		}(i)
	}

	close(start)
	pw.Wait()
	close(ch)
	cw.Wait()

	total := 0
	var sum uint64
	for _, r := range results {
		total += r.count
		sum += r.sum
	}
	want := nProducers * perProd
	ckSeed(total == want, fmt.Sprintf("message count got=%d want=%d", total, want))

	missing := 0
	for i, v := range seen {
		if v != 1 {
			missing++
			if missing <= 4 {
				ckSeed(false, fmt.Sprintf("message missing prod=%d seq=%d", i/perProd, i%perProd))
			}
		}
	}
	ckSeed(missing == 0, fmt.Sprintf("missing messages=%d", missing))

	var wantSum uint64
	for p := 0; p < nProducers; p++ {
		for s := 0; s < perProd; s++ {
			wantSum += uint64(tagFor(curSeed, p, s))
		}
	}
	ckSeed(sum == wantSum, "aggregate checksum mismatch")

	fmt.Printf("producers=%d consumers=%d messages=%d\n", nProducers, nConsumers, total)
	finish("l02_chan_ring_producers_consumers")
}
