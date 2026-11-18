// L19: 零/负时长定时器的立即到期语义。NewTimer(0/-d)、After(0)、AfterFunc(0/-d)
// 都必须立即就绪；已到期 timer 的 Stop 返回 false，Reset(0/-d) 重新立即排程；
// 非正周期 Ticker 必须 panic。每条路径都用有界等待防死锁，并穿插显式 GC
// 校验闭包捕获的指针链。
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

const waitLimit = 3 * time.Second

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

func chainOK(h *node, base int64) bool {
	if h == nil || h.v != base || h.next == nil || h.next.v != base+1 {
		return false
	}
	return h.next.next != nil && h.next.next.v == base+2 && h.next.next.next == nil
}

func recv(c <-chan time.Time, what string) bool {
	select {
	case <-c:
		return true
	case <-time.After(waitLimit):
		ckf(false, "%s: timer did not fire", what)
		return false
	}
}

func panics(fn func()) (panicked bool) {
	defer func() {
		if recover() != nil {
			panicked = true
		}
	}()
	fn()
	return false
}

func main() {
	seed := envSeed()
	chain := makeChain(seed*29 + 11)

	// NewTimer(0)：立即触发，读空后 Stop 返回 false。
	t0 := time.NewTimer(0)
	if recv(t0.C, "NewTimer(0)") {
		ck(!t0.Stop(), "Stop after zero timer fired must return false")
	}
	gcStress()
	ck(chainOK(chain, seed*29+11), "payload corrupted after zero timer")

	// 负时长同样立即触发；Reset 为负/零时重新立即排程。
	tn := time.NewTimer(-5 * time.Second)
	if recv(tn.C, "NewTimer(-5s)") {
		ck(!tn.Stop(), "Stop after negative timer fired must return false")
		if tn.Reset(-time.Second) {
			ck(false, "Reset(negative) on expired timer must return false")
		}
		if recv(tn.C, "Reset(-1s)") {
			ck(!tn.Stop(), "Stop after Reset(-1s) fired must return false")
		}
		if tn.Reset(0) {
			ck(false, "Reset(0) on expired timer must return false")
		}
		recv(tn.C, "Reset(0)")
	}

	// time.After(0) 立即就绪。
	recv(time.After(0), "After(0)")

	// 活跃 timer 先 Stop，再用零时长 Reset：Reset 返回 false 但必须再次触发。
	tl := time.NewTimer(time.Hour)
	if !tl.Stop() {
		ck(false, "Stop on active hour timer must return true")
	}
	if tl.Reset(0) {
		ck(false, "Reset(0) on stopped timer must return false")
	}
	recv(tl.C, "active timer Reset(0)")
	gcStress()
	ck(chainOK(chain, seed*29+11), "payload corrupted after resets")

	// AfterFunc(0) 与 AfterFunc(negative) 都立即执行；Reset/Stop 返回 false。
	base := seed*31 + 13
	afChain := makeChain(base)
	var fired int32
	firedCh := make(chan struct{}, 4)
	var af *time.Timer
	armed := make(chan struct{})
	af = time.AfterFunc(0, func() {
		<-armed
		atomic.AddInt32(&fired, 1)
		if !chainOK(afChain, base) {
			ckf(false, "AfterFunc(0) payload corrupted, fired=%d", atomic.LoadInt32(&fired))
		}
		firedCh <- struct{}{}
	})
	close(armed)
	select {
	case <-firedCh:
	case <-time.After(waitLimit):
		ck(false, "AfterFunc(0) did not fire")
	}
	ck(!af.Stop(), "Stop after AfterFunc(0) fired must return false")
	if af.Reset(0) {
		ck(false, "Reset(0) on expired AfterFunc must return false")
	}
	select {
	case <-firedCh:
	case <-time.After(waitLimit):
		ck(false, "AfterFunc(0) did not fire after Reset")
	}
	ckf(atomic.LoadInt32(&fired) == 2, "AfterFunc(0) fired=%d want=2", atomic.LoadInt32(&fired))
	gcStress()
	ck(chainOK(afChain, base), "AfterFunc payload corrupted after GC")

	var negFired int32
	negDone := make(chan struct{})
	afNeg := time.AfterFunc(-time.Second, func() {
		atomic.AddInt32(&negFired, 1)
		close(negDone)
	})
	select {
	case <-negDone:
	case <-time.After(waitLimit):
		ck(false, "AfterFunc(-1s) did not fire")
	}
	ckf(atomic.LoadInt32(&negFired) == 1, "AfterFunc(-1s) fired=%d want=1", atomic.LoadInt32(&negFired))
	if afNeg.Stop() {
		ck(false, "Stop after AfterFunc(-1s) fired must return false")
	}

	// 非正周期 Ticker 必须 panic。
	ck(panics(func() { time.NewTicker(0) }), "NewTicker(0) must panic")
	ck(panics(func() { time.NewTicker(-time.Second) }), "NewTicker(-1s) must panic")

	gcStress()
	finish("l19_timer_negative_zero")
}
