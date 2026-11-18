// L02: atomic 计数器 + 并发分配/GC：共享计数、CAS 峰值、每 worker 本地计数，
// 并发分配指向数组内部的指针并在 GC 后校验；最终不变量确定。
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
	nWorkers   = 8
	baseIters  = 400
	chunkEvery = 32
	chunkSize  = 16
)

var (
	failed     int32
	gcMu       sync.Mutex
	gcSink     [][]byte
	totalCount atomic.Int64
	maxCount   atomic.Int64
	liveChunks atomic.Int64
	workCounts [nWorkers]int64
)

type node struct {
	v    int64
	name string
}

func ck(ok bool, msg string) {
	if !ok {
		atomic.AddInt32(&failed, 1)
		fmt.Println("FAIL:", msg)
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

func tortureRounds(def int) int {
	s := os.Getenv("TORTURE_ROUNDS")
	if s == "" {
		return def
	}
	n, err := strconv.Atoi(s)
	if err != nil || n <= 0 {
		return def
	}
	if n > 100 {
		n = 100
	}
	return n
}

func worker(id, iters int, start <-chan struct{}, wg *sync.WaitGroup) {
	defer wg.Done()
	<-start
	local := 0
	for i := 0; i < iters; i++ {
		v := totalCount.Add(1)
		local++
		for {
			cur := maxCount.Load()
			if v <= cur {
				break
			}
			if maxCount.CompareAndSwap(cur, v) {
				break
			}
		}
		if i%chunkEvery == 0 {
			liveChunks.Add(1)
			arr := make([]node, chunkSize)
			ptrs := make([]*node, chunkSize)
			for j := range arr {
				arr[j] = node{v: int64(i*chunkSize + j), name: "n"}
				ptrs[j] = &arr[j]
			}
			gcStress()
			for j := range ptrs {
				if ptrs[j] == nil || ptrs[j].v != int64(i*chunkSize+j) || ptrs[j].name != "n" {
					ck(false, "interior pointer corrupted after GC")
					break
				}
			}
			liveChunks.Add(-1)
			ptrs = nil
			arr = nil
			runtime.KeepAlive(ptrs)
		}
	}
	workCounts[id] = int64(local)
}

func main() {
	iters := baseIters * tortureRounds(1)
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < nWorkers; i++ {
		wg.Add(1)
		go worker(i, iters, start, &wg)
	}
	close(start)
	wg.Wait()

	want := int64(nWorkers * iters)
	got := totalCount.Load()
	ck(got == want, fmt.Sprintf("total counter got=%d want=%d", got, want))
	ck(maxCount.Load() == want, fmt.Sprintf("max counter got=%d want=%d", maxCount.Load(), want))
	ck(liveChunks.Load() == 0, "live chunk counter did not return to zero")

	var sum int64
	for i := range workCounts {
		if workCounts[i] != int64(iters) {
			ck(false, fmt.Sprintf("worker %d count got=%d want=%d", i, workCounts[i], iters))
		}
		sum += workCounts[i]
	}
	ck(sum == want, "sum of per-worker counters mismatch")

	gcStress()
	ck(totalCount.Load() == want && maxCount.Load() == want, "counters changed after final GC")

	fmt.Printf("workers=%d increments=%d max=%d\n", nWorkers, want, maxCount.Load())
	finish("l02_concurrent_counter_churn")
}
