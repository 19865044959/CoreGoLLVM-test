// L19: 数千个 time.AfterFunc 闭包各自捕获一条独立指针链；全部到期回调先阻塞在
// gate 上，主协程在此时分配垃圾并显式 GC，放行后逐条校验 timer heap 与回调
// goroutine 栈上的指针内容。
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
	baseTimers = 2048
	waitLimit  = 15 * time.Second
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

// gcStress 只使用局部 sink 与 KeepAlive，可在并发 goroutine 中安全调用。
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

// makeChain 返回 3 节点指针链与期望值 base/b+1/b+2。
func makeChain(id int, seed int64) (*node, int64) {
	base := int64(id)*131 + seed*17
	tail := &node{v: base + 2}
	mid := &node{v: base + 1, next: tail}
	return &node{v: base, next: mid}, base
}

func chainOK(head *node, base int64) bool {
	if head == nil || head.v != base {
		return false
	}
	if head.next == nil || head.next.v != base+1 {
		return false
	}
	if head.next.next == nil || head.next.next.v != base+2 {
		return false
	}
	return head.next.next.next == nil
}

func timerRound(seed int64, n int) {
	gate := make(chan struct{})
	var wg sync.WaitGroup
	var fired, bad int32
	var sum int64

	for i := 0; i < n; i++ {
		id := i
		head, base := makeChain(id, seed)
		wg.Add(1)
		time.AfterFunc(time.Millisecond, func() {
			// 到期回调先停在 gate：闭包捕获链同时被 timer heap、闭包对象
			// 和回调 goroutine 栈引用。
			<-gate
			if !chainOK(head, base) {
				atomic.AddInt32(&bad, 1)
			}
			atomic.AddInt64(&sum, base*3+3)
			atomic.AddInt32(&fired, 1)
			wg.Done()
		})
	}

	// timer 刚进堆时 GC：大部分 timer 尚未到期，闭包链只能由 timer heap 保活。
	gcStress()

	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()
	close(gate)
	select {
	case <-done:
	case <-time.After(waitLimit):
		ck(false, "timers did not all fire")
		return
	}

	var want int64
	for i := 0; i < n; i++ {
		base := int64(i)*131 + seed*17
		want += base*3 + 3
	}
	ckf(atomic.LoadInt32(&fired) == int32(n), "fired=%d want=%d", atomic.LoadInt32(&fired), n)
	ckf(atomic.LoadInt32(&bad) == 0, "%d timer payload chains corrupted", atomic.LoadInt32(&bad))
	ckf(atomic.LoadInt64(&sum) == want, "sum=%d want=%d", atomic.LoadInt64(&sum), want)

	// 回调结束后的再次 GC，让下一轮重新填充 timer heap。
	gcStress()
}

func main() {
	seed := envSeed()
	rounds := envInt("TORTURE_ROUNDS", 1, 1, 20)
	fmt.Printf("timers=%d rounds=%d\n", baseTimers, rounds)
	for r := 0; r < rounds; r++ {
		timerRound(seed+int64(r)*211, baseTimers)
	}
	finish("l19_timer_heap_many")
}
