// L19: Timer.Stop/Reset 语义：Stop 后不触发、Reset 后再次触发；已触发且已读
// 的 timer 再 Stop 返回 false。Stop/Reset 之间穿插显式 GC 与指针 payload 校验。
package main

import (
	"fmt"
	"os"
	"runtime"
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

func makeChain(base int64) *node {
	return &node{v: base, next: &node{v: base + 1, next: &node{v: base + 2}}}
}

func chainOK(head *node, base int64) bool {
	if head == nil || head.v != base || head.next == nil || head.next.v != base+1 {
		return false
	}
	return head.next.next != nil && head.next.next.v == base+2 && head.next.next.next == nil
}

// waitFire 等待 timer.C 或超时；返回是否收到。
func waitFire(t *time.Timer, what string) bool {
	select {
	case <-t.C:
		return true
	case <-time.After(waitLimit):
		ck(false, what+": timer did not fire")
		return false
	}
}

func main() {
	// 1) Stop 尚未到期的 timer：返回 true，不再触发；Reset 后可再次触发。
	p := makeChain(100)
	t := time.NewTimer(time.Hour)
	ck(t.Stop(), "Stop on fresh timer returned false")
	select {
	case <-t.C:
		ck(false, "stopped timer delivered a value")
	default:
	}
	gcStress()
	ck(chainOK(p, 100), "payload corrupted after Stop+GC")
	t.Reset(time.Millisecond)
	ck(waitFire(t, "reset-after-stop"), "reset-after-stop: no fire")
	gcStress()
	ck(!t.Stop(), "Stop on expired+drained timer returned true")
	ck(chainOK(p, 100), "payload corrupted after fire+GC")

	// 2) 反复 Reset：每次先读空 channel 再 Reset，每次必须恰好触发一次。
	t2 := time.NewTimer(time.Hour)
	ck(t2.Stop(), "Stop fresh timer 2")
	fires := 0
	for i := 0; i < 8; i++ {
		t2.Reset(time.Millisecond)
		if !waitFire(t2, "reset-loop") {
			break
		}
		fires++
		gcStress()
	}
	ckf(fires == 8, "reset loop fired=%d want=8", fires)
	ck(!t2.Stop(), "Stop after reset loop returned true")

	// 3) 未 Stop 直接 Reset 活跃 timer：仍只触发一次，不重复投递。
	t3 := time.NewTimer(time.Hour)
	t3.Reset(time.Millisecond)
	ck(waitFire(t3, "reset-active"), "reset-active: no fire")
	select {
	case <-t3.C:
		ck(false, "reset-active: duplicate delivery")
	default:
	}
	gcStress()
	ck(!t3.Stop(), "Stop on reset-active timer returned true")

	// 4) AfterFunc：Stop 阻止回调，Reset 重新武装并保留闭包捕获的指针。
	var fired int32
	pf := makeChain(200)
	cb := make(chan struct{}, 4)
	t4 := time.AfterFunc(time.Hour, func() {
		if !chainOK(pf, 200) {
			atomic.AddInt32(&failed, 1)
		}
		atomic.AddInt32(&fired, 1)
		cb <- struct{}{}
	})
	ck(t4.Stop(), "Stop fresh AfterFunc returned false")
	gcStress()
	ck(!t4.Reset(time.Millisecond), "Reset of stopped AfterFunc returned true")
	select {
	case <-cb:
	case <-time.After(waitLimit):
		ck(false, "AfterFunc did not fire after Reset")
	}
	ckf(atomic.LoadInt32(&fired) == 1, "AfterFunc fired=%d want=1", atomic.LoadInt32(&fired))
	gcStress()
	ck(chainOK(pf, 200), "AfterFunc payload corrupted after GC")
	ck(!t4.Reset(time.Millisecond), "Reset of fired AfterFunc returned true")
	select {
	case <-cb:
	case <-time.After(waitLimit):
		ck(false, "AfterFunc did not fire after second Reset")
	}
	ckf(atomic.LoadInt32(&fired) == 2, "AfterFunc fired=%d want=2", atomic.LoadInt32(&fired))
	gcStress()
	ck(chainOK(pf, 200), "AfterFunc payload corrupted after final GC")

	fmt.Println("stop_reset_checks=4")
	finish("l19_timer_stop_reset")
}
