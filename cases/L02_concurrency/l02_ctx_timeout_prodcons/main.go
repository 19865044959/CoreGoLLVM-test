// L02: 生产/消费的超时与取消路径：预填有界 channel 后，消费者处理固定前缀，
// 主 goroutine 显式 cancel；另用已到期 deadline 确定性触发生产者放弃、消费者超时。
package main

import (
	"context"
	"fmt"
	"os"
	"runtime"
	"strconv"
	"sync"
	"sync/atomic"
	"time"
)

const (
	totalA    = 48
	stopAfter = 16
	totalB    = 24
)

var (
	failed  int32
	gcMu    sync.Mutex
	gcSink  [][]byte
	curSeed int64
)

type packet struct {
	seq   int
	phase int
	tag   int64
	name  string
}

type message struct {
	p *packet
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

func tagFor(seed int64, seq, phase int) int64 {
	x := uint64(seed)*0x9E3779B97F4A7C15 + uint64(seq+1)*0xBF58476D1CE4E5B9 + uint64(phase+1)
	x ^= x >> 30
	x *= 0x94D049BB133111EB
	x ^= x >> 31
	return int64(x >> 1)
}

func newPacket(seq, phase int) *packet {
	return &packet{seq: seq, phase: phase, tag: tagFor(curSeed, seq, phase), name: "pkt"}
}

func validPacket(p *packet, seq, phase int) bool {
	return p != nil && p.seq == seq && p.phase == phase && p.name == "pkt" && p.tag == tagFor(curSeed, seq, phase)
}

func main() {
	curSeed = envSeed()

	// Phase A: 取消消费端，固定前缀之后停止，残留消息随后显式排空。
	ctxA, cancelA := context.WithCancel(context.Background())
	chA := make(chan message, totalA)
	for i := 0; i < totalA; i++ {
		chA <- message{p: newPacket(i, 1)}
	}
	close(chA)
	seenA := make([]int32, totalA)
	reached := make(chan struct{})
	var consumedA int32
	var cwg sync.WaitGroup
	cwg.Add(1)
	go func() {
		defer cwg.Done()
		n := 0
		var held *packet
		for {
			if n == stopAfter {
				close(reached)
				<-ctxA.Done()
				if ctxA.Err() != context.Canceled {
					ck(false, "consumer did not observe context.Canceled")
				}
				gcStress()
				if !validPacket(held, stopAfter-1, 1) {
					ckSeed(false, "held packet corrupted across cancel/GC")
				}
				return
			}
			select {
			case m := <-chA:
				if !validPacket(m.p, n, 1) {
					ckSeed(false, "consumer received corrupted or out-of-order packet")
					continue
				}
				if !atomic.CompareAndSwapInt32(&seenA[m.p.seq], 0, 1) {
					ckSeed(false, "consumer saw duplicate packet")
				}
				held = m.p
				n++
				atomic.StoreInt32(&consumedA, int32(n))
				if n%8 == 0 {
					gcStress()
					if !validPacket(held, n-1, 1) {
						ckSeed(false, "consumed packet corrupted after GC")
					}
				}
			case <-ctxA.Done():
				ck(false, "context canceled before the consumption prefix")
				return
			}
		}
	}()
	<-reached
	gcStress()
	cancelA()
	cwg.Wait()
	if got := atomic.LoadInt32(&consumedA); got != stopAfter {
		ck(false, fmt.Sprintf("consumed before cancel got=%d want=%d", got, stopAfter))
	}

	residual := 0
	var sumA, sumRes int64
	for i := 0; i < stopAfter; i++ {
		sumA += tagFor(curSeed, i, 1)
	}
	for m := range chA {
		wantSeq := stopAfter + residual
		if !validPacket(m.p, wantSeq, 1) {
			ckSeed(false, "residual packet corrupted or out of order")
		} else {
			if !atomic.CompareAndSwapInt32(&seenA[m.p.seq], 0, 1) {
				ckSeed(false, "residual packet already seen")
			}
			sumRes += m.p.tag
		}
		residual++
		if residual%8 == 0 {
			gcStress()
			if m.p != nil && m.p.seq != wantSeq {
				ckSeed(false, "residual packet corrupted after GC")
			}
		}
	}
	ckSeed(residual == totalA-stopAfter,
		fmt.Sprintf("residual got=%d want=%d", residual, totalA-stopAfter))
	missing := 0
	for _, v := range seenA {
		if v != 1 {
			missing++
		}
	}
	ckSeed(missing == 0, fmt.Sprintf("packets not accounted for=%d", missing))
	var wantSumA int64
	for i := 0; i < totalA; i++ {
		wantSumA += tagFor(curSeed, i, 1)
	}
	ckSeed(sumA+sumRes == wantSumA, "cancel-phase checksum mismatch")

	// Phase B/C: deadline 已过期，生产者的发送 select 与消费者的接收 select
	// 都只能观察 Done；不依赖任何时长阈值。
	ctxB, cancelB := context.WithDeadline(context.Background(), time.Unix(0, 0))
	if ctxB.Err() != context.DeadlineExceeded {
		ck(false, "expired context did not report DeadlineExceeded")
	}
	select {
	case <-ctxB.Done():
	default:
		ck(false, "expired context Done is not closed")
	}
	chB := make(chan message)
	var sentB, abortedB int32
	var pwg sync.WaitGroup
	pwg.Add(1)
	go func() {
		defer pwg.Done()
		for i := 0; i < totalB; i++ {
			p := newPacket(i, 2)
			select {
			case chB <- message{p: p}:
				atomic.AddInt32(&sentB, 1)
			case <-ctxB.Done():
				if !validPacket(p, i, 2) {
					ckSeed(false, "aborted packet corrupted")
				}
				atomic.AddInt32(&abortedB, 1)
			}
		}
	}()
	var timeoutSeen int32
	var cwg2 sync.WaitGroup
	cwg2.Add(1)
	go func() {
		defer cwg2.Done()
		<-ctxB.Done()
		atomic.StoreInt32(&timeoutSeen, 1)
	}()
	pwg.Wait()
	cwg2.Wait()
	cancelB()
	gcStress()
	ck(atomic.LoadInt32(&sentB) == 0,
		fmt.Sprintf("expired producer sent=%d want=0", atomic.LoadInt32(&sentB)))
	ck(atomic.LoadInt32(&abortedB) == totalB,
		fmt.Sprintf("expired producer aborts=%d want=%d", atomic.LoadInt32(&abortedB), totalB))
	ck(atomic.LoadInt32(&timeoutSeen) == 1, "expired consumer did not take the Done branch")

	fmt.Printf("cancel_consumed=%d cancel_residual=%d expired_sent=0 expired_aborts=%d timeout_seen=%d\n",
		stopAfter, residual, atomic.LoadInt32(&abortedB), timeoutSeen)
	finish("l02_ctx_timeout_prodcons")
}
