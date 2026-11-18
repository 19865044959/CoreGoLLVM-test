// L19: 多个 timer 的并发取消。128 个 1ms AfterFunc 回调启动后阻塞在 gate
// 上（此时显式 GC），另外 512 个 1 小时 timer 由 4 个 goroutine 全部 Stop。
// 放行后已触发的回调必须恰好执行 128 次，被取消的回调必须为 0 次。
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
	const fireN = 128
	const cancelN = 512
	const workers = 4

	gate := make(chan struct{})
	ready := make(chan struct{}, fireN)
	done := make(chan struct{}, fireN)
	var fireCount int32
	var fireBad int32
	var cancelFired int32

	fireChains := make([]*node, 0, fireN)
	for i := 0; i < fireN; i++ {
		b := seed*100000 + int64(i)*3 + 1
		p := makeChain(b)
		fireChains = append(fireChains, p)
		time.AfterFunc(time.Millisecond, func() {
			if !chainOK(p, b) {
				atomic.StoreInt32(&fireBad, 1)
			}
			atomic.AddInt32(&fireCount, 1)
			ready <- struct{}{}
			<-gate
			done <- struct{}{}
		})
	}

	// 等全部回调进入阻塞态：此时 payload 同时被 timer 回调栈与主协程引用。
	for i := 0; i < fireN; i++ {
		select {
		case <-ready:
		case <-time.After(waitLimit):
			ckf(false, "only %d/%d callbacks started (seed=%d)", i, fireN, seed)
			i = fireN
		}
	}
	gcStress()

	// 512 个长周期 timer：4 个 goroutine 并发 Stop，全部必须成功。
	cancelTimers := make([]*time.Timer, cancelN)
	cancelChains := make([]*node, cancelN)
	for i := 0; i < cancelN; i++ {
		b := seed*100000 + 20000 + int64(i)*3
		p := makeChain(b)
		cancelChains[i] = p
		cancelTimers[i] = time.AfterFunc(time.Hour, func() {
			atomic.AddInt32(&cancelFired, 1)
		})
	}
	gcStress()

	var stopped int32
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		lo, hi := w*cancelN/workers, (w+1)*cancelN/workers
		wg.Add(1)
		go func(lo, hi int) {
			defer wg.Done()
			for i := lo; i < hi; i++ {
				if cancelTimers[i].Stop() {
					atomic.AddInt32(&stopped, 1)
				}
			}
		}(lo, hi)
	}
	wg.Wait()
	ckf(stopped == cancelN, "stopped=%d want=%d (seed=%d)", stopped, cancelN, seed)

	extra := 0
	for i := 0; i < cancelN; i += 53 {
		if cancelTimers[i].Stop() {
			extra++
		}
	}
	ckf(extra == 0, "double Stop returned true %d times (seed=%d)", extra, seed)

	// 回调仍全部阻塞时再次 GC，然后放行。
	gcStress()
	close(gate)
	for i := 0; i < fireN; i++ {
		select {
		case <-done:
		case <-time.After(waitLimit):
			ckf(false, "only %d/%d callbacks finished (seed=%d)", i, fireN, seed)
			i = fireN
		}
	}
	gcStress()

	ckf(atomic.LoadInt32(&fireCount) == fireN, "fired=%d want=%d (seed=%d)",
		atomic.LoadInt32(&fireCount), fireN, seed)
	ckf(atomic.LoadInt32(&fireBad) == 0, "fired payload corrupted (seed=%d)", seed)
	ckf(atomic.LoadInt32(&cancelFired) == 0, "canceled timers fired=%d want=0 (seed=%d)",
		atomic.LoadInt32(&cancelFired), seed)

	// 触发后再等一个哨兵 timer，确认取消的 timer 没有迟到回调。
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
		ck(false, "sentinel timer did not fire")
	}
	gcStress()
	ckf(atomic.LoadInt32(&cancelFired) == 0, "canceled timers fired after sentinel=%d (seed=%d)",
		atomic.LoadInt32(&cancelFired), seed)

	badFire := 0
	for i, p := range fireChains {
		if !chainOK(p, seed*100000+int64(i)*3+1) {
			badFire++
		}
	}
	badCancel := 0
	for i := 0; i < cancelN; i += 11 {
		if !chainOK(cancelChains[i], seed*100000+20000+int64(i)*3) {
			badCancel++
		}
	}
	ckf(badFire == 0, "fired payload chains corrupted: bad=%d (seed=%d)", badFire, seed)
	ckf(badCancel == 0, "canceled payload chains corrupted: bad=%d (seed=%d)", badCancel, seed)

	fmt.Printf("fired=%d canceled=%d\n", fireN, cancelN)
	finish("l19_multi_timer_cancel")
}
