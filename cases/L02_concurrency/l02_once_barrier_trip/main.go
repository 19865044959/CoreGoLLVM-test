// L02: 一次性屏障（trip barrier）：N 个参与者全部到齐后屏障永久打开，
// 最后到达者成为唯一 leader；之后到达的 Wait 立即通过且不会再次触发。
// 校验屏障前的共享 payload 在屏障后对全部参与者可见，且只 trip 一次。
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
	obParticipants = 8
	obLateWaiters  = 5
)

var (
	failed  int32
	gcMu    sync.Mutex
	gcSink  [][]byte
	curSeed int64
)

type tripBarrier struct {
	mu      sync.Mutex
	n       int
	arrived int
	open    bool
	leader  int
	ch      chan struct{}
}

func newTripBarrier(n int) *tripBarrier {
	return &tripBarrier{n: n, leader: -1, ch: make(chan struct{})}
}

// wait 返回 (isLeader, wasLate)。late 调用在屏障打开后立即返回且不参与计数。
func (b *tripBarrier) wait(id int) (bool, bool) {
	b.mu.Lock()
	if b.open {
		b.mu.Unlock()
		return false, true
	}
	b.arrived++
	if b.arrived == b.n {
		b.leader = id
		b.open = true
		close(b.ch)
		b.mu.Unlock()
		return true, false
	}
	ch := b.ch
	b.mu.Unlock()
	<-ch
	b.mu.Lock()
	isLeader := b.leader == id
	b.mu.Unlock()
	return isLeader, false
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

func payloadVal(seed int64, id int) int64 { return seed*997 + int64(id)*41 + 19 }

func main() {
	curSeed = envSeed()
	b := newTripBarrier(obParticipants)
	payloads := make([]int64, obParticipants)
	var ready, passed, leaders int32
	var wg sync.WaitGroup

	for id := 0; id < obParticipants; id++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			payloads[id] = payloadVal(curSeed, id)
			atomic.AddInt32(&ready, 1)
			if id%2 == 0 {
				gcStress()
				if payloads[id] != payloadVal(curSeed, id) {
					ckSeed(false, "pre-barrier payload corrupted by GC")
				}
			}
			isLeader, late := b.wait(id)
			if late {
				ck(false, "participant observed barrier as already open")
			}
			if isLeader {
				atomic.AddInt32(&leaders, 1)
			}
			if n := atomic.LoadInt32(&ready); n != obParticipants {
				ckSeed(false, fmt.Sprintf("barrier released before all ready: ready=%d", n))
			}
			bad := 0
			for j := 0; j < obParticipants; j++ {
				if payloads[j] != payloadVal(curSeed, j) {
					bad++
				}
			}
			ckSeed(bad == 0, "payloads not visible after barrier")
			atomic.AddInt32(&passed, 1)
		}(id)
	}
	wg.Wait()

	ck(atomic.LoadInt32(&leaders) == 1, fmt.Sprintf("leaders=%d want=1", leaders))
	ck(atomic.LoadInt32(&ready) == obParticipants, fmt.Sprintf("ready=%d want=%d", ready, obParticipants))
	ck(atomic.LoadInt32(&passed) == obParticipants, fmt.Sprintf("passed=%d want=%d", passed, obParticipants))

	// 屏障打开后：迟到者立即通过，且永远不会产生新 leader。
	var lwg sync.WaitGroup
	var lateOK, lateLeaders int32
	for i := 0; i < obLateWaiters; i++ {
		lwg.Add(1)
		go func(i int) {
			defer lwg.Done()
			isLeader, late := b.wait(100 + i)
			if late && !isLeader {
				atomic.AddInt32(&lateOK, 1)
			}
			if isLeader {
				atomic.AddInt32(&lateLeaders, 1)
			}
		}(i)
	}
	lwg.Wait()
	ck(atomic.LoadInt32(&lateOK) == obLateWaiters,
		fmt.Sprintf("late pass=%d want=%d", lateOK, obLateWaiters))
	ck(atomic.LoadInt32(&lateLeaders) == 0, "late waiter must not become leader")
	// 再次调用仍然不阻塞、不重开。
	if isLeader, late := b.wait(999); !late || isLeader {
		ck(false, "repeated wait after trip must be late and non-leader")
	}

	gcStress()
	bad := 0
	for j := 0; j < obParticipants; j++ {
		if payloads[j] != payloadVal(curSeed, j) {
			bad++
		}
	}
	ckSeed(bad == 0, "payloads corrupted by final GC")

	fmt.Printf("participants=%d leaders=1 late_waiters=%d\n", obParticipants, obLateWaiters)
	finish("l02_once_barrier_trip")
}
