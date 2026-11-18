// L19: 定时器合并与批量取消。1024 个逻辑活动事件反复 Reset 同一个物理
// timer，最终只允许一次回调；2048 个 AfterFunc timer 由 4 个 goroutine
// 批量 Stop，全部返回 true 且不得触发回调。计时器仍在堆中时显式 GC。
package main

import (
	"fmt"
	"os"
	"runtime"
	"strconv"
	"sync"
	"sync/atomic"
	"time"
)

type node struct {
	v    int64
	next *node
}

var failed int32

const waitLimit = 10 * time.Second

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
	bufs := make([][]byte, 0, 16)
	for i := 0; i < 16; i++ {
		b := make([]byte, 4096)
		b[len(b)-1] = byte(i)
		bufs = append(bufs, b)
	}
	runtime.GC()
	runtime.GC()
	runtime.KeepAlive(bufs)
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

func envRounds(def int) int {
	s := os.Getenv("TORTURE_ROUNDS")
	if s == "" {
		return def
	}
	v, err := strconv.Atoi(s)
	if err != nil || v < 1 {
		return def
	}
	if v > 20 {
		v = 20
	}
	return v
}

func makeChain(base int64) *node {
	return &node{v: base, next: &node{v: base + 1, next: &node{v: base + 2}}}
}

func chainOK(head *node, base int64) bool {
	if head == nil || head.v != base || head.next == nil || head.next.v != base+1 {
		return false
	}
	return head.next.next != nil && head.next.next.v == base+2 && head.next.next.next == nil
}

// coalesceRound 把 events 个逻辑活动合并到一个物理 AfterFunc timer 上。
func coalesceRound(seed int64, round int) {
	const events = 1024
	const delay = 25 * time.Millisecond

	base := seed*100000 + int64(round)*10000 + 1
	var mu sync.Mutex
	var fires int32
	done := make(chan struct{}, 4)
	payloads := make([]*node, 0, events)
	var t *time.Timer

	for i := 0; i < events; i++ {
		p := makeChain(base + int64(i)*3)
		payloads = append(payloads, p)
		mu.Lock()
		if t == nil {
			t = time.AfterFunc(delay, func() {
				mu.Lock()
				atomic.AddInt32(&fires, 1)
				mu.Unlock()
				select {
				case done <- struct{}{}:
				default:
				}
			})
		} else {
			t.Reset(delay)
		}
		mu.Unlock()
	}

	// timer 仍在堆中排队，先 GC 再等唯一一次回调。
	gcStress()
	select {
	case <-done:
	case <-time.After(waitLimit):
		ckf(false, "coalesced timer did not fire (seed=%d round=%d)", seed, round)
		return
	}
	ckf(atomic.LoadInt32(&fires) == 1, "coalesced fires=%d want=1 (seed=%d round=%d)",
		atomic.LoadInt32(&fires), seed, round)
	t.Stop()
	gcStress()

	bad := 0
	for i, p := range payloads {
		if !chainOK(p, base+int64(i)*3) {
			bad++
		}
	}
	ckf(bad == 0, "coalesced payloads corrupted: bad=%d (seed=%d round=%d)", bad, seed, round)
}

// cancelRound 批量 Stop 2048 个 AfterFunc timer，全部成功后不得回调。
func cancelRound(seed int64, round int) {
	const n = 2048
	const workers = 4

	base := seed*100000 + int64(round)*10000 + 5000
	timers := make([]*time.Timer, n)
	chains := make([]*node, n)
	var fired int32
	var bad int32

	for i := 0; i < n; i++ {
		b := base + int64(i)*3
		p := makeChain(b)
		chains[i] = p
		timers[i] = time.AfterFunc(time.Hour, func() {
			atomic.AddInt32(&fired, 1)
			if !chainOK(p, b) {
				atomic.StoreInt32(&bad, 1)
			}
		})
	}
	gcStress()

	var stopped int32
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		lo, hi := w*n/workers, (w+1)*n/workers
		wg.Add(1)
		go func(lo, hi int) {
			defer wg.Done()
			for i := lo; i < hi; i++ {
				if timers[i].Stop() {
					atomic.AddInt32(&stopped, 1)
				}
			}
		}(lo, hi)
	}
	wg.Wait()

	ckf(stopped == n, "batch cancel stopped=%d want=%d (seed=%d round=%d)", stopped, n, seed, round)

	// 重复 Stop 已停止的 timer 返回 false，且不得 panic。
	extra := 0
	for i := 0; i < n; i += 97 {
		if timers[i].Stop() {
			extra++
		}
	}
	ckf(extra == 0, "double Stop returned true %d times (seed=%d round=%d)", extra, seed, round)

	// 用一个哨兵 timer 推动调度队列走一圈，确认没有取消的 timer 被触发。
	sentinel := make(chan struct{}, 1)
	time.AfterFunc(time.Millisecond, func() {
		select {
		case sentinel <- struct{}{}:
		default:
		}
	})
	select {
	case <-sentinel:
	case <-time.After(waitLimit):
		ckf(false, "sentinel timer did not fire (seed=%d round=%d)", seed, round)
	}
	gcStress()

	ckf(atomic.LoadInt32(&fired) == 0, "canceled timers fired=%d want=0 (seed=%d round=%d)",
		atomic.LoadInt32(&fired), seed, round)
	ckf(atomic.LoadInt32(&bad) == 0, "canceled timer payload corrupted (seed=%d round=%d)", seed, round)

	badChains := 0
	for i := 0; i < n; i += 13 {
		if !chainOK(chains[i], base+int64(i)*3) {
			badChains++
		}
	}
	ckf(badChains == 0, "canceled payload chains corrupted: bad=%d (seed=%d round=%d)",
		badChains, seed, round)
}

func main() {
	seed := envSeed()
	rounds := envRounds(1)
	for r := 0; r < rounds; r++ {
		coalesceRound(seed, r)
		cancelRound(seed, r)
	}
	fmt.Printf("rounds=%d events_per_round=1024 canceled_per_round=2048\n", rounds)
	finish("l19_timer_coalesce_cancel")
}
