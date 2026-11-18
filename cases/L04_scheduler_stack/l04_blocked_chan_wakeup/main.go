// L04: 阻塞的接收者被 close(ch) 唤醒、阻塞的 select 发送者被 close(done) 唤醒；
// 唤醒前后持有局部指针，GC 发生在阻塞点，校验指针不丢。
package main

import (
	"fmt"
	"os"
	"runtime"
	"strconv"
	"sync"
	"sync/atomic"
)

type node struct{ v int64 }

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

func spinUntil(p *int32, n int32) {
	for atomic.LoadInt32(p) != n {
		runtime.Gosched()
	}
}

type wakeResult struct {
	id   int
	p    *node
	bad  bool
	sent bool
}

// 接收者阻塞在无缓冲 channel 上，close(ch) 将其全部唤醒。
func receiverPhase(n int, seed int64) {
	ch := make(chan *node)
	gate := make(chan struct{})
	var ready int32
	results := make(chan wakeResult, n)

	for i := 0; i < n; i++ {
		go func(id int) {
			want := int64(id)*13 + seed
			p := &node{v: want}
			atomic.AddInt32(&ready, 1)
			v, ok := <-ch
			bad := ok || v != nil || p.v != want
			// 唤醒后等 GC 再做一次校验。
			<-gate
			if p.v != want {
				bad = true
			}
			results <- wakeResult{id: id, p: p, bad: bad}
		}(i)
	}

	spinUntil(&ready, int32(n))
	gcStress()
	close(ch)
	close(gate)
	for i := 0; i < n; i++ {
		r := <-results
		ckf(!r.bad, "receiver %d: close wake lost pointer or returned value", r.id)
		ckf(r.p != nil && r.p.v == int64(r.id)*13+seed, "receiver %d: local pointer corrupted", r.id)
	}
}

// 发送者阻塞在 select 上：out 已满、done 未关闭；close(done) 走唤醒分支。
func senderPhase(n int, seed int64) {
	out := make(chan *node, 1)
	out <- &node{v: -1}
	done := make(chan struct{})
	gate := make(chan struct{})
	var ready int32
	results := make(chan wakeResult, n)

	for i := 0; i < n; i++ {
		go func(id int) {
			want := int64(id)*29 + seed
			p := &node{v: want}
			atomic.AddInt32(&ready, 1)
			var sent bool
			select {
			case out <- p:
				sent = true
			case <-done:
			}
			<-gate
			bad := p.v != want || sent
			results <- wakeResult{id: id, p: p, bad: bad, sent: sent}
		}(i)
	}

	spinUntil(&ready, int32(n))
	gcStress()
	close(done)
	close(gate)
	for i := 0; i < n; i++ {
		r := <-results
		ckf(!r.bad, "sender %d: done wake failed (sent=%v)", r.id, r.sent)
		ckf(r.p != nil && r.p.v == int64(r.id)*29+seed, "sender %d: local pointer corrupted", r.id)
	}
	sentinel := <-out
	ckf(sentinel != nil && sentinel.v == -1, "blocked senders unexpectedly delivered a pointer")
}

// 已 close 的带缓冲 channel：先排空缓冲内指针，再返回零值。
func bufferedClosePhase(n int, seed int64) {
	ch := make(chan *node, 2)
	a := &node{v: seed + 100}
	b := &node{v: seed + 200}
	ch <- a
	ch <- b
	close(ch)
	// GC 时两个指针仍位于 channel 缓冲区（堆对象），必须被扫描到。
	gcStress()

	var countA, countB, countNil, countOther int32
	var wg sync.WaitGroup
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func() {
			defer wg.Done()
			p := <-ch
			switch {
			case p == nil:
				atomic.AddInt32(&countNil, 1)
			case p.v == seed+100:
				atomic.AddInt32(&countA, 1)
			case p.v == seed+200:
				atomic.AddInt32(&countB, 1)
			default:
				atomic.AddInt32(&countOther, 1)
			}
		}()
	}
	wg.Wait()
	ckf(countA == 1 && countB == 1 && countOther == 0 && countNil == int32(n-2),
		"buffered closed channel drain: a=%d b=%d nil=%d other=%d", countA, countB, countNil, countOther)
	gcStress()
	ckf(a.v == seed+100 && b.v == seed+200, "buffered channel pointers corrupted")
}

func main() {
	seed := envSeed()
	const n = 16
	receiverPhase(n, seed)
	senderPhase(n, seed)
	bufferedClosePhase(n, seed)
	ckf(n > 2, "invalid worker count")
	finish("l04_blocked_chan_wakeup")
}
