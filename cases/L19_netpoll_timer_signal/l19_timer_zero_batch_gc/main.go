// L19: 大批零延时 AfterFunc 的 timer heap + GC 边界。每轮挂入固定 256 个
// AfterFunc(0)，回调先报到再阻塞在 gate 上；主协程确认全部回调已开始后显式 GC，
// 此时 timer 已到期且 256 个回调栈同时被扫描。sampled Stop 必须返回 false，
// 放行后每个回调恰好执行一次，被取消路径不存在。TORTURE_ROUNDS 放大轮数。
package main

import (
	"fmt"
	"os"
	"runtime"
	"strconv"
	"sync/atomic"
	"time"
)

type node struct {
	v    int64
	next *node
}

var failed int32

const (
	batch     = 256
	sampled   = 32
	waitLimit = 10 * time.Second
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

func makeChain(base int64) *node {
	return &node{v: base, next: &node{v: base + 1, next: &node{v: base + 2}}}
}

func chainOK(h *node, base int64) bool {
	if h == nil || h.v != base || h.next == nil || h.next.v != base+1 {
		return false
	}
	return h.next.next != nil && h.next.next.v == base+2 && h.next.next.next == nil
}

func round(seed int64, roundIdx int) {
	gate := make(chan struct{})
	entered := make(chan struct{}, batch)
	done := make(chan struct{})
	var fired, dups int32
	chains := make([]*node, batch)
	bases := make([]int64, batch)
	timers := make([]*time.Timer, batch)

	for i := 0; i < batch; i++ {
		base := seed*47 + int64(roundIdx*1000+i)*13 + 3
		bases[i] = base
		chains[i] = makeChain(base)
		idx := i
		timers[i] = time.AfterFunc(0, func() {
			entered <- struct{}{}
			<-gate
			if !chainOK(chains[idx], bases[idx]) {
				ckf(false, "round %d timer %d payload corrupted", roundIdx, idx)
			}
			n := atomic.AddInt32(&fired, 1)
			if n > batch {
				atomic.AddInt32(&dups, 1)
				return
			}
			if n == batch {
				close(done)
			}
		})
	}

	// 全部回调已进入并阻塞在 gate 上：timer 已到期，回调栈可被 GC 扫描。
	for i := 0; i < batch; i++ {
		select {
		case <-entered:
		case <-time.After(waitLimit):
			ckf(false, "round %d: only %d/%d callbacks started", roundIdx, i, batch)
			close(gate)
			return
		}
	}
	for i := 0; i < sampled; i++ {
		if timers[i].Stop() {
			ckf(false, "round %d: Stop on expired callback timer %d returned true", roundIdx, i)
		}
	}
	gcStress()
	for i := 0; i < batch; i++ {
		if !chainOK(chains[i], bases[i]) {
			ckf(false, "round %d timer %d payload corrupted after GC", roundIdx, i)
		}
	}

	close(gate)
	select {
	case <-done:
	case <-time.After(waitLimit):
		ckf(false, "round %d: callbacks did not finish", roundIdx)
	}
	ckf(atomic.LoadInt32(&fired) == batch, "round %d fired=%d want=%d", roundIdx, atomic.LoadInt32(&fired), batch)

	// 哨兵零 timer 触发后再 GC，确认没有迟到/重复回调。
	sent := make(chan struct{})
	time.AfterFunc(0, func() { close(sent) })
	select {
	case <-sent:
	case <-time.After(waitLimit):
		ckf(false, "round %d: sentinel timer did not fire", roundIdx)
	}
	gcStress()
	ckf(atomic.LoadInt32(&fired) == batch, "round %d fired after sentinel=%d want=%d", roundIdx, atomic.LoadInt32(&fired), batch)
	ckf(atomic.LoadInt32(&dups) == 0, "round %d duplicate callbacks=%d", roundIdx, atomic.LoadInt32(&dups))
}

func main() {
	seed := envSeed()
	rounds := envInt("TORTURE_ROUNDS", 2, 1, 20)
	fmt.Printf("batch=%d sampled_stop=%d rounds=%d\n", batch, sampled, rounds)
	for r := 0; r < rounds; r++ {
		round(seed+int64(r)*151, r)
	}
	finish("l19_timer_zero_batch_gc")
}
