// L19: runtime timer 堆的“增长 -> 取消收缩 -> 再次增长”。第一波批量挂入长周期
// AfterFunc（堆增长），全部 Stop 后显式 GC（堆收缩），再挂入一批短周期 timer
// 并等待全部到期；被取消的长 timer 回调一次都不能执行，短 timer 的指针 payload
// 在 GC 前后必须完好。TORTURE_ROUNDS 控制轮数，默认 1 轮。
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

const (
	growTimers  = 4096
	shortTimers = 512
	keptChains  = 16
	waitLimit   = 15 * time.Second
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

// gcStress 只使用局部 sink 与 KeepAlive，可安全并发调用。
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

func chainOK(head *node, base int64) bool {
	if head == nil || head.v != base || head.next == nil || head.next.v != base+1 {
		return false
	}
	return head.next.next != nil && head.next.next.v == base+2 && head.next.next.next == nil
}

func round(seed int64) {
	// 阶段 1：timer 堆增长。长周期 timer 的闭包各自捕获一条指针链。
	var longFired int32
	timers := make([]*time.Timer, growTimers)
	heldHeads := make([]*node, 0, keptChains)
	heldBases := make([]int64, 0, keptChains)
	for i := 0; i < growTimers; i++ {
		base := int64(i)*131 + seed*17
		head := makeChain(base)
		if i < keptChains {
			heldHeads = append(heldHeads, head)
			heldBases = append(heldBases, base)
		}
		timers[i] = time.AfterFunc(time.Hour, func() {
			atomic.AddInt32(&longFired, 1)
			// 保持闭包捕获，回调若被错误触发则 payload 也必须可达。
			if !chainOK(head, base) {
				atomic.AddInt32(&failed, 1)
			}
		})
	}
	// timer 全部在堆中时 GC：闭包链只能由 timer heap 保活。
	gcStress()
	for i := range heldHeads {
		ckf(chainOK(heldHeads[i], heldBases[i]), "long timer chain %d corrupted while pending", i)
	}

	// 阶段 2：取消并收缩。全部 Stop 后丢弃 timer 引用并 GC。
	stopped := 0
	for _, t := range timers {
		if t.Stop() {
			stopped++
		}
	}
	ckf(stopped == growTimers, "stopped=%d want=%d", stopped, growTimers)
	timers = nil
	gcStress()
	gcStress()
	ckf(atomic.LoadInt32(&longFired) == 0, "canceled long timers fired %d times", atomic.LoadInt32(&longFired))
	for i := range heldHeads {
		ckf(chainOK(heldHeads[i], heldBases[i]), "long timer chain %d corrupted after shrink+GC", i)
	}

	// 阶段 3：收缩后重新增长。短周期 timer 必须全部按时到期。
	var wg sync.WaitGroup
	var shortFired, bad int32
	var sum int64
	for i := 0; i < shortTimers; i++ {
		base := int64(growTimers+i)*131 + seed*17
		head := makeChain(base)
		wg.Add(1)
		time.AfterFunc(time.Millisecond, func() {
			if !chainOK(head, base) {
				atomic.AddInt32(&bad, 1)
			}
			atomic.AddInt64(&sum, base*3+3)
			if atomic.AddInt32(&shortFired, 1) == shortTimers/2 {
				// 到期回调栈上仍持有闭包链，此时 GC 覆盖栈扫描路径。
				gcStress()
			}
			wg.Done()
		})
	}
	gcStress()

	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(waitLimit):
		ck(false, "regrown short timers did not all fire")
		return
	}

	var wantSum int64
	for i := 0; i < shortTimers; i++ {
		base := int64(growTimers+i)*131 + seed*17
		wantSum += base*3 + 3
	}
	ckf(atomic.LoadInt32(&shortFired) == shortTimers, "short fired=%d want=%d",
		atomic.LoadInt32(&shortFired), shortTimers)
	ckf(atomic.LoadInt32(&bad) == 0, "%d short timer payloads corrupted", atomic.LoadInt32(&bad))
	ckf(atomic.LoadInt64(&sum) == wantSum, "sum=%d want=%d", atomic.LoadInt64(&sum), wantSum)
	ckf(atomic.LoadInt32(&longFired) == 0, "canceled long timers fired after regrow: %d",
		atomic.LoadInt32(&longFired))

	gcStress()
	for i := range heldHeads {
		ckf(chainOK(heldHeads[i], heldBases[i]), "long timer chain %d corrupted at round end", i)
	}
}

func main() {
	seed := envSeed()
	rounds := envInt("TORTURE_ROUNDS", 1, 1, 10)
	fmt.Printf("long_timers=%d short_timers=%d rounds=%d\n", growTimers, shortTimers, rounds)
	for r := 0; r < rounds; r++ {
		round(seed + int64(r)*211)
	}
	finish("l19_timer_heap_grow_shrink")
}
