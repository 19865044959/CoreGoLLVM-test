// L02: 可复用 cyclic barrier（tailscale start barrier / grpc 并发回调收敛场景）：
// 6 个 worker 跑 4 个阶段，Wait 前累加 done，Wait 后必须看到全部 done；
// 另测 break 语义（全部等待者返回失败）与 break 后复用。
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
	barWorkers = 6
	barPhases  = 4
	barBroken  = 2
)

type barPayload struct {
	id  int
	val int64
	tag string
}

type barrier struct {
	mu        sync.Mutex
	n         int
	count     int
	gen       uint64
	brokenGen uint64
	ch        chan struct{}
}

func newBarrier(n int) *barrier {
	return &barrier{n: n, brokenGen: ^uint64(0), ch: make(chan struct{})}
}

func (b *barrier) Wait() bool { return b.waitNotify(nil) }

// waitNotify 在计数加一后（仍然持锁）向 entered 发送通知，便于测试确定性触发 Break。
// entered 必须是有缓冲 channel，且容量足够。
func (b *barrier) waitNotify(entered chan<- struct{}) bool {
	b.mu.Lock()
	if b.brokenGen == b.gen {
		b.mu.Unlock()
		return false
	}
	b.count++
	if entered != nil {
		entered <- struct{}{}
	}
	if b.count == b.n {
		b.count = 0
		b.gen++
		close(b.ch)
		b.ch = make(chan struct{})
		b.mu.Unlock()
		return true
	}
	gen := b.gen
	ch := b.ch
	b.mu.Unlock()
	<-ch
	b.mu.Lock()
	broken := b.brokenGen == gen
	b.mu.Unlock()
	return !broken
}

func (b *barrier) Break() {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.brokenGen == b.gen {
		return
	}
	b.brokenGen = b.gen
	b.count = 0
	b.gen++
	close(b.ch)
	b.ch = make(chan struct{})
}

var (
	failed int32
	gcMu   sync.Mutex
	gcSink [][]byte
)

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

func seedVal() int64 {
	if s := os.Getenv("UAF_SEED"); s != "" {
		if n, err := strconv.Atoi(s); err == nil {
			return int64(n)
		}
	}
	return 1
}

func (p *barPayload) valid(seed int64) bool {
	return p.val == seed+int64(p.id)*17 && p.tag == fmt.Sprintf("w%d", p.id)
}

func main() {
	seed := seedVal()
	b := newBarrier(barWorkers)
	var done [barPhases]int32
	var passed [barPhases]int32
	var wg sync.WaitGroup

	for w := 0; w < barWorkers; w++ {
		p := &barPayload{id: w, val: seed + int64(w)*17, tag: fmt.Sprintf("w%d", w)}
		wg.Add(1)
		go func(w int, p *barPayload) {
			defer wg.Done()
			for ph := 0; ph < barPhases; ph++ {
				if !p.valid(seed) {
					ck(false, fmt.Sprintf("worker %d payload corrupted before phase %d", w, ph))
				}
				atomic.AddInt32(&done[ph], 1)
				if !b.Wait() {
					ck(false, fmt.Sprintf("barrier broken unexpectedly in phase %d", ph))
					return
				}
				if n := atomic.LoadInt32(&done[ph]); n != barWorkers {
					ck(false, fmt.Sprintf("phase %d advanced early: done=%d want=%d", ph, n, barWorkers))
				}
				atomic.AddInt32(&passed[ph], 1)
				if ph == 1 {
					gcStress()
					if !p.valid(seed) {
						ck(false, fmt.Sprintf("worker %d payload corrupted after GC", w))
					}
				}
			}
		}(w, p)
	}
	wg.Wait()

	for ph := 0; ph < barPhases; ph++ {
		if n := atomic.LoadInt32(&done[ph]); n != barWorkers {
			ck(false, fmt.Sprintf("phase %d done=%d want=%d", ph, n, barWorkers))
		}
		if n := atomic.LoadInt32(&passed[ph]); n != barWorkers {
			ck(false, fmt.Sprintf("phase %d passed=%d want=%d", ph, n, barWorkers))
		}
	}

	// break：两个等待者必须被释放并返回失败。
	bb := newBarrier(4)
	started := make(chan struct{}, barBroken)
	woke := make(chan bool, barBroken)
	for i := 0; i < barBroken; i++ {
		go func() {
			woke <- bb.waitNotify(started)
		}()
	}
	<-started
	<-started
	bb.Break()
	for i := 0; i < barBroken; i++ {
		if <-woke {
			ck(false, "waiters released by break must report failure")
		}
	}

	// break 后 barrier 必须能重新用于下一代。
	reuse := make(chan bool, bb.n)
	for i := 0; i < bb.n; i++ {
		go func() { reuse <- bb.Wait() }()
	}
	for i := 0; i < bb.n; i++ {
		if !<-reuse {
			ck(false, "barrier not reusable after break")
		}
	}

	gcStress()
	fmt.Printf("workers=%d phases=%d broken=%d\n", barWorkers, barPhases, barBroken)
	finish("l02_barrier_phase_gate")
}
