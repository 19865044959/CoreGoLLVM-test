// L04: 固定 goroutine 池 + 逐任务/池级取消。任务在检查点同时等待 release 与
// task ctx；取消的任务只能从 ctx.Done 醒来，不得完成。池级取消让 worker 从
// jobs/stop 的 select 中退出。GC 落在任务阻塞、worker 持栈指针时。
package main

import (
	"context"
	"fmt"
	"os"
	"runtime"
	"strconv"
	"sync"
	"sync/atomic"
)

type wpNode struct {
	v    int64
	next *wpNode
}

type wpTask struct {
	id      int
	ctx     context.Context
	cancel  context.CancelFunc
	head    *wpNode
	release chan struct{}
	started chan<- int
	status  int32 // 0 pending, 1 done, 2 canceled
}

type wpPool struct {
	jobs chan *wpTask
	stop <-chan struct{}
	wg   sync.WaitGroup
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

func newWPPool(workers int, stop <-chan struct{}) *wpPool {
	p := &wpPool{jobs: make(chan *wpTask, 128), stop: stop}
	p.wg.Add(workers)
	for i := 0; i < workers; i++ {
		go p.worker()
	}
	return p
}

func (p *wpPool) worker() {
	defer p.wg.Done()
	for {
		select {
		case t, ok := <-p.jobs:
			if !ok {
				return
			}
			t.run()
		case <-p.stop:
			return
		}
	}
}

func (t *wpTask) run() {
	t.started <- t.id
	select {
	case <-t.release:
		if t.ctx.Err() != nil {
			atomic.StoreInt32(&t.status, 2)
			return
		}
		ok := t.head != nil && t.head.next != nil && t.head.next.next != nil &&
			t.head.v+1 == t.head.next.v && t.head.next.v+1 == t.head.next.next.v
		ckf(ok, "task %d chain corrupted while holding it", t.id)
		atomic.StoreInt32(&t.status, 1)
	case <-t.ctx.Done():
		atomic.StoreInt32(&t.status, 2)
	}
}

func mkWPTask(ctx context.Context, id int, seed int64, started chan<- int, withRelease bool) *wpTask {
	tctx, cancel := context.WithCancel(ctx)
	b := seed*100003 + int64(id)*977
	t := &wpTask{
		id:      id,
		ctx:     tctx,
		cancel:  cancel,
		started: started,
		head:    &wpNode{v: b, next: &wpNode{v: b + 1, next: &wpNode{v: b + 2}}},
	}
	if withRelease {
		t.release = make(chan struct{})
	}
	return t
}

func main() {
	seed := envSeed()
	started := make(chan int, 128)

	// Phase A: 池内逐任务取消。16 个任务取消 id%4==2 的 4 个，
	// 其余 12 个在检查点等待 release。GC 发生在首批任务阻塞且持链时。
	stopA := make(chan struct{})
	poolA := newWPPool(4, stopA)
	tasksA := make([]*wpTask, 16)
	for id := range tasksA {
		t := mkWPTask(context.Background(), id, seed, started, true)
		tasksA[id] = t
		if id%4 == 2 {
			t.cancel()
		}
	}
	for _, t := range tasksA {
		poolA.jobs <- t
	}
	for i := 0; i < 4; i++ {
		<-started
	}
	gcStress()
	for _, t := range tasksA {
		if t.ctx.Err() == nil {
			close(t.release)
		}
	}
	close(poolA.jobs)
	poolA.wg.Wait()
	close(stopA)

	var doneA, canceledA int32
	for _, t := range tasksA {
		switch atomic.LoadInt32(&t.status) {
		case 1:
			doneA++
		case 2:
			canceledA++
		default:
			ckf(false, "phase A task %d never ran", t.id)
		}
	}
	ckf(doneA == 12, "phase A completed=%d want=12", doneA)
	ckf(canceledA == 4, "phase A canceled=%d want=4", canceledA)
	gcStress()
	for _, t := range tasksA {
		ok := t.head != nil && t.head.next != nil && t.head.next.next != nil &&
			t.head.v+1 == t.head.next.v && t.head.next.v+1 == t.head.next.next.v
		ckf(ok, "phase A task %d retained chain corrupted", t.id)
		t.cancel()
	}

	// Phase B: 池级取消。12 个任务尚未完成时取消池 ctx，worker 从
	// jobs/stop select 退出，所有任务（运行中或排队中）都观察到取消。
	poolCtx, poolCancel := context.WithCancel(context.Background())
	poolB := newWPPool(4, poolCtx.Done())
	tasksB := make([]*wpTask, 12)
	for id := range tasksB {
		tasksB[id] = mkWPTask(poolCtx, 100+id, seed, started, true)
	}
	for _, t := range tasksB {
		poolB.jobs <- t
	}
	for i := 0; i < 4; i++ {
		<-started
	}
	gcStress()
	poolCancel()
	poolB.wg.Wait()

	var canceledB int
	for _, t := range tasksB {
		st := atomic.LoadInt32(&t.status)
		if st == 1 {
			ckf(false, "phase B task %d completed despite pool cancel", t.id)
		}
		if st == 2 || (st == 0 && t.ctx.Err() == context.Canceled) {
			canceledB++
		}
		t.cancel()
	}
	ckf(canceledB == len(tasksB), "phase B canceled-or-queued=%d want=%d", canceledB, len(tasksB))
	close(poolB.jobs)

	gcStress()
	for _, t := range tasksB {
		ok := t.head != nil && t.head.next != nil && t.head.next.next != nil &&
			t.head.v+1 == t.head.next.v && t.head.next.v+1 == t.head.next.next.v
		ckf(ok, "phase B task %d retained chain corrupted", t.id)
	}

	finish("l04_worker_pool_cancel_drain", seed)
}
