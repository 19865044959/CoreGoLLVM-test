// L19: 信号、看门狗定时器与工作队列在同一 select 中的优雅关闭状态机。
// 偶数轮：先自发 SIGUSR1，事件循环必须在处理固定 8 个任务的同时消费信号，
// 之后 Stop 1h 看门狗返回 true 且回调 0 次。奇数轮：不发信号，零延时看门狗
// AfterFunc 必须触发并关闭 watchCh，Stop 返回 false，信号通道保持空闲。
// 每轮穿插显式 GC 校验 payload 链；仅 Linux 执行真实信号路径。
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

type job struct {
	id   int
	base int64
	head *node
}

var failed int32

const (
	rounds       = 8
	jobsPerRound = 8
	waitLimit    = 5 * time.Second
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

func chainOK(h *node, base int64) bool {
	if h == nil || h.v != base || h.next == nil || h.next.v != base+1 {
		return false
	}
	return h.next.next != nil && h.next.next.v == base+2 && h.next.next.next == nil
}

func main() {
	if runtime.GOOS != "linux" {
		fmt.Printf("SKIP: SIGUSR1 path requires linux (os=%s)\n", runtime.GOOS)
		finish("l19_signal_timer_shutdown")
		return
	}

	seed := envSeed()
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGUSR1)
	signals := 0

	for i := 0; i < rounds; i++ {
		if i%2 == 0 {
			// 信号先到：事件循环同时排空工作队列，看门狗保持活跃到最后。
			base := seed*61 + int64(i)*19 + 5
			chain := makeChain(base)
			var watchFired int32
			watchdog := time.AfterFunc(time.Hour, func() {
				atomic.AddInt32(&watchFired, 1)
			})
			work := make(chan *job, jobsPerRound)
			for j := 0; j < jobsPerRound; j++ {
				jb := &job{id: j, base: base + int64(j)*3}
				jb.head = makeChain(jb.base)
				work <- jb
			}
			if err := syscall.Kill(os.Getpid(), syscall.SIGUSR1); err != nil {
				ckf(false, "round %d kill: %v", i, err)
			}
			processed, got := 0, 0
			guard := time.After(waitLimit)
			for processed < jobsPerRound || got == 0 {
				select {
				case s := <-sigCh:
					got++
					signals++
					if s != syscall.SIGUSR1 {
						ckf(false, "round %d got %v want SIGUSR1", i, s)
					}
				case jb := <-work:
					if jb.id != processed {
						ckf(false, "round %d job id=%d want=%d", i, jb.id, processed)
					}
					if !chainOK(jb.head, jb.base) {
						ckf(false, "round %d job %d payload corrupted", i, jb.id)
					}
					processed++
					if processed%4 == 0 {
						gcStress()
						if !chainOK(chain, base) || !chainOK(jb.head, jb.base) {
							ckf(false, "round %d job payload corrupted after GC", i)
						}
					}
				case <-guard:
					ckf(false, "round %d shutdown select stuck (processed=%d got=%d)", i, processed, got)
					processed = jobsPerRound
					got = 1
				}
			}
			ckf(processed == jobsPerRound, "round %d processed=%d want=%d", i, processed, jobsPerRound)
			ckf(got == 1, "round %d signals consumed=%d want=1", i, got)
			if !watchdog.Stop() {
				ckf(false, "round %d watchdog fired while signal path was taken", i)
			}
			ckf(atomic.LoadInt32(&watchFired) == 0, "round %d watchdog callbacks=%d want=0", i, atomic.LoadInt32(&watchFired))
			gcStress()
			ck(chainOK(chain, base), "round payload chain corrupted after signal shutdown")
		} else {
			// 无信号：零延时看门狗必须赢得 select。
			base := seed*67 + int64(i)*23 + 9
			chain := makeChain(base)
			var watchFired int32
			watchCh := make(chan struct{})
			watchdog := time.AfterFunc(0, func() {
				atomic.AddInt32(&watchFired, 1)
				if !chainOK(chain, base) {
					ckf(false, "round %d watchdog payload corrupted", i)
				}
				gcStress()
				if !chainOK(chain, base) {
					ckf(false, "round %d watchdog payload corrupted after GC", i)
				}
				close(watchCh)
			})
			select {
			case s := <-sigCh:
				ckf(false, "round %d unexpected signal %v on timer path", i, s)
			case <-watchCh:
			case <-time.After(waitLimit):
				ckf(false, "round %d watchdog did not fire", i)
			}
			ckf(atomic.LoadInt32(&watchFired) == 1, "round %d watchdog callbacks=%d want=1", i, atomic.LoadInt32(&watchFired))
			if watchdog.Stop() {
				ckf(false, "round %d Stop on fired watchdog returned true", i)
			}
			select {
			case s := <-sigCh:
				ckf(false, "round %d stray signal %v", i, s)
			default:
			}
			gcStress()
			ck(chainOK(chain, base), "round payload chain corrupted after watchdog shutdown")
		}
	}

	ckf(signals == rounds/2, "signals=%d want=%d", signals, rounds/2)
	signal.Stop(sigCh)
	gcStress()
	fmt.Printf("signals=%d rounds=%d jobs_per_round=%d\n", signals, rounds, jobsPerRound)
	finish("l19_signal_timer_shutdown")
}
