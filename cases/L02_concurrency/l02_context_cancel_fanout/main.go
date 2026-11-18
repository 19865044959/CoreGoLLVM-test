// L02: context.WithCancel 广播到 N 个 worker；取消期间持续 GC 与分配，
// 所有 worker 必须观察到 Done、校验各自 payload 后退出。
package main

import (
	"context"
	"fmt"
	"os"
	"runtime"
	"sync"
	"sync/atomic"
)

const (
	nWorkers   = 8
	workTarget = 200
	maxIters   = 1 << 21
)

var (
	failed int32
	gcMu   sync.Mutex
	gcSink [][]byte
)

type blob struct {
	tag  int64
	name string
}

type workerPayload struct {
	id   int
	blob *blob
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

func expectedTag(id int) int64 { return int64(id*11 + 3) }

func main() {
	ctx, cancel := context.WithCancel(context.Background())

	payloads := make([]*workerPayload, nWorkers)
	for i := range payloads {
		payloads[i] = &workerPayload{id: i, blob: &blob{tag: expectedTag(i), name: "ctx"}}
	}

	var wg sync.WaitGroup
	started := make(chan struct{}, nWorkers)
	var exited [nWorkers]int32
	var work int64

	for i := 0; i < nWorkers; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			p := payloads[id]
			started <- struct{}{}
			var churn [][]byte
			iters := 0
			for {
				select {
				case <-ctx.Done():
					ck(p.id == id && p.blob != nil && p.blob.tag == expectedTag(id) && p.blob.name == "ctx",
						"worker payload invalid on cancel")
					gcStress()
					ck(p.blob != nil && p.blob.tag == expectedTag(id) && p.blob.name == "ctx",
						"worker payload corrupted across GC during cancel")
					atomic.StoreInt32(&exited[id], 1)
					return
				default:
				}
				atomic.AddInt64(&work, 1)
				iters++
				if iters >= maxIters {
					ck(false, "worker hit iteration cap without observing cancel")
					atomic.StoreInt32(&exited[id], 1)
					return
				}
				if iters%2048 == 0 {
					churn = append(churn[:0], make([]byte, 256))
					runtime.KeepAlive(churn)
				}
			}
		}(i)
	}

	for i := 0; i < nWorkers; i++ {
		<-started
	}
	for atomic.LoadInt64(&work) < int64(nWorkers*workTarget) {
		runtime.Gosched()
	}
	cancel()
	for i := 0; i < 6; i++ {
		gcStress()
	}

	wg.Wait()
	ck(ctx.Err() == context.Canceled, "context did not report Canceled")

	sum := 0
	for i := range exited {
		if atomic.LoadInt32(&exited[i]) == 1 {
			sum++
		}
	}
	ck(sum == nWorkers, fmt.Sprintf("workers exited=%d want=%d", sum, nWorkers))

	fmt.Printf("workers=%d exited=%d\n", nWorkers, sum)
	finish("l02_context_cancel_fanout")
}
