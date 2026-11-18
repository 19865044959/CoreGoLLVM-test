// L19: time.AfterFunc 在零/负时长下的回调内自重置。零延时回调每次触发都用
// Reset(0) 重新武装自身，直到固定次数；对已到期（正在回调）的 timer，Reset
// 必须返回 false 并重新立即排程，最后 Stop 返回 false。负时长路径同理。
// 回调内与主协程各显式 GC，校验闭包捕获的三节点指针链。
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
	zeroRearms = 32
	negRearms  = 8
	waitLimit  = 5 * time.Second
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

func waitClose(ch <-chan struct{}, what string) bool {
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

	// 阶段 1：零延时回调内 Reset(0) 自重置 zeroRearms 次。
	base1 := seed*41 + 7
	chain1 := makeChain(base1)
	var fired1, resetFalse1, extra1 int32
	armed1 := make(chan struct{})
	done1 := make(chan struct{})
	var t1 *time.Timer
	t1 = time.AfterFunc(0, func() {
		<-armed1
		n := atomic.AddInt32(&fired1, 1)
		if n > zeroRearms {
			atomic.AddInt32(&extra1, 1)
			return
		}
		if !chainOK(chain1, base1) {
			ckf(false, "zero callback %d payload corrupted", n)
		}
		if n%8 == 0 {
			gcStress()
			if !chainOK(chain1, base1) {
				ckf(false, "zero callback %d payload corrupted after GC", n)
			}
		}
		if n < zeroRearms {
			if !t1.Reset(0) {
				atomic.AddInt32(&resetFalse1, 1)
			}
			return
		}
		if t1.Stop() {
			ckf(false, "Stop on fired zero timer returned true at callback %d", n)
		}
		close(done1)
	})
	close(armed1)
	if waitClose(done1, "zero self-reset chain") {
		ckf(atomic.LoadInt32(&fired1) == zeroRearms, "zero fired=%d want=%d", atomic.LoadInt32(&fired1), zeroRearms)
		ckf(atomic.LoadInt32(&resetFalse1) == zeroRearms-1, "zero resetFalse=%d want=%d", atomic.LoadInt32(&resetFalse1), zeroRearms-1)
		if t1.Stop() {
			ck(false, "Stop after zero chain done must return false")
		}
		gcStress()
		ck(chainOK(chain1, base1), "zero payload chain corrupted after GC")
	}

	// 阶段 2：负延时回调内 Reset(负值) 自重置 negRearms 次。
	base2 := seed*43 + 11
	chain2 := makeChain(base2)
	var fired2, resetFalse2, extra2 int32
	armed2 := make(chan struct{})
	done2 := make(chan struct{})
	var t2 *time.Timer
	t2 = time.AfterFunc(-time.Second, func() {
		<-armed2
		n := atomic.AddInt32(&fired2, 1)
		if n > negRearms {
			atomic.AddInt32(&extra2, 1)
			return
		}
		if !chainOK(chain2, base2) {
			ckf(false, "negative callback %d payload corrupted", n)
		}
		if n%4 == 0 {
			gcStress()
			if !chainOK(chain2, base2) {
				ckf(false, "negative callback %d payload corrupted after GC", n)
			}
		}
		if n < negRearms {
			if !t2.Reset(-time.Second) {
				atomic.AddInt32(&resetFalse2, 1)
			}
			return
		}
		if t2.Stop() {
			ckf(false, "Stop on fired negative timer returned true at callback %d", n)
		}
		close(done2)
	})
	close(armed2)
	if waitClose(done2, "negative self-reset chain") {
		ckf(atomic.LoadInt32(&fired2) == negRearms, "negative fired=%d want=%d", atomic.LoadInt32(&fired2), negRearms)
		ckf(atomic.LoadInt32(&resetFalse2) == negRearms-1, "negative resetFalse=%d want=%d", atomic.LoadInt32(&resetFalse2), negRearms-1)
		gcStress()
		ck(chainOK(chain2, base2), "negative payload chain corrupted after GC")
	}

	// 阶段 3：哨兵零 timer 触发后再 GC，确认没有多余回调。
	sent := make(chan struct{})
	time.AfterFunc(0, func() { close(sent) })
	waitClose(sent, "sentinel zero timer")
	gcStress()
	ckf(atomic.LoadInt32(&extra1) == 0, "zero extra callbacks=%d", atomic.LoadInt32(&extra1))
	ckf(atomic.LoadInt32(&extra2) == 0, "negative extra callbacks=%d", atomic.LoadInt32(&extra2))

	fmt.Printf("zero_rearms=%d negative_rearms=%d\n", zeroRearms, negRearms)
	finish("l19_afterfunc_self_reset_zero")
}
