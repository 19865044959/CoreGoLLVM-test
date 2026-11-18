// L03: select 驱动的 worker 循环，每个 job 内部是无调用紧循环；worker 自身
// 周期性 runtime.GC()，主 goroutine 另起 GC pump，job 顺序由 UAF_SEED 决定。
package main

import (
	"fmt"
	"math/rand"
	"os"
	"runtime"
	"strconv"
	"sync"
	"sync/atomic"
)

var (
	failed    int32
	gcKeep    [][]byte
	tickSink  []byte
	workerSum int64
	workerGCs int64
)

const (
	jobs  = 1024
	inner = 512
)

type node struct {
	v int64
}

func ck(ok bool, msg string) {
	if !ok {
		atomic.AddInt32(&failed, 1)
		fmt.Println("FAIL:", msg)
	}
}

func finish(name string) {
	if n := atomic.LoadInt32(&failed); n != 0 {
		fmt.Printf("FAILED %s (%d failed checks)\n", name, n)
		os.Exit(1)
	}
	fmt.Println("ALL PASS " + name)
}

func seedFromEnv() int64 {
	if s := os.Getenv("UAF_SEED"); s != "" {
		if v, err := strconv.ParseInt(s, 10, 64); err == nil {
			return v
		}
	}
	return 1
}

func main() {
	seed := seedFromEnv()

	backing := make([]node, jobs)
	nodes := make([]*node, jobs)
	for i := range backing {
		backing[i] = node{v: int64(i) + 1}
		nodes[i] = &backing[i]
	}

	jobCh := make(chan *node, 32)
	stop := make(chan struct{})
	done := make(chan struct{})

	var pumpEpoch atomic.Int64
	var pumpWG sync.WaitGroup
	start := make(chan struct{})
	pumpWG.Add(1)
	go func() {
		defer pumpWG.Done()
		<-start
		for r := 0; r < 6; r++ {
			junk := make([][]byte, 8)
			for i := range junk {
				junk[i] = make([]byte, 8192)
				junk[i][0] = byte(r + i)
			}
			gcKeep = junk
			runtime.GC()
			pumpEpoch.Add(1)
		}
		gcKeep = nil
	}()

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		defer close(done)
		var acc int64
		processed := 0
		for {
			select {
			case nd, ok := <-jobCh:
				if !ok {
					workerSum = acc
					return
				}
				q := nd
				for k := 0; k < inner; k++ {
					acc += q.v
				}
				processed++
				if processed%16 == 0 {
					b := make([]byte, 2048)
					b[0] = byte(processed)
					tickSink = b
					runtime.GC()
					workerGCs++
				}
			case <-stop:
				workerSum = acc
				return
			}
		}
	}()

	close(start)
	rng := rand.New(rand.NewSource(seed))
	order := rng.Perm(jobs)
	for _, i := range order {
		jobCh <- nodes[i]
	}
	close(jobCh)
	<-done
	close(stop)
	wg.Wait()
	pumpWG.Wait()

	var base int64
	for i := range backing {
		base += backing[i].v
	}
	ck(workerSum == inner*base, fmt.Sprintf("worker checksum across select loop (seed=%d)", seed))
	ck(workerGCs == jobs/16, "worker ran periodic GC inside loop")
	ck(pumpEpoch.Load() >= 1, "concurrent GC pump ran")
	corrupt := false
	for i := range backing {
		if nodes[i].v != int64(i)+1 {
			corrupt = true
		}
	}
	ck(!corrupt, fmt.Sprintf("job nodes intact after GC (seed=%d)", seed))
	runtime.KeepAlive(backing)
	runtime.KeepAlive(tickSink)
	finish("l03_select_worker_preempt")
}
