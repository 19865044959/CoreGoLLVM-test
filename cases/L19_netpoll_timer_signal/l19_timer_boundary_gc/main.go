// L19: AfterFunc 闭包是 payload 的唯一引用。闭包在 timer heap 中排队期间执行
// 多轮显式 GC，到期回调仍必须读到正确指针链；回调逐个回报 id，主协程校验
// 触发次数、唯一性与内容。
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

type result struct {
	id  int
	sum int64
	ok  bool
}

var failed int32

const (
	nTimers   = 64
	waitLimit = 10 * time.Second
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

//go:noinline
func schedule(id int, seed int64, out chan<- result) {
	base := int64(id)*53 + seed*11
	// 指针链只在闭包内被引用；函数返回后，timer heap 是唯一保活来源。
	head := &node{v: base, next: &node{v: base + 1, next: &node{v: base + 2}}}
	time.AfterFunc(250*time.Millisecond, func() {
		h := head
		ok := h != nil && h.v == base &&
			h.next != nil && h.next.v == base+1 &&
			h.next.next != nil && h.next.next.v == base+2 &&
			h.next.next.next == nil
		out <- result{id: id, sum: base*3 + 3, ok: ok}
	})
}

func main() {
	seed := envSeed()
	out := make(chan result, nTimers)
	for i := 0; i < nTimers; i++ {
		schedule(i, seed, out)
	}

	// timer 尚在堆中排队时连续 GC；闭包与捕获链只能由 timer heap 保活。
	gcStress()
	gcStress()
	gcStress()

	// 额外的堆垃圾，迫使 GC 完整扫描 timer 堆。
	junk := make([][]byte, 0, 256)
	for i := 0; i < 256; i++ {
		junk = append(junk, make([]byte, 8192))
	}
	runtime.GC()
	runtime.KeepAlive(junk)

	seen := make([]bool, nTimers)
	got := 0
	for got < nTimers {
		select {
		case r := <-out:
			got++
			if r.id < 0 || r.id >= nTimers {
				ckf(false, "callback id out of range: %d", r.id)
				continue
			}
			ckf(!seen[r.id], "duplicate callback for timer %d", r.id)
			seen[r.id] = true
			ckf(r.ok, "timer %d payload corrupted after GC", r.id)
			want := (int64(r.id)*53+seed*11)*3 + 3
			ckf(r.sum == want, "timer %d sum=%d want=%d", r.id, r.sum, want)
		case <-time.After(waitLimit):
			ckf(false, "only %d/%d timers fired", got, nTimers)
			got = nTimers
		}
	}
	for i, ok := range seen {
		ckf(ok, "timer %d never fired", i)
	}

	// 回调结束后 timer heap 释放闭包；再次 GC 并确认没有重复回调。
	gcStress()
	select {
	case r := <-out:
		ckf(false, "extra callback after completion: id=%d", r.id)
	default:
	}

	fmt.Printf("timers=%d\n", nTimers)
	finish("l19_timer_boundary_gc")
}
