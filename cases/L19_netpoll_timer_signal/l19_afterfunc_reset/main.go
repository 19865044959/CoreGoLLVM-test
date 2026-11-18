// L19: time.AfterFunc 的 Reset 语义。覆盖 active/fired/stopped 三种状态下
// Reset 的返回值、回调内自 Reset、以及回调阻塞期间 Reset 再次武装。每次
// 状态迁移前后显式 GC 并校验闭包捕获的指针链。
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

const waitLimit = 5 * time.Second

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

func makeChain(base int64) *node {
	return &node{v: base, next: &node{v: base + 1, next: &node{v: base + 2}}}
}

func chainOK(head *node, base int64) bool {
	if head == nil || head.v != base || head.next == nil || head.next.v != base+1 {
		return false
	}
	return head.next.next != nil && head.next.next.v == base+2 && head.next.next.next == nil
}

func waitCh(ch <-chan struct{}, msg string) bool {
	select {
	case <-ch:
		return true
	case <-time.After(waitLimit):
		ck(false, msg)
		return false
	}
}

func main() {
	seed := envSeed()
	base := seed*1000 + 1
	var bad int32

	// 1) 活跃 AfterFunc：Reset 返回 true，只触发一次。
	var fires int32
	p := makeChain(base)
	fired := make(chan struct{}, 8)
	t := time.AfterFunc(time.Hour, func() {
		if !chainOK(p, base) {
			atomic.StoreInt32(&bad, 1)
		}
		atomic.AddInt32(&fires, 1)
		fired <- struct{}{}
	})
	gcStress()
	ck(t.Reset(time.Millisecond), "Reset on active AfterFunc returned false")
	waitCh(fired, "active AfterFunc did not fire after Reset")
	gcStress()
	ckf(atomic.LoadInt32(&fires) == 1, "active fires=%d want=1 (seed=%d)", atomic.LoadInt32(&fires), seed)
	ck(chainOK(p, base), "active payload corrupted after GC")

	// 2) 已触发且回调已完成：Reset 返回 false 并重新武装。
	ck(!t.Reset(time.Millisecond), "Reset on fired AfterFunc returned true")
	waitCh(fired, "fired AfterFunc did not fire after Reset")
	gcStress()
	ckf(atomic.LoadInt32(&fires) == 2, "fired fires=%d want=2 (seed=%d)", atomic.LoadInt32(&fires), seed)
	ck(chainOK(p, base), "fired payload corrupted after GC")

	// 3) Stop 后 Reset：Stop 返回 true，Reset 返回 false 并重新武装。
	var stoppedFires int32
	p2 := makeChain(base + 50)
	t2 := time.AfterFunc(time.Hour, func() {
		if !chainOK(p2, base+50) {
			atomic.StoreInt32(&bad, 1)
		}
		atomic.AddInt32(&stoppedFires, 1)
		fired <- struct{}{}
	})
	ck(t2.Stop(), "Stop on fresh AfterFunc returned false")
	gcStress()
	ck(!t2.Reset(time.Millisecond), "Reset on stopped AfterFunc returned true")
	waitCh(fired, "AfterFunc did not fire after Stop+Reset")
	ckf(atomic.LoadInt32(&stoppedFires) == 1, "stopped fires=%d want=1 (seed=%d)",
		atomic.LoadInt32(&stoppedFires), seed)
	ck(!t2.Stop(), "Stop on expired AfterFunc returned true")
	ck(chainOK(p2, base+50), "stopped payload corrupted after GC")

	// 4) 回调内自 Reset：连续 8 次触发，闭包指针在 GC 后保持完整。
	const selfTarget = 8
	var self int32
	var selfBad int32
	selfDone := make(chan struct{}, selfTarget+4)
	pSelf := makeChain(base + 100)
	var stMu sync.Mutex
	var st *time.Timer
	stMu.Lock()
	st = time.AfterFunc(time.Millisecond, func() {
		n := atomic.AddInt32(&self, 1)
		if !chainOK(pSelf, base+100) {
			atomic.StoreInt32(&selfBad, 1)
		}
		selfDone <- struct{}{}
		if n < selfTarget {
			stMu.Lock()
			t := st
			stMu.Unlock()
			t.Reset(time.Millisecond)
		}
	})
	stMu.Unlock()
	for i := 0; i < selfTarget; i++ {
		if !waitCh(selfDone, "self-reset callback did not fire") {
			break
		}
		if i%2 == 1 {
			gcStress()
		}
	}
	gcStress()
	ckf(atomic.LoadInt32(&self) == selfTarget, "self-reset fires=%d want=%d (seed=%d)",
		atomic.LoadInt32(&self), selfTarget, seed)
	ckf(atomic.LoadInt32(&selfBad) == 0, "self-reset payload corrupted (seed=%d)", seed)
	ck(!st.Stop(), "Stop after self-reset loop returned true")

	// 5) 回调阻塞期间 Reset：Reset 返回 false，放行后恰好再触发一次。
	gate := make(chan struct{})
	started := make(chan struct{}, 4)
	done := make(chan struct{}, 4)
	pBlocked := makeChain(base + 200)
	var blockedFires int32
	var bt *time.Timer
	bt = time.AfterFunc(time.Millisecond, func() {
		atomic.AddInt32(&blockedFires, 1)
		started <- struct{}{}
		<-gate
		if !chainOK(pBlocked, base+200) {
			atomic.StoreInt32(&bad, 1)
		}
		done <- struct{}{}
	})
	waitCh(started, "blocking callback did not start")
	gcStress() // 回调阻塞在 gate 上，指针在协程栈中
	ck(!bt.Reset(time.Millisecond), "Reset while callback running returned true")
	close(gate)
	waitCh(done, "first blocked callback did not finish")
	waitCh(started, "second callback did not start after Reset")
	waitCh(done, "second blocked callback did not finish")
	gcStress()
	ckf(atomic.LoadInt32(&blockedFires) == 2, "blocked fires=%d want=2 (seed=%d)",
		atomic.LoadInt32(&blockedFires), seed)
	ck(chainOK(pBlocked, base+200), "blocked payload corrupted after GC")
	ck(!bt.Stop(), "Stop after blocked callback returned true")

	ckf(atomic.LoadInt32(&bad) == 0, "payload corrupted in callback (seed=%d)", seed)
	fmt.Println("afterfunc_checks=5")
	finish("l19_afterfunc_reset")
}
