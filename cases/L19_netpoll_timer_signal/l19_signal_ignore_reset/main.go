// L19: signal.Ignore 与 signal.Reset 语义。Linux 上自发 SIGUSR1：
// Notify 基线 -> Ignore 后信号被丢弃（以 SIGUSR2 作为顺序屏障）-> Reset
// 恢复默认可通知状态后重新 Notify 并再次收信号。每次迁移穿插 GC 与指针校验。
package main

import (
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

func drainSignals(ch <-chan os.Signal) int {
	n := 0
	for {
		select {
		case <-ch:
			n++
		default:
			return n
		}
	}
}

var ignoredStray int32

// waitUSR2 等待 SIGUSR2；期间若出现 SIGUSR1 说明 Ignore 未生效。
func waitUSR2(ch <-chan os.Signal, label string, seed int64) bool {
	timer := time.NewTimer(waitLimit)
	defer timer.Stop()
	for {
		select {
		case s := <-ch:
			if s == syscall.SIGUSR2 {
				return true
			}
			if s == syscall.SIGUSR1 {
				atomic.AddInt32(&ignoredStray, 1)
			}
		case <-timer.C:
			ckf(false, "%s: SIGUSR2 barrier not received (seed=%d)", label, seed)
			return false
		}
	}
}

func waitUSR1(ch <-chan os.Signal, label string, seed int64) bool {
	timer := time.NewTimer(waitLimit)
	defer timer.Stop()
	for {
		select {
		case s := <-ch:
			if s == syscall.SIGUSR1 {
				return true
			}
			ckf(false, "%s: unexpected signal %v (seed=%d)", label, s, seed)
		case <-timer.C:
			ckf(false, "%s: SIGUSR1 not received (seed=%d)", label, seed)
			return false
		}
	}
}

func main() {
	if runtime.GOOS != "linux" {
		fmt.Printf("SKIP: signal.Ignore/Reset path requires linux (os=%s)\n", runtime.GOOS)
		finish("l19_signal_ignore_reset")
		return
	}
	seed := envSeed()
	base := seed*1000 + 1

	ch := make(chan os.Signal, 16)
	signal.Notify(ch, syscall.SIGUSR1, syscall.SIGUSR2)
	defer signal.Stop(ch)

	// 1) Notify 基线：自发 SIGUSR1 必须收到。
	if err := syscall.Kill(os.Getpid(), syscall.SIGUSR1); err != nil {
		ckf(false, "baseline kill: %v (seed=%d)", err, seed)
	}
	ckf(waitUSR1(ch, "baseline", seed), "baseline signal lost (seed=%d)", seed)
	ckf(drainSignals(ch) == 0, "unexpected extra signal after baseline (seed=%d)", seed)

	// 2) Ignore 撤销 Notify：连发 4 个 SIGUSR1，进程不得退出、ch 不得收到。
	signal.Ignore(syscall.SIGUSR1)
	signal.Ignore(syscall.SIGUSR1) // 幂等
	p := makeChain(base)
	for i := 0; i < 4; i++ {
		gcStress()
		if err := syscall.Kill(os.Getpid(), syscall.SIGUSR1); err != nil {
			ckf(false, "ignored kill %d: %v (seed=%d)", i, err, seed)
		}
	}
	// 顺序屏障：先发 SIGUSR2 并等到，再用第二个 SIGUSR2 确认队列已清。
	if err := syscall.Kill(os.Getpid(), syscall.SIGUSR2); err != nil {
		ckf(false, "barrier kill 1: %v (seed=%d)", err, seed)
	}
	ckf(waitUSR2(ch, "barrier-1", seed), "barrier 1 failed (seed=%d)", seed)
	if err := syscall.Kill(os.Getpid(), syscall.SIGUSR2); err != nil {
		ckf(false, "barrier kill 2: %v (seed=%d)", err, seed)
	}
	ckf(waitUSR2(ch, "barrier-2", seed), "barrier 2 failed (seed=%d)", seed)
	gcStress()
	ckf(atomic.LoadInt32(&ignoredStray) == 0, "ignored SIGUSR1 was delivered %d times (seed=%d)",
		atomic.LoadInt32(&ignoredStray), seed)
	ckf(drainSignals(ch) == 0, "stray signal after ignore barriers (seed=%d)", seed)
	ck(chainOK(p, base), "payload corrupted while signal ignored + GC")

	// 3) Reset 恢复默认状态，立即重新 Notify 后必须再次收到 SIGUSR1。
	signal.Reset(syscall.SIGUSR1)
	signal.Notify(ch, syscall.SIGUSR1)
	for i := 0; i < 2; i++ {
		if err := syscall.Kill(os.Getpid(), syscall.SIGUSR1); err != nil {
			ckf(false, "post-reset kill %d: %v (seed=%d)", i, err, seed)
		}
		ckf(waitUSR1(ch, "post-reset", seed), "signal lost after Reset+Notify (seed=%d)", seed)
		gcStress()
		ck(chainOK(p, base), "payload corrupted after Reset+Notify + GC")
	}
	signal.Stop(ch)
	ckf(drainSignals(ch) == 0, "extra signal after final Stop (seed=%d)", seed)
	signal.Ignore(syscall.SIGUSR1) // 防止退出前默认动作终止进程
	gcStress()
	ck(chainOK(p, base), "payload corrupted after final GC")

	fmt.Println("notify_baseline=1 ignored_sends=4 reset_notify=2")
	finish("l19_signal_ignore_reset")
}
