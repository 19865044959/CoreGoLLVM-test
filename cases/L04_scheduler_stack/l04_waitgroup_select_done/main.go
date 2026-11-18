// L04: WaitGroup + select。完成信号由 goroutine 在 wg.Wait() 后 close(done)，
// 主协程用 select 同时收进度事件与 done。三个阶段分别验证全量完成、启动前
// 取消（0 事件）与事件中途取消（恰好 N 事件）；GC 落在 worker 持有链时。
package main

import (
	"fmt"
	"os"
	"runtime"
	"strconv"
	"sync"
	"sync/atomic"
)

type wgNode struct {
	v    int64
	next *wgNode
}

type wgEvent struct {
	id  int
	seq int
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

func allDoneOf(wg *sync.WaitGroup) <-chan struct{} {
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()
	return done
}

func main() {
	seed := envSeed()
	const workers = 24
	const eventsPerWorker = 16
	want := workers * eventsPerWorker
	mkHead := func(id int) *wgNode {
		b := seed*100003 + int64(id)*977
		return &wgNode{v: b, next: &wgNode{v: b + 1, next: &wgNode{v: b + 2}}}
	}
	verify := func(id int, head *wgNode) bool {
		b := seed*100003 + int64(id)*977
		return head != nil && head.next != nil && head.next.next != nil &&
			head.v == b && head.next.v == b+1 && head.next.next.v == b+2
	}

	// Phase A: 全部 worker 在 start 上阻塞时 GC，随后完成 24*16 个事件。
	var wgA sync.WaitGroup
	wgA.Add(workers)
	doneA := allDoneOf(&wgA)
	startA := make(chan struct{})
	progressA := make(chan wgEvent, want)
	headsA := make([]*wgNode, workers)
	for id := 0; id < workers; id++ {
		go func(id int) {
			defer wgA.Done()
			head := mkHead(id)
			headsA[id] = head
			<-startA
			for seq := 0; seq < eventsPerWorker; seq++ {
				if !verify(id, head) {
					ckf(false, "phase A worker %d chain corrupted at seq %d", id, seq)
				}
				progressA <- wgEvent{id: id, seq: seq}
			}
		}(id)
	}
	select {
	case <-doneA:
		ck(false, "phase A allDone closed before workers started")
	default:
	}
	gcStress()
	close(startA)

	seenA := make([][]bool, workers)
	for i := range seenA {
		seenA[i] = make([]bool, eventsPerWorker)
	}
	gotA := 0
	finishedA := false
	for gotA < want || !finishedA {
		select {
		case ev := <-progressA:
			if ev.id < 0 || ev.id >= workers || ev.seq < 0 || ev.seq >= eventsPerWorker {
				ckf(false, "phase A bad event id=%d seq=%d", ev.id, ev.seq)
				continue
			}
			if seenA[ev.id][ev.seq] {
				ckf(false, "phase A duplicate event id=%d seq=%d", ev.id, ev.seq)
			}
			seenA[ev.id][ev.seq] = true
			gotA++
		case <-doneA:
			finishedA = true
		}
	}
	ckf(gotA == want, "phase A events=%d want=%d", gotA, want)
	select {
	case ev := <-progressA:
		ckf(false, "phase A residual event after done id=%d seq=%d", ev.id, ev.seq)
	default:
	}
	for id := 0; id < workers; id++ {
		ckf(verify(id, headsA[id]), "phase A worker %d retained chain corrupted", id)
	}

	// Phase B: halt 在启动前已关闭，所有 worker 走 select 取消分支，0 事件。
	var wgB sync.WaitGroup
	wgB.Add(workers)
	doneB := allDoneOf(&wgB)
	startB := make(chan struct{})
	haltB := make(chan struct{})
	progressB := make(chan wgEvent, 1)
	for id := 0; id < workers; id++ {
		go func(id int) {
			defer wgB.Done()
			head := mkHead(100 + id)
			<-startB
			select {
			case <-haltB:
				ckf(verify(100+id, head), "phase B worker %d chain corrupted before cancel", id)
				return
			default:
			}
			progressB <- wgEvent{id: id, seq: 0}
		}(id)
	}
	close(haltB)
	close(startB)
	select {
	case ev := <-progressB:
		ckf(false, "phase B worker %d ran despite pre-start halt", ev.id)
	case <-doneB:
	}

	// Phase C: 每个 worker 恰好发 1 个事件后阻塞在 halt 上；GC 后放行。
	var wgC sync.WaitGroup
	wgC.Add(workers)
	doneC := allDoneOf(&wgC)
	startC := make(chan struct{})
	haltC := make(chan struct{})
	progressC := make(chan wgEvent, workers)
	headsC := make([]*wgNode, workers)
	for id := 0; id < workers; id++ {
		go func(id int) {
			defer wgC.Done()
			head := mkHead(200 + id)
			headsC[id] = head
			<-startC
			progressC <- wgEvent{id: id, seq: 0}
			<-haltC
			ckf(verify(200+id, head), "phase C worker %d chain corrupted while halted", id)
		}(id)
	}
	gcStress()
	close(startC)
	seenC := make([]bool, workers)
	gotC := 0
	for gotC < workers {
		select {
		case ev := <-progressC:
			if ev.id < 0 || ev.id >= workers || seenC[ev.id] {
				ckf(false, "phase C bad/duplicate event id=%d", ev.id)
				continue
			}
			seenC[ev.id] = true
			gotC++
		case <-doneC:
			ckf(false, "phase C allDone before all progress events")
		}
	}
	select {
	case <-doneC:
		ck(false, "phase C allDone closed while workers still halted")
	default:
	}
	gcStress()
	close(haltC)
	<-doneC
	ckf(gotC == workers, "phase C events=%d want=%d", gotC, workers)
	select {
	case ev := <-progressC:
		ckf(false, "phase C residual event id=%d", ev.id)
	default:
	}

	gcStress()
	for id := 0; id < workers; id++ {
		ckf(verify(200+id, headsC[id]), "phase C worker %d retained chain corrupted", id)
	}
	finish("l04_waitgroup_select_done", seed)
}
