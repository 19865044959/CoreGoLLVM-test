// L04: timer 合并总线：多个 notifier 在窗口内反复更新同一个 pending 值，
// flusher 用真实 timer 合并触发，只投递窗口内最新的指针链。notifier 在中点
// 屏障阻塞时 GC；关闭时强制 flush 尾巴。断言只看合并语义（投递代号严格递增、
// 无重复、最终值完整），不依赖墙钟。
package main

import (
	"fmt"
	"os"
	"runtime"
	"strconv"
	"sync"
	"sync/atomic"
	"time"
)

type bnode struct {
	gen  int64
	v    int64
	next *bnode
}

type coalesceBus struct {
	mu      sync.Mutex
	pending *bnode
	gen     int64
	kick    chan struct{}
	stop    chan struct{}
	done    chan struct{}
	out     chan *bnode
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

func newBus(outCap int) *coalesceBus {
	return &coalesceBus{
		kick: make(chan struct{}, 1),
		stop: make(chan struct{}),
		done: make(chan struct{}),
		out:  make(chan *bnode, outCap),
	}
}

// update 在锁内把 pending 换成新链；同一窗口内的多次 update 只会留下最后一个。
func (b *coalesceBus) update(seed int64) *bnode {
	b.mu.Lock()
	b.gen++
	g := b.gen
	n := &bnode{gen: g, v: seed*1000003 + g*13, next: &bnode{gen: g, v: seed*1000003 + g*13 + 1}}
	b.pending = n
	b.mu.Unlock()
	select {
	case b.kick <- struct{}{}:
	default:
	}
	return n
}

func (b *coalesceBus) flush() {
	b.mu.Lock()
	p := b.pending
	b.pending = nil
	b.mu.Unlock()
	if p != nil {
		b.out <- p
	}
}

func (b *coalesceBus) run() {
	defer close(b.done)
	var timer *time.Timer
	var timerC <-chan time.Time
	for {
		select {
		case <-b.kick:
			if timerC == nil {
				if timer == nil {
					timer = time.NewTimer(time.Millisecond)
				} else {
					timer.Reset(time.Millisecond)
				}
				timerC = timer.C
			}
		case <-timerC:
			b.flush()
			timerC = nil
		case <-b.stop:
			if timer != nil {
				timer.Stop()
			}
			b.flush()
			return
		}
	}
}

func runCoalesceRound(r, seed int64, workers, perWorker int) {
	total := workers * perWorker
	b := newBus(total + 4)
	go b.run()

	halfReady := make(chan struct{}, workers)
	release := make(chan struct{})
	finished := make(chan struct{}, workers)
	var wg sync.WaitGroup
	wg.Add(workers)

	kept := make([][]*bnode, workers)
	for w := 0; w < workers; w++ {
		go func(w int) {
			defer wg.Done()
			mine := make([]*bnode, 0, perWorker)
			for k := 0; k < perWorker; k++ {
				mine = append(mine, b.update(seed))
				if k+1 == perWorker/2 {
					halfReady <- struct{}{}
					<-release
				}
			}
			kept[w] = mine
			finished <- struct{}{}
		}(w)
	}

	for i := 0; i < workers; i++ {
		<-halfReady
	}
	// 一半更新已合并进 pending、notifier 全部停在屏障上时 GC。
	gcStress()
	close(release)
	for i := 0; i < workers; i++ {
		<-finished
	}
	wg.Wait()
	close(b.stop)
	<-b.done
	close(b.out)

	var delivered []*bnode
	var lastGen int64
	for n := range b.out {
		if n == nil {
			ckf(false, "round %d nil delivered", r)
			continue
		}
		ckf(n.gen > lastGen, "round %d delivered gen %d after %d", r, n.gen, lastGen)
		ckf(n.gen >= 1 && n.gen <= int64(total), "round %d delivered gen %d out of range", r, n.gen)
		ckf(n.next != nil && n.next.v == n.v+1 && n.next.gen == n.gen,
			"round %d delivered gen %d chain corrupted", r, n.gen)
		lastGen = n.gen
		delivered = append(delivered, n)
	}
	ckf(len(delivered) >= 1, "round %d no delivery", r)
	ckf(len(delivered) <= total, "round %d deliveries=%d want<=%d", r, len(delivered), total)
	ckf(lastGen == int64(total), "round %d last delivered gen=%d want=%d", r, lastGen, int64(total))

	// 被投递过的链只由 recorded slice 引用；GC 后必须完整。
	gcStress()
	for _, n := range delivered {
		ckf(n.next != nil && n.next.v == n.v+1,
			"round %d delivered gen %d corrupted after GC", r, n.gen)
	}
	// 每个 notifier 的局部记录在 GC 后也保持自洽（未投递的更新已被合并覆盖）。
	for w := 0; w < workers; w++ {
		ckf(len(kept[w]) == perWorker, "round %d worker %d kept=%d want=%d", r, w, len(kept[w]), perWorker)
		for k, n := range kept[w] {
			if n == nil || n.next == nil || n.next.v != n.v+1 {
				ckf(false, "round %d worker %d update %d corrupted after GC", r, w, k)
				continue
			}
		}
	}
}

func main() {
	seed := envSeed()
	rounds := envInt("TORTURE_ROUNDS", 2, 1, 10)
	workers := envInt("TORTURE_WORKERS", 6, 2, 16)
	perWorker := envInt("TORTURE_ITEMS", 24, 4, 128)
	for r := 0; r < rounds; r++ {
		runCoalesceRound(int64(r), seed+int64(r), workers, perWorker)
	}
	finish("l04_timer_coalesce_bus")
}
