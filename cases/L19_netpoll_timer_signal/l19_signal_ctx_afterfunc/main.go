// L19: 信号驱动的 context 生命周期与 AfterFunc 清理组合。SIGUSR1 通过
// signal.NotifyContext 取消 worker 的 context；context.AfterFunc 注册的清理
// 回调必须恰好执行一次，执行后 stop 返回 false；worker 在 GC 后读取
// context.WithValue 携带的指针链。另一 NotifyContext 只监听 SIGUSR2，不得被
// SIGUSR1 误取消；父取消传播与 stop 幂等也要成立。仅 Linux 执行真实信号路径。
package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"runtime"
	"strconv"
	"sync/atomic"
	"syscall"
	"time"
)

type node struct {
	v    int64
	next *node
}

type chainKey struct{}

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

func chainOK(h *node, base int64) bool {
	if h == nil || h.v != base || h.next == nil || h.next.v != base+1 {
		return false
	}
	return h.next.next != nil && h.next.next.v == base+2 && h.next.next.next == nil
}

func waitDone(ch <-chan struct{}, what string) bool {
	select {
	case <-ch:
		return true
	case <-time.After(waitLimit):
		ckf(false, "%s: timeout", what)
		return false
	}
}

func main() {
	if runtime.GOOS != "linux" {
		fmt.Printf("SKIP: SIGUSR1 path requires linux (os=%s)\n", runtime.GOOS)
		finish("l19_signal_ctx_afterfunc")
		return
	}

	seed := envSeed()
	base := seed*53 + 17
	chain := makeChain(base)
	parent := context.WithValue(context.Background(), chainKey{}, chain)
	ctx, stop := signal.NotifyContext(parent, syscall.SIGUSR1)
	defer stop()

	afterRan := make(chan struct{}, 1)
	var afterCount int32
	stopAfter := context.AfterFunc(ctx, func() {
		atomic.AddInt32(&afterCount, 1)
		ch, _ := ctx.Value(chainKey{}).(*node)
		if !chainOK(ch, base) {
			ckf(false, "AfterFunc payload corrupted (seed=%d)", seed)
		}
		gcStress()
		if !chainOK(ch, base) {
			ckf(false, "AfterFunc payload corrupted after GC (seed=%d)", seed)
		}
		afterRan <- struct{}{}
	})

	workerDone := make(chan error, 1)
	go func() {
		<-ctx.Done()
		ch, _ := ctx.Value(chainKey{}).(*node)
		if !chainOK(ch, base) {
			workerDone <- fmt.Errorf("worker payload corrupted (seed=%d)", seed)
			return
		}
		gcStress()
		if !chainOK(ch, base) {
			workerDone <- fmt.Errorf("worker payload corrupted after GC (seed=%d)", seed)
			return
		}
		workerDone <- ctx.Err()
	}()

	select {
	case <-ctx.Done():
		ck(false, "context canceled before signal")
	default:
	}

	if err := syscall.Kill(os.Getpid(), syscall.SIGUSR1); err != nil {
		ckf(false, "kill SIGUSR1: %v", err)
	}
	select {
	case <-ctx.Done():
	case <-time.After(waitLimit):
		ckf(false, "context not canceled by SIGUSR1 (seed=%d)", seed)
	}
	ckf(errors.Is(ctx.Err(), context.Canceled), "ctx.Err=%v want=context.Canceled", ctx.Err())
	select {
	case <-afterRan:
	case <-time.After(waitLimit):
		ck(false, "context AfterFunc did not run")
	}
	ckf(atomic.LoadInt32(&afterCount) == 1, "AfterFunc ran %d times", atomic.LoadInt32(&afterCount))
	if stopAfter() {
		ck(false, "AfterFunc stop after execution must return false")
	}
	select {
	case err := <-workerDone:
		ckf(errors.Is(err, context.Canceled), "worker err=%v want=context.Canceled", err)
	case <-time.After(waitLimit):
		ck(false, "worker did not finish")
	}
	gcStress()
	ck(chainOK(chain, base), "main payload chain corrupted after GC")

	// 只监听 SIGUSR2 的 ctx 不得被 SIGUSR1 误取消。
	ctx2, stop2 := signal.NotifyContext(context.Background(), syscall.SIGUSR2)
	select {
	case <-ctx2.Done():
		ck(false, "SIGUSR1 canceled the SIGUSR2 context")
	default:
	}

	// 父 context 取消必须传播到子 NotifyContext，且不影响 ctx2。
	parent2, pcancel := context.WithCancel(context.Background())
	ctx3, stop3 := signal.NotifyContext(parent2, syscall.SIGUSR2)
	pcancel()
	if waitDone(ctx3.Done(), "parent cancel propagation") {
		ckf(errors.Is(ctx3.Err(), context.Canceled), "ctx3.Err=%v want=context.Canceled", ctx3.Err())
	}
	select {
	case <-ctx2.Done():
		ck(false, "unrelated ctx2 canceled by parent cancel")
	default:
	}

	// stop 幂等：重复调用不得 panic，也不得影响已取消状态。
	stop2()
	stop2()
	stop3()
	stop3()
	stop()

	// stop 在取消前调用时，AfterFunc 必须被撤销、不得执行。
	ctx4, stop4 := signal.NotifyContext(context.Background(), syscall.SIGUSR1)
	ran4 := make(chan struct{}, 1)
	af4 := context.AfterFunc(ctx4, func() { ran4 <- struct{}{} })
	if !af4() {
		ck(false, "AfterFunc stop before cancellation must return true")
	}
	stop4()
	sent := make(chan struct{})
	time.AfterFunc(0, func() { close(sent) })
	waitDone(sent, "afterfunc stop sentinel")
	gcStress()
	select {
	case <-ran4:
		ck(false, "stopped context AfterFunc ran")
	default:
	}

	gcStress()
	fmt.Printf("afterfunc_runs=%d\n", atomic.LoadInt32(&afterCount))
	finish("l19_signal_ctx_afterfunc")
}
