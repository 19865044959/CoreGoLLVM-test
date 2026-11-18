// L19: 信号通道与长周期 timer 同时挂在 select 上。每轮先排程 1h timer，再自发
// SIGUSR1：select 必须走信号分支，timer 分支不得命中且 Stop 返回 true。全部轮次
// 完成后验证反向路径——无信号时 1ms timer 分支先就绪，信号通道没有回调残留。
// 每轮在 select 前后显式 GC 校验 payload 指针链。仅 Linux 执行真实信号路径。
package main

import (
	"fmt"
	"os"
	"os/signal"
	"runtime"
	"strconv"
	"sync/atomic"
	"syscall"
	"time"
)

type node struct {
	v    int64
	next *node
}

var failed int32

const (
	rounds    = 8
	waitLimit = 5 * time.Second
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

func makeChain(base int64) *node {
	return &node{v: base, next: &node{v: base + 1, next: &node{v: base + 2}}}
}

func chainOK(h *node, base int64) bool {
	if h == nil || h.v != base || h.next == nil || h.next.v != base+1 {
		return false
	}
	return h.next.next != nil && h.next.next.v == base+2 && h.next.next.next == nil
}

func main() {
	if runtime.GOOS != "linux" {
		fmt.Printf("SKIP: SIGUSR1 path requires linux (os=%s)\n", runtime.GOOS)
		finish("l19_signal_timer_select")
		return
	}

	seed := envSeed()
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGUSR1)

	signals := 0
	for i := 0; i < rounds; i++ {
		base := int64(i)*43 + seed*41
		chain := makeChain(base)
		timer := time.NewTimer(time.Hour)
		gate := make(chan struct{})
		go func() {
			<-gate
			if err := syscall.Kill(os.Getpid(), syscall.SIGUSR1); err != nil {
				ckf(false, "kill round %d: %v", i, err)
			}
		}()
		close(gate)

		select {
		case s := <-sigCh:
			signals++
			if s != syscall.SIGUSR1 {
				ckf(false, "round %d got signal %v, want SIGUSR1", i, s)
			}
		case <-timer.C:
			ckf(false, "round %d: 1h timer beat signal branch", i)
		case <-time.After(waitLimit):
			ckf(false, "round %d: neither signal nor timer ready", i)
		}
		if !timer.Stop() {
			ckf(false, "round %d: timer fired unexpectedly", i)
		}
		if !chainOK(chain, base) {
			ckf(false, "round %d: payload corrupted", i)
		}
		gcStress()
		if !chainOK(chain, base) {
			ckf(false, "round %d: payload corrupted after GC", i)
		}
	}
	ckf(signals == rounds, "signals=%d want=%d", signals, rounds)

	// 反向路径：没有信号时 1ms timer 必须先就绪。
	t2 := time.NewTimer(time.Millisecond)
	fired := false
	select {
	case <-t2.C:
		fired = true
	case s := <-sigCh:
		ckf(false, "unexpected extra signal %v", s)
	case <-time.After(waitLimit):
		ck(false, "1ms timer did not fire on idle signal channel")
	}
	ck(fired, "timer branch not taken")
	if t2.Stop() {
		ck(false, "Stop on expired timer must return false")
	}
	select {
	case s := <-sigCh:
		ckf(false, "signal channel not drained: %v", s)
	default:
	}

	signal.Stop(sigCh)
	gcStress()
	fmt.Printf("signals=%d rounds=%d\n", signals, rounds)
	finish("l19_signal_timer_select")
}
