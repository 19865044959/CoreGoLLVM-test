// L03: 嵌套 select 事件循环。worker 外层 select 在 jobCh 与 stop 间选择，
// 每处理一个 job 前用内层 select+default 非阻塞排空 urgentCh；job 内部持指针
// 做短紧循环。每 64 个 job 显式 GC，另有 GC pump 并发；任务顺序由 UAF_SEED 决定。
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

var failed int32

const (
	nsJobs   = 1024
	nsUrgent = 256
	nsGCDiv  = 64
	nsInner  = 16
)

type nsNode struct {
	id int64
	v  int64
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
	if seed < 0 {
		seed = -seed
	}
	rng := rand.New(rand.NewSource(seed))

	jobs := make([]nsNode, nsJobs)
	urgents := make([]nsNode, nsUrgent)
	for i := range jobs {
		jobs[i].id = int64(i)
		jobs[i].v = int64(i)*5 + 2
	}
	for i := range urgents {
		urgents[i].id = int64(i)
		urgents[i].v = int64(i)*7 + 3
	}

	jobCh := make(chan *nsNode, 32)
	urgentCh := make(chan *nsNode, 64)
	stop := make(chan struct{})
	done := make(chan struct{})

	var workerWG sync.WaitGroup
	var workerSum int64
	var workerJobs int64
	var workerUrgent int64
	var workerGC int64
	var jobDups int64
	var urgentDups int64
	workerWG.Add(1)
	go func() {
		defer workerWG.Done()
		defer close(done)
		seenJ := make([]bool, nsJobs)
		seenU := make([]bool, nsUrgent)
		var acc int64
		var processed, gcRuns int64
		var sinkLocal []byte

		processJob := func(nd *nsNode) {
			q := nd
			var local int64
			for k := 0; k < nsInner; k++ {
				local += q.v
			}
			acc += local
			processed++
			if processed%nsGCDiv == 0 {
				sinkLocal = make([]byte, 8192)
				sinkLocal[0] = byte(processed)
				runtime.GC()
				gcRuns++
			}
		}
		drainUrgent := func() {
			for {
				select {
				case u := <-urgentCh:
					if seenU[u.id] {
						urgentDups++
					} else {
						seenU[u.id] = true
					}
					acc += u.v
					workerUrgent++
				default:
					return
				}
			}
		}

		for {
			select {
			case nd, ok := <-jobCh:
				if !ok {
					drainUrgent()
					workerSum = acc
					workerJobs = processed
					workerGC = gcRuns
					for i := range seenJ {
						if !seenJ[i] {
							atomic.AddInt32(&failed, 1)
							fmt.Printf("FAIL: missing job %d (seed=%d)\n", i, seed)
						}
					}
					runtime.KeepAlive(sinkLocal)
					return
				}
				if seenJ[nd.id] {
					jobDups++
				} else {
					seenJ[nd.id] = true
				}
				drainUrgent()
				processJob(nd)
			case <-stop:
				workerSum = acc
				workerJobs = processed
				workerGC = gcRuns
				return
			}
		}
	}()

	var epoch atomic.Int64
	var pumpWG sync.WaitGroup
	start := make(chan struct{})
	pumpWG.Add(1)
	go func() {
		defer pumpWG.Done()
		<-start
		for r := 0; r < 8; r++ {
			junk := make([]byte, 16384)
			for i := 0; i < len(junk); i += 512 {
				junk[i] = byte(r + i)
			}
			runtime.GC()
			epoch.Add(1)
			runtime.KeepAlive(junk)
		}
	}()
	close(start)

	order := rng.Perm(nsJobs)
	u := 0
	for i, oi := range order {
		jobCh <- &jobs[oi]
		if i%4 == 3 && u < nsUrgent {
			urgentCh <- &urgents[u]
			u++
		}
	}
	close(jobCh)
	<-done
	for epoch.Load() < 1 {
		runtime.Gosched()
	}
	workerWG.Wait()
	close(stop)
	pumpWG.Wait()

	var wantSum int64
	for i := range jobs {
		wantSum += int64(nsInner) * jobs[i].v
	}
	for i := range urgents {
		wantSum += urgents[i].v
	}

	ck(epoch.Load() >= 1, fmt.Sprintf("concurrent GC ran during nested select (seed=%d)", seed))
	ck(workerJobs == nsJobs, fmt.Sprintf("worker processed every job (seed=%d)", seed))
	ck(workerUrgent == nsUrgent, fmt.Sprintf("worker drained every urgent item (seed=%d)", seed))
	ck(jobDups == 0 && urgentDups == 0, fmt.Sprintf("no duplicate jobs or urgent items (seed=%d)", seed))
	ck(workerSum == wantSum, fmt.Sprintf("nested select checksum after GC (seed=%d)", seed))
	ck(workerGC == nsJobs/nsGCDiv, fmt.Sprintf("worker explicit GC every %d jobs (seed=%d)", nsGCDiv, seed))
	ck(u == nsUrgent, fmt.Sprintf("producer sent every urgent item (seed=%d)", seed))

	corrupt := false
	for i := range jobs {
		if jobs[i].v != int64(i)*5+2 || jobs[i].id != int64(i) {
			corrupt = true
		}
	}
	for i := range urgents {
		if urgents[i].v != int64(i)*7+3 || urgents[i].id != int64(i) {
			corrupt = true
		}
	}
	ck(!corrupt, fmt.Sprintf("nested select nodes intact after GC (seed=%d)", seed))
	runtime.KeepAlive(jobs)
	runtime.KeepAlive(urgents)
	finish("l03_nested_select")
}
