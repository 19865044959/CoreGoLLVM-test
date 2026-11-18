// L19: time.Ticker 多次 Stop/Reset。Stop 幂等且不关闭 channel；Stop 后
// Reset 必须重新开始投递；并发 Stop 与 pending ticker 下的 GC 均不得
// 破坏 ticker 状态。每次 Reset/Stop 前后显式 GC 并校验 payload 指针链。
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

// drainTicker 排空已缓冲的 tick；返回 channel 是否仍然打开。
func drainTicker(tk *time.Ticker) bool {
	for {
		select {
		case _, ok := <-tk.C:
			if !ok {
				return false
			}
		default:
			return true
		}
	}
}

func receiveTick(tk *time.Ticker, label string) bool {
	select {
	case _, ok := <-tk.C:
		if !ok {
			ck(false, label+": ticker channel closed")
			return false
		}
		return true
	case <-time.After(waitLimit):
		ck(false, label+": ticker did not deliver a tick")
		return false
	}
}

func main() {
	seed := envSeed()
	base := seed*1000 + 1
	holder := makeChain(base)
	ticks := 0

	tk := time.NewTicker(time.Millisecond)

	// 1) 首次 tick 之前 Stop：幂等、不关闭 channel、不 panic。
	tk.Stop()
	tk.Stop()
	ck(drainTicker(tk), "ticker channel closed after first Stop")
	gcStress()
	ck(chainOK(holder, base), "payload corrupted after Stop+GC")

	// 2) 8 轮 Stop/Reset：每次 Reset 后必须收到 tick，Stop 后可重复 Stop。
	for cycle := 0; cycle < 8; cycle++ {
		tk.Reset(time.Millisecond)
		if !receiveTick(tk, "stop-reset cycle") {
			break
		}
		ticks++
		tk.Stop()
		tk.Stop()
		if !drainTicker(tk) {
			ck(false, "ticker channel closed after Stop")
			break
		}
		if cycle%2 == 1 {
			gcStress()
			ck(chainOK(holder, base), "payload corrupted in stop-reset cycle")
		}
	}
	ckf(ticks == 8, "stop-reset ticks=%d want=8 (seed=%d)", ticks, seed)

	// 3) 4 个 goroutine 并发 Stop 同一 ticker：不得 panic/死锁。
	var wg sync.WaitGroup
	for w := 0; w < 4; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 2; i++ {
				tk.Stop()
			}
		}()
	}
	wg.Wait()
	gcStress()
	ck(drainTicker(tk), "ticker channel closed after concurrent Stop")
	ck(chainOK(holder, base), "payload corrupted after concurrent Stop+GC")

	// 4) 并发 Stop 之后 Reset：必须重新开始投递。
	tk.Reset(time.Millisecond)
	if receiveTick(tk, "after-concurrent-stop") {
		ticks++
	}
	tk.Stop()
	ckf(ticks == 9, "total ticks=%d want=9 (seed=%d)", ticks, seed)

	// 5) ticker 处于 pending 状态时 GC，再收 tick。
	tk.Reset(time.Millisecond)
	gcStress()
	if receiveTick(tk, "pending-during-gc") {
		ticks++
	}
	tk.Stop()
	ckf(ticks == 10, "final ticks=%d want=10 (seed=%d)", ticks, seed)
	ck(drainTicker(tk), "ticker channel closed after final Stop")
	ck(chainOK(holder, base), "payload corrupted after final GC")

	fmt.Printf("ticks=%d stop_reset_cycles=8 concurrent_stops=8\n", ticks)
	finish("l19_ticker_stop_reset")
}
