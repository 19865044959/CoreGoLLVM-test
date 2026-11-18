// L04: 定时器唤醒 + 轮转公平性：多个 goroutine 按共享 turn 计数严格轮转，
// 每轮在 timer 上阻塞；两阶段之间全部停在 barrier，由主协程 GC 后校验指针。
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

type timerResult struct {
	id  int
	bad int32
	acc int64
}

func main() {
	seed := envSeed()
	workers := envInt("TORTURE_WORKERS", 8, 2, 32)
	mult := envInt("TORTURE_ROUNDS", 2, 1, 20)
	perPhase := 6 * mult // 每个 worker 每阶段参与的轮数

	var mu sync.Mutex
	cond := sync.NewCond(&mu)
	turn := 0

	pause1 := make(chan struct{})
	pause2 := make(chan struct{})
	ready1 := make(chan int, workers)
	ready2 := make(chan int, workers)
	out := make(chan timerResult, workers)

	var wg sync.WaitGroup
	wg.Add(workers)
	for id := 0; id < workers; id++ {
		go func(id int) {
			defer wg.Done()
			base := int64(id)*1000 + seed*31
			head := &node{v: base, next: &node{v: base + 7, next: &node{v: base + 14}}}
			want := base*3 + 21
			var acc int64
			var bad int32
			verify := func() {
				var sum int64
				n := 0
				for p := head; p != nil; p = p.next {
					sum += p.v
					n++
				}
				if n != 3 || sum != want {
					bad = 1
				}
			}
			runTurn := func(k int) {
				mu.Lock()
				for turn%workers != id {
					cond.Wait()
				}
				turn++
				mu.Unlock()
				// 持有本轮资格时在 timer 上阻塞，模拟 deadline 驱动的唤醒。
				d := time.Duration(1+(int(seed)+id+k)%3) * time.Millisecond
				timer := time.NewTimer(d)
				<-timer.C
				verify()
				acc += want + int64(k)
				mu.Lock()
				cond.Broadcast()
				mu.Unlock()
			}
			for k := 0; k < perPhase; k++ {
				runTurn(k)
			}
			ready1 <- id
			<-pause1
			verify()
			for k := perPhase; k < 2*perPhase; k++ {
				runTurn(k)
			}
			ready2 <- id
			<-pause2
			verify()
			out <- timerResult{id: id, bad: bad, acc: acc}
		}(id)
	}

	for i := 0; i < workers; i++ {
		<-ready1
	}
	gcStress()
	close(pause1)
	for i := 0; i < workers; i++ {
		<-ready2
	}
	gcStress()
	close(pause2)
	wg.Wait()
	close(out)

	mu.Lock()
	gotTurn := turn
	mu.Unlock()
	ckf(gotTurn == workers*2*perPhase, "turn count=%d want=%d", gotTurn, workers*2*perPhase)

	seen := make([]bool, workers)
	var total int64
	for r := range out {
		if r.id < 0 || r.id >= workers {
			ckf(false, "bad worker id %d", r.id)
			continue
		}
		seen[r.id] = true
		base := int64(r.id)*1000 + seed*31
		want := base*3 + 21
		// 每个 worker 两阶段各执行 perPhase 轮，局部 k 恰好覆盖 0..2*perPhase-1。
		wantAcc := int64(2*perPhase)*want + int64(perPhase*(2*perPhase-1))
		ckf(r.bad == 0, "worker %d pointer chain corrupted", r.id)
		ckf(r.acc == wantAcc, "worker %d acc=%d want=%d", r.id, r.acc, wantAcc)
		total += r.acc
	}
	for id, ok := range seen {
		ckf(ok, "worker %d missing result", id)
	}
	ckf(total != 0, "empty accumulator")
	finish("l04_timer_fairness")
}
