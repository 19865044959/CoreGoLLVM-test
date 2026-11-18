// L19: context.WithTimeout 的到期/取消路径。32 轮 1ms 超时，worker 阻塞在
// ctx.Done 上，主协程在等待期间显式 GC 并校验 context value 携带的指针链；
// 另测父 ctx 取消、已过期 ctx、context.AfterFunc 提前 stop 三类路径。
package main

import (
	"context"
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

type ctxKey struct{}

type ctxResult struct {
	err     error
	payload *node
}

var failed int32

const waitLimit = 5 * time.Second

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

func makeChain(base int64) *node {
	return &node{v: base, next: &node{v: base + 1, next: &node{v: base + 2}}}
}

func chainOK(head *node, base int64) bool {
	if head == nil || head.v != base || head.next == nil || head.next.v != base+1 {
		return false
	}
	return head.next.next != nil && head.next.next.v == base+2 && head.next.next.next == nil
}

func main() {
	seed := envSeed()
	const rounds = 32
	key := ctxKey{}

	// 1) WithTimeout 到期：worker 阻塞在 Done 上，期间 GC。
	for i := 0; i < rounds; i++ {
		base := seed*10000 + int64(i)*7 + 1
		p := makeChain(base)
		ctx, cancel := context.WithTimeout(context.Background(), time.Millisecond)
		ctx = context.WithValue(ctx, key, p)
		if _, ok := ctx.Deadline(); !ok {
			ckf(false, "WithTimeout has no deadline (seed=%d round=%d)", seed, i)
		}
		waiting := make(chan struct{}, 1)
		res := make(chan ctxResult, 1)
		go func() {
			waiting <- struct{}{}
			<-ctx.Done()
			v, _ := ctx.Value(key).(*node)
			res <- ctxResult{err: ctx.Err(), payload: v}
		}()
		<-waiting
		gcStress()
		select {
		case r := <-res:
			ckf(r.err == context.DeadlineExceeded, "timeout err=%v want=DeadlineExceeded (seed=%d round=%d)",
				r.err, seed, i)
			ckf(chainOK(r.payload, base), "timeout payload corrupted (seed=%d round=%d)", seed, i)
		case <-time.After(waitLimit):
			ckf(false, "timeout ctx did not fire (seed=%d round=%d)", seed, i)
		}
		cancel()
		cancel() // CancelFunc 幂等
		gcStress()
		ckf(chainOK(p, base), "timeout chain corrupted after cancel (seed=%d round=%d)", seed, i)
	}

	// 2) 父 ctx 取消：WithTimeout 子 ctx 必须返回 Canceled，而不是等待到期。
	parent, pcancel := context.WithCancel(context.Background())
	base2 := seed*10000 + 9001
	p2 := makeChain(base2)
	ctx2, cancel2 := context.WithTimeout(parent, time.Hour)
	ctx2 = context.WithValue(ctx2, key, p2)
	waiting2 := make(chan struct{}, 1)
	res2 := make(chan ctxResult, 1)
	go func() {
		waiting2 <- struct{}{}
		<-ctx2.Done()
		v, _ := ctx2.Value(key).(*node)
		res2 <- ctxResult{err: ctx2.Err(), payload: v}
	}()
	<-waiting2
	gcStress()
	pcancel()
	select {
	case r := <-res2:
		ckf(r.err == context.Canceled, "parent cancel err=%v want=Canceled (seed=%d)", r.err, seed)
		ckf(chainOK(r.payload, base2), "parent-cancel payload corrupted (seed=%d)", seed)
	case <-time.After(waitLimit):
		ck(false, "parent-cancel ctx did not fire")
	}
	cancel2()
	gcStress()
	ck(chainOK(p2, base2), "parent-cancel chain corrupted after GC")

	// 3) 负超时：WithTimeout 立即到期，Done 已关闭，Err 为 DeadlineExceeded。
	exp, cancelExp := context.WithTimeout(context.Background(), -time.Second)
	select {
	case <-exp.Done():
	default:
		ck(false, "already-expired WithTimeout Done not closed")
	}
	ckf(exp.Err() == context.DeadlineExceeded, "expired err=%v want=DeadlineExceeded (seed=%d)", exp.Err(), seed)
	cancelExp()
	gcStress()

	// 4) context.AfterFunc：到期回调执行一次，回调后 stop 返回 false。
	base4 := seed*10000 + 9100
	p4 := makeChain(base4)
	ctx4, cancel4 := context.WithTimeout(context.Background(), time.Millisecond)
	called := make(chan struct{}, 2)
	stop4 := context.AfterFunc(ctx4, func() {
		if !chainOK(p4, base4) {
			atomic.AddInt32(&failed, 1)
		}
		called <- struct{}{}
	})
	select {
	case <-called:
	case <-time.After(waitLimit):
		ckf(false, "context.AfterFunc did not run (seed=%d)", seed)
	}
	ck(!stop4(), "stop after AfterFunc ran returned true")
	cancel4()
	gcStress()
	ck(chainOK(p4, base4), "AfterFunc payload corrupted after GC")

	// 5) 取消前 stop：回调不得启动，stop 返回 true。
	ctx5, cancel5 := context.WithTimeout(context.Background(), time.Hour)
	ck(ctx5.Err() == nil, "fresh WithTimeout err is not nil")
	var never int32
	stop5 := context.AfterFunc(ctx5, func() {
		atomic.AddInt32(&never, 1)
	})
	ckf(stop5(), "stop before cancel returned false (seed=%d)", seed)
	cancel5()
	gcStress()
	ckf(atomic.LoadInt32(&never) == 0, "stopped AfterFunc ran %d times (seed=%d)",
		atomic.LoadInt32(&never), seed)

	fmt.Printf("timeout_rounds=%d paths=5\n", rounds)
	finish("l19_context_timeout_path")
}
