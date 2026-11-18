// L04: WaitGroup 复用：同一个 sync.WaitGroup 在 Add/Wait 完成后跨 12 个阶段反复复用。
// 每阶段 worker 阻塞在 gate 上时 GC，放行后校验本轮次结果；任何复用计数未归零
// 都会导致 Wait 提前返回、结果缺失或串轮。
package main

import (
	"fmt"
	"os"
	"runtime"
	"strconv"
	"sync"
	"sync/atomic"
)

type wnode struct {
	v    int64
	next *wnode
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

func runWaitGroupReuse(rounds, workers int, seed int64) {
	var wg sync.WaitGroup
	kept := make([]*wnode, 0, rounds*2*workers)
	var bad int32
	phase := 0

	for r := 0; r < rounds; r++ {
		for ph := 0; ph < 2; ph++ {
			gen := int64(phase + 1)
			gate := make(chan struct{})
			ready := make(chan int, workers)
			chains := make([]*wnode, workers)
			sums := make([]int64, workers)
			counts := make([]int32, workers)

			wg.Add(workers)
			for w := 0; w < workers; w++ {
				go func(w int) {
					defer wg.Done()
					b := seed*100003 + gen*1000 + int64(w)*7
					head := &wnode{v: b, next: &wnode{v: b + 1}}
					chains[w] = head
					ready <- w
					<-gate
					if head.next == nil || head.next.v != b+1 {
						atomic.AddInt32(&bad, 1)
					}
					sums[w] = head.v + head.next.v
					counts[w]++
				}(w)
			}
			for i := 0; i < workers; i++ {
				<-ready
			}
			// WaitGroup 计数非零、全部 worker 停在 gate 上时 GC。
			gcStress()
			close(gate)
			wg.Wait()

			for w := 0; w < workers; w++ {
				b := seed*100003 + gen*1000 + int64(w)*7
				ckf(counts[w] == 1, "phase %d worker %d ran %d times want=1", phase, w, counts[w])
				ckf(sums[w] == 2*b+1, "phase %d worker %d sum=%d want=%d", phase, w, sums[w], 2*b+1)
				h := chains[w]
				if h == nil || h.next == nil {
					ckf(false, "phase %d worker %d chain missing", phase, w)
					continue
				}
				ckf(h.v == b && h.next.v == b+1, "phase %d worker %d chain corrupted", phase, w)
				kept = append(kept, h)
			}
			// 低占用阶段 GC，为下一轮复用前清理旧任务对象。
			gcStress()
			phase++
		}
	}
	ckf(atomic.LoadInt32(&bad) == 0, "worker saw corrupted chain across WaitGroup reuse")

	gcStress()
	for idx, h := range kept {
		p := idx / workers
		w := idx % workers
		gen := int64(p + 1)
		b := seed*100003 + gen*1000 + int64(w)*7
		if h == nil || h.next == nil {
			ckf(false, "kept phase %d worker %d missing after final GC", p, w)
			continue
		}
		ckf(h.v == b && h.next.v == b+1, "kept phase %d worker %d corrupted after final GC", p, w)
	}
	ckf(len(kept) == rounds*2*workers, "kept=%d want=%d", len(kept), rounds*2*workers)
}

func main() {
	seed := envSeed()
	const rounds = 6
	const workers = 32
	runWaitGroupReuse(rounds, workers, seed)
	finish("l04_waitgroup_reuse_rounds")
}
