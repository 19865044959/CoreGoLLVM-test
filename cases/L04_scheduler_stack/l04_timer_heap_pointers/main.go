// L04: 大量 timer/AfterFunc 闭包捕获独立指针链，回调阻塞在 gate 时显式 GC，
// 放行后校验 timer heap 与回调 goroutine 栈中的指针。
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

var (
	failed int32
	gcSink [][]byte
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
	for i := 0; i < 8; i++ {
		b := make([]byte, 4096)
		b[0] = byte(i)
		gcSink = append(gcSink[:0], b)
	}
	gcSink = gcSink[:0]
	runtime.GC()
	runtime.GC()
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

func timerChain(id int, seed int64) *node {
	base := int64(id)*97 + seed*5
	var head *node
	for j := 2; j >= 0; j-- {
		head = &node{v: base + int64(j), next: head}
	}
	return head
}

func timerChainSum(id int, seed int64) int64 {
	base := int64(id)*97 + seed*5
	return base*3 + 3
}

func timerChainOK(head *node, id int, seed int64) bool {
	base := int64(id)*97 + seed*5
	p := head
	for j := 0; j < 3; j++ {
		if p == nil || p.v != base+int64(j) {
			return false
		}
		p = p.next
	}
	return p == nil
}

func timerRound(seed int64, n int) {
	gate := make(chan struct{})
	var wg sync.WaitGroup
	var fired, bad, stopped int32
	var sum int64
	wantActive := 0

	for i := 0; i < n; i++ {
		id := i
		head := timerChain(id, seed)
		if i%4 == 3 {
			// 被 Stop 的 timer 回调不得执行，但其闭包仍进入过 timer heap。
			t := time.AfterFunc(time.Hour, func() {
				atomic.AddInt32(&stopped, 1)
				runtime.KeepAlive(head)
			})
			if !t.Stop() {
				atomic.AddInt32(&bad, 1)
			}
			continue
		}
		wantActive++
		wg.Add(1)
		time.AfterFunc(time.Millisecond, func() {
			// 所有到期的回调先停在 gate 上，确保 GC 在其执行前完成。
			<-gate
			if timerChainOK(head, id, seed) {
				atomic.AddInt64(&sum, timerChainSum(id, seed))
			} else {
				atomic.AddInt32(&bad, 1)
			}
			atomic.AddInt32(&fired, 1)
			wg.Done()
		})
	}

	gcStress()
	close(gate)
	wg.Wait()

	var exp int64
	for i := 0; i < n; i++ {
		if i%4 == 3 {
			continue
		}
		exp += timerChainSum(i, seed)
	}
	ckf(wantActive > 0, "no active timers")
	ckf(fired == int32(wantActive), "fired=%d want=%d", fired, wantActive)
	ckf(bad == 0, "timer closure/target corrupted or Stop failed")
	ckf(stopped == 0, "stopped timer callback ran")
	ckf(sum == exp, "timer sum=%d want=%d", sum, exp)
	// 回调结束后再 GC，清掉闭包并让下一轮重新填充 timer heap。
	gcStress()
}

func main() {
	seed := envSeed()
	rounds := envInt("TORTURE_ROUNDS", 2, 1, 50)
	const timersPerRound = 128
	for r := 0; r < rounds; r++ {
		timerRound(seed+int64(r)*211, timersPerRound)
	}
	ckf(rounds > 0, "empty configuration")
	finish("l04_timer_heap_pointers")
}
