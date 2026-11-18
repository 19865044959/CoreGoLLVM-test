// L04: time.Timer 与 channel 组合：Reset/Stop/排空语义 + 单 goroutine select
// 服务循环同时读 timer.C 与 jobs。每轮 Reset 前显式 GC；定时器停止后发送
// 哨兵 job 作为屏障，校验没有残留 tick、没有旧代事件。
package main

import (
	"fmt"
	"os"
	"runtime"
	"strconv"
	"sync/atomic"
	"time"
)

type tmNode struct {
	v    int64
	next *tmNode
}

type tmJob struct {
	node *tmNode
	ack  chan struct{} // barrier when non-nil
}

type tmEvent struct {
	kind int // 0 timer tick, 1 job
	sum  int64
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

func finish(name string, seed int64) {
	if n := atomic.LoadInt32(&failed); n != 0 {
		fmt.Printf("FAILED %s (UAF_SEED=%d, %d failed checks)\n", name, seed, n)
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

func timerLoop(t *time.Timer, out chan<- tmEvent, jobs <-chan tmJob, quit <-chan struct{}, done chan<- struct{}, head *tmNode) {
	defer close(done)
	for {
		select {
		case <-t.C:
			ok := head != nil && head.next != nil && head.v+1 == head.next.v
			if !ok {
				out <- tmEvent{kind: -1}
				continue
			}
			out <- tmEvent{kind: 0, sum: head.v + head.next.v}
		case j, ok := <-jobs:
			if !ok {
				return
			}
			if j.ack != nil {
				close(j.ack)
				continue
			}
			if j.node == nil || j.node.v != j.node.next.v-1 {
				out <- tmEvent{kind: -1}
				continue
			}
			out <- tmEvent{kind: 1, sum: j.node.v}
		case <-quit:
			return
		}
	}
}

func drainTimer(t *time.Timer) {
	for {
		select {
		case <-t.C:
		default:
			return
		}
	}
}

func main() {
	seed := envSeed()
	rounds := 16 + int(seed%5)

	b := seed*100003 + 17
	head := &tmNode{v: b, next: &tmNode{v: b + 1}}
	jobs := make(chan tmJob, 32)
	out := make(chan tmEvent, 32)
	quit := make(chan struct{})
	loopDone := make(chan struct{})
	t := time.NewTimer(time.Hour)
	go timerLoop(t, out, jobs, quit, loopDone, head)

	if !t.Stop() {
		drainTimer(t)
		ck(false, "fresh 1h timer was not stoppable")
	}
	drainTimer(t)
	if len(t.C) != 0 {
		ck(false, "timer channel not empty after Stop+drain")
	}

	// 每轮：GC -> Reset(1ms) -> 等一个 tick；tick 必须携带当前链的值。
	for r := 0; r < rounds; r++ {
		gcStress()
		ok := head != nil && head.next != nil && head.v+1 == head.next.v
		ckf(ok, "round %d chain corrupted before Reset", r)
		drainTimer(t)
		t.Reset(time.Millisecond)
		ev := <-out
		ckf(ev.kind == 0, "round %d got event kind=%d want timer tick", r, ev.kind)
		ckf(ev.sum == b+b+1, "round %d tick sum=%d want=%d", r, ev.sum, b+b+1)
		if len(t.C) != 0 {
			ckf(false, "round %d duplicate tick left in timer channel", r)
		}
	}

	// 停止并切换到 1h：已到期 timer 的 Stop 可能为 false，排空后必须干净；
	// 新 Reset 的 1h timer 必须能被 Stop 掉，且通道为空。
	drainTimer(t)
	if t.Stop() {
		ck(false, "expired timer Stop returned true")
	}
	drainTimer(t)
	t.Reset(time.Hour)
	if !t.Stop() {
		ckf(false, "Stop of reset 1h timer returned false")
		drainTimer(t)
	}
	if len(t.C) != 0 {
		ckf(false, "timer channel non-empty after final Stop")
	}

	// 哨兵 job 作为屏障：之前排队的 job 都处理完才 close(ack)；
	// 定时器已停，屏障前后都不允许出现 tick 事件。
	barrier := make(chan struct{})
	jobs <- tmJob{ack: barrier}
	<-barrier

	totalJobs := 10 + int(seed%4)
	sent := make(map[int64]bool, totalJobs)
	for i := 0; i < totalJobs; i++ {
		v := seed*7919 + int64(i)*131
		sent[v] = true
		jobs <- tmJob{node: &tmNode{v: v, next: &tmNode{v: v + 1}}}
	}
	barrier = make(chan struct{})
	jobs <- tmJob{ack: barrier}
	var ticks, gotJobs int
	gotSums := make(map[int64]bool, totalJobs)
	waitBarrier := true
	for waitBarrier {
		select {
		case ev := <-out:
			switch ev.kind {
			case 0:
				ticks++
			case 1:
				gotJobs++
				gotSums[ev.sum] = true
			default:
				ck(false, "service loop observed corrupted pointer chain")
			}
		case <-barrier:
			waitBarrier = false
		}
	}
	// 屏障只保证服务循环处理完之前的 job；已投递到带缓冲 out 的事件需排空。
drain:
	for {
		select {
		case ev := <-out:
			switch ev.kind {
			case 0:
				ticks++
			case 1:
				gotJobs++
				gotSums[ev.sum] = true
			default:
				ck(false, "service loop observed corrupted pointer chain")
			}
		default:
			break drain
		}
	}
	ckf(ticks == 0, "ticks after Stop=%d want=0", ticks)
	ckf(gotJobs == totalJobs, "jobs forwarded=%d want=%d", gotJobs, totalJobs)
	ckf(len(gotSums) == totalJobs, "distinct job sums=%d want=%d", len(gotSums), totalJobs)
	for v := range sent {
		ckf(gotSums[v], "job value %d missing after timer/channel roundtrip", v)
	}
	select {
	case ev := <-out:
		ckf(false, "unexpected residual event kind=%d after barrier", ev.kind)
	default:
	}

	close(quit)
	<-loopDone
	t.Stop()
	drainTimer(t)
	gcStress()
	ck(head != nil && head.next != nil && head.v+1 == head.next.v, "retained chain corrupted after final GC")

	finish("l04_timer_channel_reset_reuse", seed)
}
