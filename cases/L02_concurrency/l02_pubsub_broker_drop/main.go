// L02: pub/sub broker（grpc-go grpcsync/pubsub 场景）：订阅者独立缓冲队列、
// 发布前只回放最后一条、慢订阅者非阻塞丢弃计数、退订关闭 channel 且与发布互斥。
package main

import (
	"fmt"
	"os"
	"runtime"
	"strconv"
	"sync"
	"sync/atomic"
)

type message struct {
	seq int
	v   int64
	tag string
}

type subscriber struct {
	id      int
	ch      chan *message
	active  bool
	dropped int
}

type broker struct {
	mu   sync.Mutex
	subs []*subscriber
	last *message
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

func tortureRounds(def int) int {
	s := os.Getenv("TORTURE_ROUNDS")
	if s == "" {
		return def
	}
	n, err := strconv.Atoi(s)
	if err != nil || n <= 0 {
		return def
	}
	if n > 20 {
		n = 20
	}
	return n
}

func newBroker() *broker { return &broker{} }

func (b *broker) Subscribe(capacity int) *subscriber {
	b.mu.Lock()
	defer b.mu.Unlock()
	s := &subscriber{id: len(b.subs), ch: make(chan *message, capacity), active: true}
	if b.last != nil {
		s.ch <- b.last
	}
	b.subs = append(b.subs, s)
	return s
}

func (b *broker) Publish(m *message) int {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.last = m
	dropped := 0
	for _, s := range b.subs {
		if !s.active {
			continue
		}
		select {
		case s.ch <- m:
		default:
			s.dropped++
			dropped++
		}
	}
	return dropped
}

func (b *broker) Unsubscribe(s *subscriber) {
	b.mu.Lock()
	if s.active {
		s.active = false
		close(s.ch)
	}
	b.mu.Unlock()
}

func (b *broker) droppedOf(s *subscriber) int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return s.dropped
}

func expect(seed int64, seq int) int64 { return seed*31 + int64(seq)*7 }

func waitFor(cond func() bool, msg string) {
	for i := 0; i < 4000000; i++ {
		if cond() {
			return
		}
		runtime.Gosched()
	}
	ck(false, msg)
}

func main() {
	seed := seedVal()
	rounds := tortureRounds(1)
	total := 20 * rounds
	b := newBroker()

	pre := &message{seq: 0, v: expect(seed, 0), tag: "pre"}
	b.Publish(pre)

	fast := make([]*subscriber, 3)
	for i := range fast {
		fast[i] = b.Subscribe(total + 2)
	}
	slow := b.Subscribe(3)

	// 订阅前发布的消息只回放最后一条。
	for _, s := range append(append([]*subscriber{}, fast...), slow) {
		select {
		case m := <-s.ch:
			if m.seq != 0 || m.v != expect(seed, 0) || m.tag != "pre" {
				ck(false, "replay message mismatch")
			}
		default:
			ck(false, "missing replay of last message")
		}
	}
	gcStress()

	var wg sync.WaitGroup
	recv := make([]int32, len(fast))
	for i := range fast {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			want := 1
			for want <= total {
				m, ok := <-fast[idx].ch
				if !ok {
					ck(false, "fast subscriber channel closed early")
					return
				}
				if m.seq != want || m.v != expect(seed, m.seq) || m.tag != "fast" {
					ck(false, "fast subscriber received wrong message")
					return
				}
				atomic.AddInt32(&recv[idx], 1)
				want++
				if want%64 == 0 {
					gcStress()
					if m.seq < 0 || m.v != expect(seed, m.seq) {
						ck(false, "message corrupted across GC")
					}
				}
			}
		}(i)
	}

	slowDrops := 0
	for seq := 1; seq <= total; seq++ {
		m := &message{seq: seq, v: expect(seed, seq), tag: "fast"}
		slowDrops += b.Publish(m)
	}
	// 每个发布最多让 slow 丢一次；slow 缓冲为 3。
	ck(slowDrops == total-3, fmt.Sprintf("slow drops=%d want=%d", slowDrops, total-3))
	ck(b.droppedOf(slow) == total-3, "slow subscriber drop counter mismatch")
	ck(len(slow.ch) == 3, fmt.Sprintf("slow buffer len=%d want=3", len(slow.ch)))
	for seq := 1; seq <= 3; seq++ {
		m := <-slow.ch
		if m.seq != seq || m.v != expect(seed, seq) {
			ck(false, "slow subscriber kept wrong prefix")
		}
	}
	wg.Wait()
	for i := range recv {
		ck(int(recv[i]) == total, fmt.Sprintf("fast subscriber %d got=%d want=%d", i, recv[i], total))
	}

	gcStress()
	last := &message{seq: total + 1, v: expect(seed, total+1), tag: "last"}
	b.Publish(last)
	for _, s := range fast {
		select {
		case m := <-s.ch:
			if m.seq != total+1 || m.v != expect(seed, total+1) {
				ck(false, "fast subscriber missed post-drain message")
			}
		default:
			ck(false, "fast subscriber did not receive post-drain message")
		}
	}

	for _, s := range append(append([]*subscriber{}, fast...), slow) {
		b.Unsubscribe(s)
		closed := false
		for i := 0; i < 8; i++ {
			if _, ok := <-s.ch; !ok {
				closed = true
				break
			}
		}
		ck(closed, "channel not closed after unsubscribe")
	}
	ck(b.Publish(&message{seq: 999, v: 0, tag: "dead"}) == 0, "publish after unsubscribe must not drop")

	// 并发发布与退订：关闭与发送互斥，不得 panic。
	pub := b.Subscribe(1)
	pub2 := b.Subscribe(1)
	drained := make(chan struct{})
	go func() {
		for i := 0; i < 60; i++ {
			b.Publish(&message{seq: i, v: int64(i), tag: "race"})
			runtime.Gosched()
			if i == 10 {
				close(drained)
			}
		}
	}()
	<-drained
	b.Unsubscribe(pub)
	b.Unsubscribe(pub2)
	for _, s := range []*subscriber{pub, pub2} {
		for range s.ch {
		}
	}
	gcStress()
	if last.v != expect(seed, last.seq) {
		ck(false, "final message corrupted by GC")
	}

	fmt.Printf("subs=4 published=%d slow_got=3 slow_drops=%d fast_got=%d\n",
		total, total-3, 3*total)
	finish("l02_pubsub_broker_drop")
}
