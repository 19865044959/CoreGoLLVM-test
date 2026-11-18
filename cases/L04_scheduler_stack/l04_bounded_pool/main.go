// L04: 有界并发池：固定 worker + 有界任务 channel + 容量受限的 semaphore。
// worker 全被 semaphore 挡住时主协程 GC，并校验并发峰值不超限且指针链完整。
package main

import (
	"fmt"
	"os"
	"runtime"
	"strconv"
	"sync"
	"sync/atomic"
)

type item struct {
	id   int
	v    int64
	next *item
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

type poolResult struct {
	id  int
	bad int32
}

func runPool(r int, seed int64, jobCount int, workers, limit, queue int) {
	jobs := make(chan *item, queue)
	sem := make(chan struct{}, limit)
	held := make(chan struct{})
	release := make(chan struct{})
	waiting := make(chan struct{}, workers)
	finalReady := make(chan struct{}, workers)
	finalRelease := make(chan struct{})
	out := make(chan poolResult, workers*(jobCount/workers+2))
	all := make([]*item, jobCount)
	seen := make([]int32, jobCount)
	var sum int64
	var active, maxActive int32

	// holder 占满全部 semaphore 名额，让所有 worker 阻塞在 heavy 阶段入口。
	go func() {
		for i := 0; i < limit; i++ {
			sem <- struct{}{}
		}
		close(held)
		<-release
		for i := 0; i < limit; i++ {
			<-sem
		}
	}()
	<-held

	go func() {
		for j := 0; j < jobCount; j++ {
			base := int64(j)*100 + seed*59 + int64(r)*1000003
			jobs <- &item{id: j, v: base, next: &item{v: base + 9}}
		}
		close(jobs)
	}()

	var wg sync.WaitGroup
	wg.Add(workers)
	for w := 0; w < workers; w++ {
		go func() {
			defer wg.Done()
			kept := make([]*item, 0, jobCount/workers+2)
			signaled := false
			for it := range jobs {
				if !signaled {
					signaled = true
					waiting <- struct{}{}
				}
				sem <- struct{}{}
				cur := atomic.AddInt32(&active, 1)
				for {
					m := atomic.LoadInt32(&maxActive)
					if cur <= m || atomic.CompareAndSwapInt32(&maxActive, m, cur) {
						break
					}
				}
				if it == nil || it.next == nil || it.next.v != it.v+9 {
					badID := -1
					if it != nil {
						badID = it.id
					}
					out <- poolResult{id: badID, bad: 1}
					atomic.AddInt32(&active, -1)
					<-sem
					continue
				}
				// 固定工作量，扩大并发重叠但不用墙钟判定。
				for k := 0; k < 256; k++ {
					runtime.Gosched()
				}
				atomic.AddInt32(&active, -1)
				<-sem
				atomic.AddInt32(&seen[it.id], 1)
				atomic.AddInt64(&sum, it.v+it.next.v)
				all[it.id] = it
				kept = append(kept, it)
			}
			finalReady <- struct{}{}
			<-finalRelease
			for _, it := range kept {
				if it.next == nil || it.next.v != it.v+9 {
					out <- poolResult{id: it.id, bad: 1}
				}
			}
			out <- poolResult{id: -1}
		}()
	}

	for i := 0; i < workers; i++ {
		<-waiting
	}
	gcStress()
	close(release)
	for i := 0; i < workers; i++ {
		<-finalReady
	}
	gcStress()
	close(finalRelease)
	wg.Wait()
	close(out)

	for res := range out {
		ckf(res.bad == 0, "round %d job %d corrupted", r, res.id)
	}
	gotMax := atomic.LoadInt32(&maxActive)
	ckf(gotMax >= 1 && gotMax <= int32(limit), "round %d max concurrent=%d limit=%d", r, gotMax, limit)
	var wantSum int64
	for id := 0; id < jobCount; id++ {
		if atomic.LoadInt32(&seen[id]) != 1 {
			ckf(false, "round %d job %d seen=%d want=1", r, id, atomic.LoadInt32(&seen[id]))
		}
		if all[id] == nil {
			ckf(false, "round %d job %d missing after final GC", r, id)
			continue
		}
		base := int64(id)*100 + seed*59 + int64(r)*1000003
		wantSum += 2*base + 9
	}
	ckf(atomic.LoadInt64(&sum) == wantSum, "round %d sum=%d want=%d", r, atomic.LoadInt64(&sum), wantSum)
}

func main() {
	seed := envSeed()
	rounds := envInt("TORTURE_ROUNDS", 2, 1, 10)
	jobCount := envInt("TORTURE_JOBS", 96, 8, 2048)
	const workers = 8
	const limit = 3
	const queue = 4
	for r := 0; r < rounds; r++ {
		runPool(r, seed, jobCount, workers, limit, queue)
	}
	finish("l04_bounded_pool")
}
