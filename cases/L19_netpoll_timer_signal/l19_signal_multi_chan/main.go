// L19: 多个 os/signal 通知通道。SIGUSR1 同时注册到 chA/chB，SIGUSR2 只注册到
// chC；验证广播、单独 signal.Stop(chA) 后 chA 不再收到而 chB 继续收到、重新
// Notify 后恢复。仅 Linux 运行真实信号路径，其他平台打印 SKIP 后按约定输出
// ALL PASS。每次信号迁移前后显式 GC 并校验栈上指针链。
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

// waitOn 从 ch 中取到 want 为止；取错信号或超时都记失败。
func waitOn(ch <-chan os.Signal, want os.Signal, label string) bool {
	timer := time.NewTimer(waitLimit)
	defer timer.Stop()
	for {
		select {
		case s := <-ch:
			if s == want {
				return true
			}
			ckf(false, "%s: got %v want %v", label, s, want)
			return false
		case <-timer.C:
			ckf(false, "%s: signal %v not received", label, want)
			return false
		}
	}
}

// expectEmpty 在顺序屏障之后检查 ch 没有遗留投递。
func expectEmpty(ch <-chan os.Signal, label string) {
	select {
	case s := <-ch:
		ckf(false, "%s: unexpected signal %v", label, s)
	default:
	}
}

func main() {
	if runtime.GOOS != "linux" {
		fmt.Printf("SKIP: multi-channel signal path requires linux (os=%s)\n", runtime.GOOS)
		finish("l19_signal_multi_chan")
		return
	}
	seed := envSeed()
	base := seed*1000 + 7
	p := makeChain(base)

	chA := make(chan os.Signal, 8)
	chB := make(chan os.Signal, 8)
	chC := make(chan os.Signal, 8)
	signal.Notify(chA, syscall.SIGUSR1)
	signal.Notify(chB, syscall.SIGUSR1)
	signal.Notify(chC, syscall.SIGUSR2)
	defer func() {
		signal.Stop(chA)
		signal.Stop(chB)
		signal.Stop(chC)
		signal.Ignore(syscall.SIGUSR1, syscall.SIGUSR2)
	}()

	usr1A, usr1B, usr2C := 0, 0, 0

	// 1) 同一信号广播到两个通道，SIGUSR2 通道保持安静。
	gcStress()
	if err := syscall.Kill(os.Getpid(), syscall.SIGUSR1); err != nil {
		ckf(false, "kill usr1 #1: %v", err)
	}
	if waitOn(chA, syscall.SIGUSR1, "broadcast-A") {
		usr1A++
	}
	if waitOn(chB, syscall.SIGUSR1, "broadcast-B") {
		usr1B++
	}
	expectEmpty(chC, "broadcast-C")
	ck(chainOK(p, base), "payload corrupted after broadcast + GC")

	// 2) 只停止 chA：chB 仍收到 SIGUSR1，chA 不得再收到。
	signal.Stop(chA)
	gcStress()
	if err := syscall.Kill(os.Getpid(), syscall.SIGUSR1); err != nil {
		ckf(false, "kill usr1 #2: %v", err)
	}
	if waitOn(chB, syscall.SIGUSR1, "post-stop-B") {
		usr1B++
	}
	expectEmpty(chA, "post-stop-A")
	ck(chainOK(p, base), "payload corrupted after Stop(chA) + GC")

	// 3) 重新 Notify chA：广播恢复。
	signal.Notify(chA, syscall.SIGUSR1)
	gcStress()
	if err := syscall.Kill(os.Getpid(), syscall.SIGUSR1); err != nil {
		ckf(false, "kill usr1 #3: %v", err)
	}
	if waitOn(chA, syscall.SIGUSR1, "re-notify-A") {
		usr1A++
	}
	if waitOn(chB, syscall.SIGUSR1, "re-notify-B") {
		usr1B++
	}
	gcStress()
	ck(chainOK(p, base), "payload corrupted after re-Notify + GC")

	// 4) 不同信号不相干：SIGUSR2 只进 chC。
	if err := syscall.Kill(os.Getpid(), syscall.SIGUSR2); err != nil {
		ckf(false, "kill usr2: %v", err)
	}
	if waitOn(chC, syscall.SIGUSR2, "usr2-C") {
		usr2C++
	}
	expectEmpty(chA, "usr2-A")
	expectEmpty(chB, "usr2-B")
	gcStress()
	ck(chainOK(p, base), "payload corrupted after SIGUSR2 + GC")

	ckf(usr1A == 2, "chA usr1=%d want=2", usr1A)
	ckf(usr1B == 3, "chB usr1=%d want=3", usr1B)
	ckf(usr2C == 1, "chC usr2=%d want=1", usr2C)

	signal.Stop(chA)
	signal.Stop(chB)
	signal.Stop(chC)
	expectEmpty(chA, "final-A")
	expectEmpty(chB, "final-B")
	expectEmpty(chC, "final-C")
	gcStress()
	ck(chainOK(p, base), "payload corrupted after final Stop + GC")

	fmt.Println("broadcast_usr1=2 stopped_chan_silent=1 cross_signal_isolated=1")
	finish("l19_signal_multi_chan")
}
