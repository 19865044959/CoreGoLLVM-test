// L19: timer 回调中的 panic 恢复。64 个 AfterFunc 回调在显式 GC 后 panic 一个
// 含指针 payload 的结构，回调内 defer/recover 必须拿到原对象并校验指针链；另有
// 64 个正常回调并发执行，两者都不能被对方的 panic 影响。最后再触发一次
// panic/recover，证明 runtime 定时器路径在恢复后仍可继续工作。
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

type panicPayload struct {
	id   int
	base int64
	head *node
}

var failed int32

const (
	panicTimers = 64
	okTimers    = 64
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

func baseOf(idx int, seed int64) int64 {
	return int64(idx)*109 + seed*59
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

func main() {
	seed := envSeed()
	var wg sync.WaitGroup
	var recovered, missed, okFired, bad int32
	var sum int64
	heldHeads := make([]*node, 0, panicTimers)
	heldBases := make([]int64, 0, panicTimers)

	// 1) 会 panic 的 timer 回调：defer recover 必须接住并校验 payload。
	for i := 0; i < panicTimers; i++ {
		id := i
		base := baseOf(id, seed)
		head := makeChain(base)
		heldHeads = append(heldHeads, head)
		heldBases = append(heldBases, base)
		wg.Add(1)
		time.AfterFunc(time.Millisecond, func() {
			defer func() {
				r := recover()
				if r == nil {
					atomic.AddInt32(&missed, 1)
					wg.Done()
					return
				}
				pp, ok := r.(*panicPayload)
				if !ok || pp.id != id || pp.base != base || !chainOK(pp.head, pp.base) {
					atomic.AddInt32(&bad, 1)
				}
				atomic.AddInt32(&recovered, 1)
				wg.Done()
			}()
			pp := &panicPayload{id: id, base: base, head: head}
			// 回调栈上有 pp.head 指针链，GC 后再 panic。
			gcStress()
			if !chainOK(pp.head, pp.base) {
				atomic.AddInt32(&bad, 1)
			}
			panic(pp)
		})
	}

	// 2) 正常 timer 回调与 panic 回调并发，不得被后者影响。
	for i := 0; i < okTimers; i++ {
		base := baseOf(panicTimers+i, seed)
		head := makeChain(base)
		wg.Add(1)
		time.AfterFunc(time.Millisecond, func() {
			defer wg.Done()
			if !chainOK(head, base) {
				atomic.AddInt32(&bad, 1)
			}
			atomic.AddInt64(&sum, base*3+3)
			atomic.AddInt32(&okFired, 1)
		})
	}
	// timer 仍在堆中时 GC：闭包链与 panic payload 由 timer/闭包保活。
	gcStress()

	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(waitLimit):
		ck(false, "timer callbacks did not all finish")
	}

	var wantSum int64
	for i := 0; i < okTimers; i++ {
		wantSum += baseOf(panicTimers+i, seed)*3 + 3
	}
	ckf(atomic.LoadInt32(&recovered) == panicTimers, "recovered=%d want=%d",
		atomic.LoadInt32(&recovered), panicTimers)
	ckf(atomic.LoadInt32(&missed) == 0, "%d panic callbacks ran without panic", atomic.LoadInt32(&missed))
	ckf(atomic.LoadInt32(&okFired) == okTimers, "ok fired=%d want=%d", atomic.LoadInt32(&okFired), okTimers)
	ckf(atomic.LoadInt32(&bad) == 0, "%d payloads corrupted across panic/recover", atomic.LoadInt32(&bad))
	ckf(atomic.LoadInt64(&sum) == wantSum, "sum=%d want=%d", atomic.LoadInt64(&sum), wantSum)

	// 3) 恢复后 runtime 定时器仍可用：再次 panic 并在 defer 中 recover。
	rec := make(chan struct{}, 1)
	time.AfterFunc(time.Millisecond, func() {
		defer func() {
			if r := recover(); r != nil {
				rec <- struct{}{}
			}
		}()
		panic("final recovered panic")
	})
	select {
	case <-rec:
	case <-time.After(waitLimit):
		ck(false, "timer callback did not recover after previous panics")
	}
	gcStress()
	for i := range heldHeads {
		ckf(chainOK(heldHeads[i], heldBases[i]), "held payload %d corrupted after final GC", i)
	}

	fmt.Printf("panicked=%d recovered=%d ok_timers=%d\n",
		atomic.LoadInt32(&recovered), atomic.LoadInt32(&recovered), atomic.LoadInt32(&okFired))
	finish("l19_timer_panic_recover")
}
