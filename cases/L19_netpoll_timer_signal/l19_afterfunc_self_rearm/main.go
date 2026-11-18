// L19: time.AfterFunc 回调内部对自身 timer 执行 Reset/Stop。回调每触发一次
// 就重新武装自己，直到固定次数；在已到期（正在回调）的 timer 上 Reset/Stop
// 必须返回 false 并重新排程/无效。闭包捕获的指针链在定时器堆、回调 goroutine
// 栈与显式 GC 之间必须保持正确。固定计数协调，不依赖墙钟阈值。
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
	rearmCount = 64
	waitLimit  = 10 * time.Second
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

func waitCh(ch <-chan struct{}, what string) bool {
	select {
	case <-ch:
		return true
	case <-time.After(waitLimit):
		ckf(false, "%s: timeout", what)
		return false
	}
}

func main() {
	seed := envSeed()

	// 阶段 1：回调内自我 Reset，固定触发 rearmCount 次。
	base1 := seed*19 + 3
	chain1 := makeChain(base1)
	var fired int32
	var resetFalse int32
	var first *time.Timer
	armed := make(chan struct{})
	done := make(chan struct{})
	first = time.AfterFunc(time.Millisecond, func() {
		<-armed // 保证 first 已完成赋值
		n := atomic.AddInt32(&fired, 1)
		if !chainOK(chain1, base1) {
			ckf(false, "rearm callback %d payload corrupted", n)
		}
		if n%8 == 0 {
			gcStress()
			if !chainOK(chain1, base1) {
				ckf(false, "rearm callback %d payload corrupted after GC", n)
			}
		}
		if n < rearmCount {
			// 回调正在执行，timer 已到期：Reset 必须返回 false 并重新武装。
			if !first.Reset(time.Millisecond) {
				atomic.AddInt32(&resetFalse, 1)
			}
			return
		}
		// 已到期的 timer 无法被 Stop：返回 false 且不再排程。
		if first.Stop() {
			ckf(false, "Stop on fired timer returned true at callback %d", n)
		}
		close(done)
	})
	close(armed)
	if waitCh(done, "self-rearming timer") {
		ckf(atomic.LoadInt32(&fired) == rearmCount, "fired=%d want=%d", atomic.LoadInt32(&fired), rearmCount)
		ckf(atomic.LoadInt32(&resetFalse) == rearmCount-1, "resetFalse=%d want=%d", atomic.LoadInt32(&resetFalse), rearmCount-1)
		gcStress()
		ck(chainOK(chain1, base1), "payload chain corrupted after rearm loop")
	}

	// 阶段 2：回调内 Stop 自身，再由外部 Reset 重新触发一次。
	base2 := seed*23 + 7
	chain2 := makeChain(base2)
	var fired2 int32
	var second *time.Timer
	armed2 := make(chan struct{})
	firstFire := make(chan struct{})
	secondDone := make(chan struct{})
	second = time.AfterFunc(time.Millisecond, func() {
		<-armed2
		n := atomic.AddInt32(&fired2, 1)
		if !chainOK(chain2, base2) {
			ckf(false, "self-stop callback %d payload corrupted", n)
		}
		if second.Stop() {
			ckf(false, "Stop inside fired callback %d returned true", n)
		}
		if n == 1 {
			close(firstFire)
			return
		}
		if n == 2 {
			close(secondDone)
		}
	})
	close(armed2)
	if waitCh(firstFire, "first self-stop callback") {
		if second.Reset(time.Millisecond) {
			ck(false, "Reset on expired timer must return false")
		}
	}
	if waitCh(secondDone, "second self-stop callback") {
		ckf(atomic.LoadInt32(&fired2) == 2, "fired2=%d want=2", atomic.LoadInt32(&fired2))
		gcStress()
		ck(chainOK(chain2, base2), "payload chain corrupted after self-stop loop")
	}

	finish("l19_afterfunc_self_rearm")
}
