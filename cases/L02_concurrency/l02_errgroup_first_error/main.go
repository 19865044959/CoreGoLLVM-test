// L02: errgroup 类模式（标准库实现）：并发子任务、首个错误取消派生 context、
// Wait 收敛并返回首个错误；成功组与失败组各验证一次，GC 穿插在任务 payload 上。
package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"runtime"
	"strconv"
	"sync"
	"sync/atomic"
)

var (
	errBoom = errors.New("boom")
	errLate = errors.New("late error")
)

type payload struct {
	id  int
	v   int64
	tag string
}

type group struct {
	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
	once   sync.Once
	err    error
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

func newGroup(parent context.Context) *group {
	ctx, cancel := context.WithCancel(parent)
	return &group{ctx: ctx, cancel: cancel}
}

func (g *group) Go(f func(ctx context.Context) error) {
	g.wg.Add(1)
	go func() {
		defer g.wg.Done()
		if err := f(g.ctx); err != nil {
			g.once.Do(func() {
				g.err = err
				g.cancel()
			})
		}
	}()
}

func (g *group) Wait() error {
	g.wg.Wait()
	g.cancel()
	return g.err
}

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

	// 阶段一：全部成功。
	sg := newGroup(context.Background())
	var success int32
	sp := make([]*payload, 8)
	for i := 0; i < 8; i++ {
		p := &payload{id: i, v: seed + int64(i), tag: "ok"}
		sp[i] = p
		sg.Go(func(ctx context.Context) error {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			atomic.AddInt32(&success, 1)
			if p.v != seed+int64(p.id) || p.tag != "ok" {
				ck(false, "success payload corrupted before GC")
			}
			gcStress()
			if p.v != seed+int64(p.id) || p.tag != "ok" {
				ck(false, "success payload corrupted after GC")
			}
			return nil
		})
	}
	err := sg.Wait()
	ck(err == nil, "successful group returned error")
	ck(atomic.LoadInt32(&success) == 8, fmt.Sprintf("success tasks=%d want=8", success))
	ck(sg.ctx.Err() == context.Canceled, "Wait did not cancel derived context")

	// 阶段二：首个错误取消其余任务。
	fg := newGroup(context.Background())
	var started int32
	var finishedOK int32
	failGate := make(chan struct{})
	fp := make([]*payload, 6)
	for i := 0; i < 6; i++ {
		p := &payload{id: i, v: seed + int64(i)*11, tag: "fail"}
		fp[i] = p
		fg.Go(func(ctx context.Context) error {
			atomic.AddInt32(&started, 1)
			if p.id == 2 {
				<-failGate
				gcStress()
				if p.v != seed+int64(p.id)*11 || p.tag != "fail" {
					ck(false, "failing payload corrupted after GC")
				}
				return errBoom
			}
			<-ctx.Done()
			gcStress()
			if p.v != seed+int64(p.id)*11 || p.tag != "fail" {
				ck(false, "canceled payload corrupted after GC")
			}
			atomic.AddInt32(&finishedOK, 1)
			if p.id == 5 {
				return errLate
			}
			return nil
		})
	}

	waitFor(func() bool { return atomic.LoadInt32(&started) == 6 }, "not all group tasks started")
	close(failGate)
	err = fg.Wait()
	ck(errors.Is(err, errBoom), fmt.Sprintf("Wait error=%v want boom", err))
	ck(!errors.Is(err, errLate), "late error must not replace first error")
	ck(atomic.LoadInt32(&finishedOK) == 5, fmt.Sprintf("canceled-ok tasks=%d want=5", finishedOK))
	ck(fg.ctx.Err() == context.Canceled, "first error did not cancel group context")

	gcStress()
	for _, p := range fp {
		if p.tag != "fail" || p.v != seed+int64(p.id)*11 {
			ck(false, "task payload corrupted by final GC")
		}
	}

	fmt.Printf("success_tasks=8 failure_tasks=6 first_error=boom\n")
	finish("l02_errgroup_first_error")
}
