// L19: 512 个 goroutine 各自持有独立 time.Timer 并发等待。1/4 的 timer 在等待
// 前 Stop 并通过关闭的 cancel channel 立即返回，其余 timer 到期放行；全部等待者
// 就位后主协程显式 GC，验证 timer heap 与各等待 goroutine 栈上的指针 payload。
// TORTURE_ROUNDS 控制轮数，默认 1 轮。
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

const (
	waiters     = 512
	cancelEvery = 4
	waitLimit   = 10 * time.Second
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

func envInt(name string, def, lo, hi int) int {
	s := os.Getenv(name)
	if s == "" {
		return def
	}
	n, err := strconv.Atoi(s)
	if err != nil || n < lo {
		return def
	}
	if n > hi {
		n = hi
	}
	return n
}

func baseOf(idx int, seed int64) int64 {
	return int64(idx)*97 + seed*23
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

type waiterResult struct {
	idx   int
	fired bool
	ok    bool
}

func round(seed int64) {
	ready := make(chan int, waiters)
	results := make(chan waiterResult, waiters)
	var wg sync.WaitGroup

	for i := 0; i < waiters; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			base := baseOf(idx, seed)
			head := makeChain(base)
			cancel := make(chan struct{})
			var t *time.Timer
			if idx%cancelEvery == 0 {
				// 取消路径：先 Stop 再关闭 cancel，select 分支确定。
				t = time.NewTimer(time.Hour)
				t.Stop()
				close(cancel)
			} else {
				t = time.NewTimer(time.Millisecond)
			}
			// 通知主协程 timer 已挂入 runtime timer heap 并即将等待。
			ready <- idx
			fired := false
			select {
			case <-t.C:
				fired = true
			case <-cancel:
			}
			if !fired {
				t.Stop()
			}
			results <- waiterResult{idx: idx, fired: fired, ok: chainOK(head, base)}
		}(i)
	}

	for i := 0; i < waiters; i++ {
		select {
		case <-ready:
		case <-time.After(waitLimit):
			ckf(false, "only %d/%d waiters became ready", i, waiters)
			return
		}
	}
	// 所有 timer 已入堆：此时显式 GC，timer 对象与等待者栈同时被扫描。
	gcStress()

	fired, canceled, bad := 0, 0, 0
	var sum int64
	for i := 0; i < waiters; i++ {
		select {
		case res := <-results:
			if res.idx < 0 || res.idx >= waiters {
				ckf(false, "result index %d out of range", res.idx)
				continue
			}
			if res.fired {
				fired++
				base := baseOf(res.idx, seed)
				sum += base*3 + 3
			} else {
				canceled++
			}
			if !res.ok {
				bad++
			}
		case <-time.After(waitLimit):
			ckf(false, "received %d/%d waiter results", i, waiters)
			i = waiters
		}
		if i%64 == 63 {
			gcStress()
		}
	}

	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(waitLimit):
		ck(false, "waiter goroutines did not exit")
	}

	wantFired := waiters - waiters/cancelEvery
	wantCanceled := waiters / cancelEvery
	var wantSum int64
	for i := 0; i < waiters; i++ {
		if i%cancelEvery != 0 {
			base := baseOf(i, seed)
			wantSum += base*3 + 3
		}
	}
	ckf(fired == wantFired, "fired=%d want=%d", fired, wantFired)
	ckf(canceled == wantCanceled, "canceled=%d want=%d", canceled, wantCanceled)
	ckf(bad == 0, "%d waiter payload chains corrupted", bad)
	ckf(sum == wantSum, "sum=%d want=%d", sum, wantSum)
	gcStress()
}

func main() {
	seed := envSeed()
	rounds := envInt("TORTURE_ROUNDS", 1, 1, 10)
	fmt.Printf("waiters=%d rounds=%d\n", waiters, rounds)
	for r := 0; r < rounds; r++ {
		round(seed + int64(r)*181)
	}
	finish("l19_timer_many_waiters")
}
