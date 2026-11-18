// L04: 并发池限流。8 个 worker + 容量 4 的并发放行闸门，外加固定窗口令牌：
// 协调者每窗口只发 K=6 个 token，收齐 K 个开跑确认后才发下一窗口。校验峰值
// 并发<=4、每窗口恰好 6 个任务、窗口顺序单调、总任务不重不漏；运行中显式 GC。
package main

import (
	"fmt"
	"os"
	"runtime"
	"strconv"
	"sync"
	"sync/atomic"
)

type fwNode struct {
	v    int64
	next *fwNode
}

type fwJob struct {
	id   int
	win  int
	head *fwNode
	sum  int64
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

func finish(name string, seed int64) {
	if n := atomic.LoadInt32(&failed); n != 0 {
		fmt.Printf("FAILED %s (UAF_SEED=%d, %d failed checks)\n", name, seed, n)
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

func envRounds() int {
	s := os.Getenv("TORTURE_ROUNDS")
	if s == "" {
		return 1
	}
	v, err := strconv.Atoi(s)
	if err != nil || v < 1 {
		return 1
	}
	return v
}

func main() {
	seed := envSeed()
	windows := 6 * envRounds()
	const (
		workers   = 8
		capacity  = 4
		perWindow = 6
		workIters = 512
	)
	total := windows * perWindow

	var (
		active  int32
		peak    int32
		armed   int32
		doneCnt int32
	)
	winCounts := make([]int32, windows)
	gate := make(chan int, perWindow)
	sem := make(chan struct{}, capacity)
	starts := make(chan int, total)
	jobs := make(chan *fwJob, total)
	runGate := make(chan struct{})
	coordDone := make(chan struct{})

	// 协调者：逐窗口发 token，收齐开跑确认后再发下一窗口。奇数窗口后 GC。
	go func() {
		defer close(coordDone)
		for w := 0; w < windows; w++ {
			for i := 0; i < perWindow; i++ {
				gate <- w
			}
			for i := 0; i < perWindow; i++ {
				s := <-starts
				ckf(s == w, "start ack window=%d want=%d", s, w)
			}
			if w%2 == 1 {
				gcStress()
			}
		}
	}()

	allJobs := make([]*fwJob, total)
	for id := range allJobs {
		b := seed*100003 + int64(id)*977
		allJobs[id] = &fwJob{id: id, head: &fwNode{v: b, next: &fwNode{v: b + 1, next: &fwNode{v: b + 2}}}}
	}

	var wg sync.WaitGroup
	wg.Add(workers)
	for w := 0; w < workers; w++ {
		go func() {
			defer wg.Done()
			for j := range jobs {
				sem <- struct{}{}
				win := <-gate
				j.win = win
				atomic.AddInt32(&winCounts[win], 1)
				cur := atomic.AddInt32(&active, 1)
				for {
					p := atomic.LoadInt32(&peak)
					if cur <= p || atomic.CompareAndSwapInt32(&peak, p, cur) {
						break
					}
				}
				starts <- win
				atomic.AddInt32(&armed, 1)
				<-runGate // 前 capacity 个任务在此阻塞，供主协程 GC
				var sum int64
				for i := 0; i < workIters; i++ {
					sum += j.head.v + j.head.next.v + j.head.next.next.v + int64(i%7)
				}
				j.sum = sum
				if j.head.v+1 != j.head.next.v || j.head.next.v+1 != j.head.next.next.v {
					ckf(false, "job %d chain corrupted while rate-limited", j.id)
				}
				atomic.AddInt32(&active, -1)
				atomic.AddInt32(&doneCnt, 1)
				<-sem
			}
		}()
	}

	for _, j := range allJobs {
		jobs <- j
	}
	close(jobs)
	// 等到 capacity 个任务已持闸门后在跑前状态 GC，再整体放行。
	spins := 0
	for atomic.LoadInt32(&armed) < capacity {
		runtime.Gosched()
		spins++
		if spins > 4000000 {
			ckf(false, "only %d tasks armed, want %d", atomic.LoadInt32(&armed), capacity)
			break
		}
	}
	gcStress()
	close(runGate)
	wg.Wait()
	<-coordDone

	ckf(atomic.LoadInt32(&doneCnt) == int32(total), "done=%d want=%d", atomic.LoadInt32(&doneCnt), total)
	peakVal := atomic.LoadInt32(&peak)
	ckf(peakVal <= capacity, "peak concurrency=%d exceeds limit=%d", peakVal, capacity)
	ckf(peakVal >= 1, "peak concurrency=%d want>=1", peakVal)
	for w := 0; w < windows; w++ {
		ckf(atomic.LoadInt32(&winCounts[w]) == perWindow, "window %d count=%d want=%d", w, winCounts[w], perWindow)
	}
	for _, j := range allJobs {
		want := int64(0)
		for i := 0; i < workIters; i++ {
			want += (seed*100003+int64(j.id)*977)*3 + 3 + int64(i%7)
		}
		ckf(j.sum == want, "job %d sum mismatch window=%d", j.id, j.win)
		ckf(j.win >= 0 && j.win < windows, "job %d invalid window=%d", j.id, j.win)
	}
	ckf(atomic.LoadInt32(&active) == 0, "active=%d at shutdown", active)

	gcStress()
	for _, j := range allJobs {
		b := seed*100003 + int64(j.id)*977
		ok := j.head != nil && j.head.v == b && j.head.next != nil && j.head.next.v == b+1 &&
			j.head.next.next != nil && j.head.next.next.v == b+2
		ckf(ok, "job %d retained chain corrupted after final GC", j.id)
	}
	finish("l04_pool_fixed_window_limit", seed)
}
