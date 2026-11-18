// L04: worker 池通过 select 接收任务/关闭信号；全部任务完成后、关闭前 GC，
// 校验阻塞在 select 的 worker 栈上局部指针，并验证优雅退出计数。
package main

import (
	"fmt"
	"os"
	"runtime"
	"strconv"
	"sync/atomic"
)

type job struct {
	id   int
	v    int64
	next *job
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

func jobValue(seed int64, id int) int64 {
	return seed*31 + int64(id)*7
}

func expectedJobSum(seed int64, jobCount int) int64 {
	var s int64
	for i := 0; i < jobCount; i++ {
		v := jobValue(seed, i)
		s += v + (v + 3) // job + job.next
	}
	return s
}

type exitCheck struct {
	lastID int
	ok     bool
}

func runRound(seed int64, workers, jobCount int) {
	jobs := make(chan *job, jobCount)
	done := make(chan struct{})
	results := make(chan int64, jobCount)
	exits := make(chan exitCheck, workers)

	all := make([]*job, jobCount)
	for i := 0; i < jobCount; i++ {
		v := jobValue(seed, i)
		p := &job{id: i, v: v}
		p.next = &job{id: -1, v: v + 3}
		all[i] = p
		jobs <- p
	}

	for w := 0; w < workers; w++ {
		go func() {
			var scratch *job
			lastID := -1
			for {
				select {
				case j := <-jobs:
					scratch = j
					lastID = j.id
					var s int64
					for p := j; p != nil; p = p.next {
						s += p.v
					}
					results <- s
				case <-done:
					ok := true
					if lastID >= 0 {
						ok = scratch != nil &&
							scratch.id == lastID &&
							scratch.v == jobValue(seed, lastID) &&
							scratch.next != nil &&
							scratch.next.v == scratch.v+3
					} else {
						ok = scratch == nil
					}
					runtime.KeepAlive(scratch)
					exits <- exitCheck{lastID: lastID, ok: ok}
					return
				}
			}
		}()
	}

	var got int64
	for i := 0; i < jobCount; i++ {
		got += <-results
	}
	// 所有任务已被取走并完成，worker 停在 select 上；此时 GC 扫描其栈上的 scratch。
	gcStress()
	close(done)

	gotExits := 0
	for i := 0; i < workers; i++ {
		ec := <-exits
		gotExits++
		ckf(ec.ok, "worker exit check failed (lastID=%d)", ec.lastID)
		if ec.lastID >= 0 {
			if ec.lastID >= jobCount {
				ckf(false, "worker lastID %d out of range", ec.lastID)
			} else if p := all[ec.lastID]; p.v != jobValue(seed, ec.lastID) || p.next == nil || p.next.v != p.v+3 {
				ckf(false, "job %d contents corrupted after shutdown GC", ec.lastID)
			}
		}
	}
	ckf(gotExits == workers, "exit count=%d want=%d", gotExits, workers)

	want := expectedJobSum(seed, jobCount)
	ckf(got == want, "job results sum=%d want=%d", got, want)

	// 关闭后再 GC，确认任务对象均未被提前回收。
	gcStress()
	for i := 0; i < jobCount; i++ {
		p := all[i]
		v := jobValue(seed, i)
		if p == nil || p.v != v || p.next == nil || p.next.v != v+3 {
			ckf(false, "job %d corrupted after final GC", i)
		}
	}
}

func main() {
	seed := envSeed()
	rounds := envInt("TORTURE_ROUNDS", 3, 1, 50)
	workers := envInt("TORTURE_WORKERS", 8, 1, 64)
	jobCount := envInt("TORTURE_JOBS", 192, 32, 4096)
	for r := 0; r < rounds; r++ {
		runRound(seed+int64(r)*101, workers, jobCount)
	}
	ckf(rounds > 0 && workers > 0 && jobCount > 1, "empty configuration")
	finish("l04_select_shutdown")
}
