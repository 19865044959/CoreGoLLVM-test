// L19: os/signal.NotifyContext 的信号驱动取消路径。SIGUSR1/SIGUSR2 分别注册到
// 两个独立 NotifyContext：只取消对应 ctx，另一 ctx 不得被误取消；父 context
// 取消时子 NotifyContext 也必须以 context.Canceled 结束。stop 可重复/并发调用，
// 取消原因保持不变。每次信号迁移前后显式 GC 并校验指针链。仅 Linux 执行。
package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"runtime"
	"strconv"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
)

type node struct {
	v    int64
	next *node
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

func chainOK(h *node, base int64) bool {
	if h == nil || h.v != base || h.next == nil || h.next.v != base+1 {
		return false
	}
	return h.next.next != nil && h.next.next.v == base+2 && h.next.next.next == nil
}

func waitDone(ctx context.Context, what string) bool {
	select {
	case <-ctx.Done():
		return true
	case <-time.After(waitLimit):
		ckf(false, "%s: Done not closed", what)
		return false
	}
}

func main() {
	if runtime.GOOS != "linux" {
		fmt.Printf("SKIP: SIGUSR1/SIGUSR2 path requires linux (os=%s)\n", runtime.GOOS)
		finish("l19_signal_notify_context")
		return
	}

	seed := envSeed()
	base := seed*37 + 17
	chain := makeChain(base)

	ctxC, stopC := signal.NotifyContext(context.Background(), syscall.SIGUSR1)
	ctxD, stopD := signal.NotifyContext(context.Background(), syscall.SIGUSR2)

	// SIGUSR1 只取消 ctxC。
	if err := syscall.Kill(os.Getpid(), syscall.SIGUSR1); err != nil {
		ckf(false, "kill SIGUSR1: %v", err)
	}
	if waitDone(ctxC, "ctxC after SIGUSR1") {
		ckf(okCanceled(ctxC), "ctxC Err=%v want context.Canceled", ctxC.Err())
	}
	select {
	case <-ctxD.Done():
		ck(false, "ctxD must not be canceled by SIGUSR1")
	default:
	}
	gcStress()
	ck(chainOK(chain, base), "payload corrupted after SIGUSR1")
	ckf(okCanceled(ctxC), "ctxC Err changed after GC: %v", ctxC.Err())
	ck(ctxD.Err() == nil, "ctxD must remain active after SIGUSR1")

	// SIGUSR2 只取消 ctxD。
	if err := syscall.Kill(os.Getpid(), syscall.SIGUSR2); err != nil {
		ckf(false, "kill SIGUSR2: %v", err)
	}
	if waitDone(ctxD, "ctxD after SIGUSR2") {
		ckf(okCanceled(ctxD), "ctxD Err=%v want context.Canceled", ctxD.Err())
	}
	gcStress()
	ck(chainOK(chain, base), "payload corrupted after SIGUSR2")

	// 父 context 取消：子 NotifyContext 立即结束，无需信号。
	parent, cancelParent := context.WithCancel(context.Background())
	ctxP, stopP := signal.NotifyContext(parent, syscall.SIGUSR1)
	cancelParent()
	if waitDone(ctxP, "ctxP after parent cancel") {
		ckf(okCanceled(ctxP), "ctxP Err=%v want context.Canceled", ctxP.Err())
	}
	ck(chainOK(chain, base), "payload corrupted after parent cancel")

	// stop 可重复、可并发调用；取消原因与 Done 状态不得改变。
	stopP()
	stopP()
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			stopC()
			stopD()
		}()
	}
	wg.Wait()
	stopC()
	stopD()

	ckf(okCanceled(ctxC), "ctxC Err after stop: %v", ctxC.Err())
	ckf(okCanceled(ctxD), "ctxD Err after stop: %v", ctxD.Err())
	ckf(okCanceled(ctxP), "ctxP Err after stop: %v", ctxP.Err())
	select {
	case <-ctxC.Done():
	default:
		ck(false, "ctxC Done must stay closed")
	}
	gcStress()
	ck(chainOK(chain, base), "payload corrupted after stops")

	fmt.Printf("signals=canceled ctxC=%v ctxD=%v\n", ctxC.Err(), ctxD.Err())
	finish("l19_signal_notify_context")
}

func okCanceled(ctx context.Context) bool {
	return errors.Is(ctx.Err(), context.Canceled)
}
