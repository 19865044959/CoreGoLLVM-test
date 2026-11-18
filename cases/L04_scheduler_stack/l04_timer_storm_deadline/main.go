// L04: timer 风暴：同一截止时间批量挂载 AfterFunc，闭包各自捕获一条私有指针链。
// 全部 timer 待触发时 GC，回调执行期间再 GC，校验每个 timer 恰好触发一次且链完整。
package main

import (
	"fmt"
	"os"
	"runtime"
	"strconv"
	"sync/atomic"
	"time"
)

type tnode struct {
	id   int
	v    int64
	next *tnode
}

type timerRec struct {
	head  *tnode
	fired int32
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

func runTimerStorm(round, count int, seed int64) {
	recs := make([]*timerRec, count)
	done := make(chan int, count)
	var bad int32
	var sum int64

	for i := 0; i < count; i++ {
		i := i
		b := int64(round)*1000003 + seed*17 + int64(i)*11
		rec := &timerRec{}
		rec.head = &tnode{id: i, v: b, next: &tnode{id: i, v: b + 1, next: &tnode{id: i, v: b + 2}}}
		recs[i] = rec
		time.AfterFunc(time.Millisecond, func() {
			h := rec.head
			if h == nil || h.next == nil || h.next.next == nil ||
				h.v != b || h.next.v != b+1 || h.next.next.v != b+2 {
				atomic.AddInt32(&bad, 1)
			}
			atomic.AddInt32(&rec.fired, 1)
			done <- i
		})
	}

	// 全部 timer 仍在 timer heap 上、闭包捕获链仅由 timer 引用时 GC。
	gcStress()

	got := 0
	for got < count {
		<-done
		got++
		if got == count/2 {
			// 一半回调执行、一半尚未触发时再次 GC。
			gcStress()
		}
	}
	ckf(got == count, "round %d fired=%d want=%d", round, got, count)

	gcStress()
	for i := 0; i < count; i++ {
		h := recs[i].head
		if h == nil || h.next == nil || h.next.next == nil {
			ckf(false, "round %d timer %d chain missing after GC", round, i)
			continue
		}
		b := int64(round)*1000003 + seed*17 + int64(i)*11
		ckf(atomic.LoadInt32(&recs[i].fired) == 1, "round %d timer %d fired=%d", round, i, recs[i].fired)
		ckf(h.id == i && h.v == b && h.next.v == b+1 && h.next.next.v == b+2,
			"round %d timer %d chain corrupted after GC", round, i)
		sum += h.v + h.next.v + h.next.next.v
	}
	var want int64
	for i := 0; i < count; i++ {
		b := int64(round)*1000003 + seed*17 + int64(i)*11
		want += 3*b + 3
	}
	ckf(sum == want, "round %d sum=%d want=%d", round, sum, want)
	ckf(atomic.LoadInt32(&bad) == 0, "round %d callback chain corrupted", round)
}

func main() {
	seed := envSeed()
	rounds := envInt("TORTURE_ROUNDS", 2, 1, 8)
	timers := envInt("TORTURE_TIMERS", 128, 8, 1024)
	for r := 0; r < rounds; r++ {
		runTimerStorm(r, timers, seed)
	}
	finish("l04_timer_storm_deadline")
}
