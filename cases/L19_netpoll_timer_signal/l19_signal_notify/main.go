// L19: os/signal 注册 SIGUSR1，syscall.Kill 向本进程自发自收；每收一个信号
// 前后显式 GC 并校验闭包/栈上的指针 payload，信号计数必须精确。仅 Linux 运行
// 真实信号路径，其他平台打印 SKIP 后按本仓约定输出 ALL PASS。
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

const (
	sigCount  = 16
	waitLimit = 5 * time.Second
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
	if runtime.GOOS != "linux" {
		fmt.Printf("SKIP: SIGUSR1 path requires linux (os=%s)\n", runtime.GOOS)
		finish("l19_signal_notify")
		return
	}

	seed := envSeed()
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, syscall.SIGUSR1)

	var got int
	var bad int32
	for i := 0; i < sigCount; i++ {
		base := int64(i)*41 + seed*7
		p := makeChain(base)
		gcStress()

		// 顺序自发自收：每个信号确认收到后再发下一个，避免标准信号合并。
		if err := syscall.Kill(os.Getpid(), syscall.SIGUSR1); err != nil {
			ckf(false, "kill %d: %v", i, err)
			break
		}
		select {
		case s := <-ch:
			got++
			if s != syscall.SIGUSR1 {
				atomic.StoreInt32(&bad, 1)
			}
			if !chainOK(p, base) {
				atomic.StoreInt32(&bad, 1)
			}
		case <-time.After(waitLimit):
			ckf(false, "signal %d not received", i)
			i = sigCount
			continue
		}
		// 收到信号后再次 GC，payload 与计数都必须稳定。
		gcStress()
		if !chainOK(p, base) {
			atomic.StoreInt32(&bad, 1)
		}
	}
	signal.Stop(ch)

	ckf(got == sigCount, "received=%d want=%d", got, sigCount)
	ckf(atomic.LoadInt32(&bad) == 0, "unexpected signal or corrupted payload")
	select {
	case s := <-ch:
		ckf(false, "extra signal after Stop: %v", s)
	default:
	}
	gcStress()
	ckf(got == sigCount, "count changed after final GC: %d", got)

	fmt.Printf("signals=%d\n", got)
	finish("l19_signal_notify")
}
