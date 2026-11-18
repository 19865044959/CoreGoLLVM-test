// L19: ticker 与数据通道在同一个 select 中合流。生产者等到第一个 tick 后才开始
// 投递固定数量的事件，主协程 select 交替处理 tick 与事件；每个事件必须恰好处理
// 一次，tick 驱动周期性 GC，最后 Stop ticker 并验证通道未被关闭。TORTURE_ROUNDS
// 控制轮数，默认 1 轮。
package main

import (
	"fmt"
	"os"
	"runtime"
	"strconv"
	"sync/atomic"
	"time"
)

type node struct {
	v    int64
	next *node
}

type message struct {
	id   int
	base int64
	head *node
}

var failed int32

const (
	eventsPerRound = 128
	waitLimit      = 10 * time.Second
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
	bufs := make([][]byte, 0, 16)
	for i := 0; i < 16; i++ {
		b := make([]byte, 4096)
		b[len(b)-1] = byte(i)
		bufs = append(bufs, b)
	}
	runtime.GC()
	runtime.GC()
	runtime.KeepAlive(bufs)
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

func baseOf(id int, seed int64) int64 {
	return int64(id)*113 + seed*43
}

func makeChain(base int64) *node {
	return &node{v: base, next: &node{v: base + 1, next: &node{v: base + 2}}}
}

func chainOK(head *node, base int64) bool {
	if head == nil || head.v != base || head.next == nil || head.next.v != base+1 {
		return false
	}
	return head.next.next != nil && head.next.next.v == base+2 && head.next.next.next == nil
}

func messageOK(m *message, seed int64) bool {
	return m != nil && m.base == baseOf(m.id, seed) && chainOK(m.head, m.base)
}

func round(seed int64) {
	tk := time.NewTicker(time.Millisecond)
	events := make(chan *message, 16)
	firstTick := make(chan struct{})
	producerErr := make(chan error, 1)

	go func() {
		// 与 ticker 握手：第一个 tick 之前不投递任何事件。
		<-firstTick
		for i := 0; i < eventsPerRound; i++ {
			base := baseOf(i, seed)
			events <- &message{id: i, base: base, head: makeChain(base)}
		}
		close(events)
		producerErr <- nil
	}()

	seen := make([]bool, eventsPerRound)
	got, ticks, bad := 0, 0, 0
	var sum int64
	for got < eventsPerRound {
		select {
		case <-tk.C:
			ticks++
			if ticks == 1 {
				close(firstTick)
			}
			if ticks%8 == 0 {
				// tick 路径上的 GC：事件对象与已处理消息同时在堆上。
				gcStress()
			}
		case m, ok := <-events:
			if !ok {
				ckf(false, "events closed after %d/%d", got, eventsPerRound)
				got = eventsPerRound
				continue
			}
			if m == nil || m.id < 0 || m.id >= eventsPerRound || seen[m.id] {
				bad++
				continue
			}
			seen[m.id] = true
			got++
			if !messageOK(m, seed) {
				bad++
			}
			sum += m.base*3 + 3
			if got%32 == 0 {
				gcStress()
				if !messageOK(m, seed) {
					bad++
				}
			}
		}
	}

	// 等生产者退出并确认通道已关闭；此后不得再有事件。
	select {
	case err := <-producerErr:
		ckf(err == nil, "producer: %v", err)
	case <-time.After(waitLimit):
		ck(false, "producer did not exit")
	}
	select {
	case _, ok := <-events:
		if ok {
			ck(false, "extra event after expected count")
		}
	default:
		// 通道可能尚未被 close 观察到，但事件数已满，不算错误。
	}

	tk.Stop()
	// Stop 不关闭 ticker channel：排空后必须仍可做非阻塞读取。
	for {
		select {
		case <-tk.C:
		default:
			goto drained
		}
	}
drained:
	select {
	case <-tk.C:
		ck(false, "ticker delivered a tick after Stop")
	default:
	}

	var wantSum int64
	for i := 0; i < eventsPerRound; i++ {
		wantSum += baseOf(i, seed)*3 + 3
	}
	ckf(got == eventsPerRound, "processed=%d want=%d", got, eventsPerRound)
	ckf(ticks >= 1, "ticker produced %d ticks", ticks)
	ckf(bad == 0, "%d events duplicated/corrupted", bad)
	ckf(sum == wantSum, "sum=%d want=%d", sum, wantSum)
	gcStress()
}

func main() {
	seed := envSeed()
	rounds := envInt("TORTURE_ROUNDS", 1, 1, 20)
	fmt.Printf("events=%d rounds=%d\n", eventsPerRound, rounds)
	for r := 0; r < rounds; r++ {
		round(seed + int64(r)*167)
	}
	finish("l19_ticker_select_merge")
}
