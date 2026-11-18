// L02: 有界 worker pool + quit 通道：固定 4 个 worker 从无缓冲 jobs 取任务，
// 用 barrier 任务确定性地把并发打到上限，处理完全部任务后 close(quit) 统一退出。
package main

import (
	"fmt"
	"os"
	"runtime"
	"strconv"
	"sync"
	"sync/atomic"
)

const (
	nWorkers     = 4
	jobsPerCycle = 48
	nCycles      = 3
)

var (
	failed  int32
	gcMu    sync.Mutex
	gcSink  [][]byte
	curSeed int64
)

type job struct {
	id      int
	tag     int64
	name    string
	release chan struct{}
}

type pool struct {
	jobs      chan *job
	quit      chan struct{}
	held      chan struct{}
	wg        sync.WaitGroup
	inflight  atomic.Int32
	peak      atomic.Int32
	processed atomic.Int64
	exited    atomic.Int32
}

func ck(ok bool, msg string) {
	if !ok {
		atomic.AddInt32(&failed, 1)
		fmt.Println("FAIL:", msg)
	}
}

func ckSeed(ok bool, msg string) {
	if !ok {
		atomic.AddInt32(&failed, 1)
		fmt.Printf("FAIL: %s (seed=%d)\n", msg, curSeed)
	}
}

func gcStress() {
	gcMu.Lock()
	for i := 0; i < 8; i++ {
		b := make([]byte, 2048)
		b[0] = byte(i)
		gcSink = append(gcSink[:0], b)
	}
	gcSink = gcSink[:0]
	gcMu.Unlock()
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

func tagFor(seed int64, id int) int64 {
	x := uint64(seed)*0x9E3779B97F4A7C15 + uint64(id+1)*0xBF58476D1CE4E5B9
	x ^= x >> 30
	x *= 0x94D049BB133111EB
	x ^= x >> 31
	return int64(x >> 1)
}

func makeJob(id int, release chan struct{}) *job {
	return &job{id: id, tag: tagFor(curSeed, id), name: "job", release: release}
}

func validJob(j *job) bool {
	return j != nil && j.id >= 0 && j.id < jobsPerCycle && j.name == "job" && j.tag == tagFor(curSeed, j.id)
}

func (p *pool) worker(start <-chan struct{}, ready chan<- struct{}) {
	defer p.wg.Done()
	defer p.exited.Add(1)
	ready <- struct{}{}
	<-start
	for {
		select {
		case j := <-p.jobs:
			p.inflight.Add(1)
			cur := p.inflight.Load()
			for {
				pk := p.peak.Load()
				if cur <= pk || p.peak.CompareAndSwap(pk, cur) {
					break
				}
			}
			if j.release != nil {
				p.held <- struct{}{}
				<-j.release
			}
			if !validJob(j) {
				ckSeed(false, "worker observed corrupted job")
			}
			if j.id%7 == 0 {
				gcStress()
				if !validJob(j) {
					ckSeed(false, "job corrupted after GC")
				}
			}
			p.inflight.Add(-1)
			p.processed.Add(1)
		case <-p.quit:
			return
		}
	}
}

func main() {
	curSeed = envSeed()
	totalPeak := int32(0)
	for cycle := 0; cycle < nCycles; cycle++ {
		p := &pool{
			jobs: make(chan *job),
			quit: make(chan struct{}),
			held: make(chan struct{}, nWorkers),
		}
		start := make(chan struct{})
		ready := make(chan struct{}, nWorkers)
		release := make(chan struct{})

		for i := 0; i < nWorkers; i++ {
			p.wg.Add(1)
			go p.worker(start, ready)
		}
		for i := 0; i < nWorkers; i++ {
			<-ready
		}
		close(start)

		// Barrier 任务：每个 worker 各持一个任务并阻塞在 release 上，
		// 主 goroutine 收齐 nWorkers 个 held 信号后并发上限必定为 nWorkers。
		for i := 0; i < nWorkers; i++ {
			p.jobs <- makeJob(i, release)
		}
		for i := 0; i < nWorkers; i++ {
			<-p.held
		}
		gcStress()
		if got := p.inflight.Load(); got != nWorkers {
			ck(false, fmt.Sprintf("barrier inflight got=%d want=%d", got, nWorkers))
		}
		if got := p.peak.Load(); got != nWorkers {
			ck(false, fmt.Sprintf("barrier peak got=%d want=%d", got, nWorkers))
		}
		close(release)

		for i := nWorkers; i < jobsPerCycle; i++ {
			p.jobs <- makeJob(i, nil)
		}
		for p.processed.Load() < jobsPerCycle {
			runtime.Gosched()
		}
		if got := p.inflight.Load(); got != 0 {
			ck(false, fmt.Sprintf("inflight before quit got=%d want=0", got))
		}
		close(p.quit)
		p.wg.Wait()

		ck(p.processed.Load() == jobsPerCycle,
			fmt.Sprintf("processed got=%d want=%d", p.processed.Load(), jobsPerCycle))
		ck(p.exited.Load() == nWorkers,
			fmt.Sprintf("exited workers got=%d want=%d", p.exited.Load(), nWorkers))
		ck(p.peak.Load() == nWorkers,
			fmt.Sprintf("peak got=%d want=%d", p.peak.Load(), nWorkers))
		ck(p.inflight.Load() == 0, "inflight not zero after quit")
		if p.peak.Load() > totalPeak {
			totalPeak = p.peak.Load()
		}
		gcStress()
		if len(p.jobs) != 0 {
			ck(false, "quit left jobs in queue")
		}
	}

	fmt.Printf("cycles=%d workers=%d jobs_per_cycle=%d peak=%d\n",
		nCycles, nWorkers, jobsPerCycle, totalPeak)
	finish("l02_worker_pool_quit")
}
