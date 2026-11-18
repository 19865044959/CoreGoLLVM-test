// L19: Ticker 驱动的 worker 持有指针状态，循环内周期性显式 GC；worker 达到
// 目标计数后自行 Stop ticker 并退出，主协程校验最终计数与状态内容完全确定。
package main

import (
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

type workState struct {
	n    int
	sum  int64
	hist []int64
}

var failed int32

const (
	targetTicks = 128
	gcEvery     = 16
	waitLimit   = 10 * time.Second
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

func tickValue(n int, seed int64) int64 {
	return int64(n)*37 + seed*13
}

func main() {
	seed := envSeed()
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()

	// 状态对象由 worker 独占持有，包含指针链；GC 扫描的是 worker 栈与堆。
	anchor := makeChain(seed * 5)
	st := &workState{hist: make([]int64, 0, targetTicks)}
	resCh := make(chan *workState, 1)
	var workerBad int32

	go func() {
		for {
			<-ticker.C
			st.n++
			v := tickValue(st.n, seed)
			st.sum += v
			st.hist = append(st.hist, v)
			if st.n%gcEvery == 0 {
				// 循环内显式 GC：计数 / 历史 / anchor 都必须完好。
				gcStress()
				if !chainOK(anchor, seed*5) {
					// 通过原子标记传给主协程，避免并发打印。
					atomic.StoreInt32(&workerBad, 1)
				}
			}
			if st.n >= targetTicks {
				// Stop 后不再接收 tick，worker 退出，计数自此不可变。
				ticker.Stop()
				resCh <- st
				return
			}
		}
	}()

	var got *workState
	select {
	case got = <-resCh:
	case <-time.After(waitLimit):
		ck(false, "ticker worker did not finish")
		finish("l19_ticker_worker")
		return
	}

	ckf(got != nil, "nil state")
	if got == nil {
		finish("l19_ticker_worker")
		return
	}
	ckf(got.n == targetTicks, "ticks=%d want=%d", got.n, targetTicks)
	ckf(len(got.hist) == targetTicks, "history len=%d want=%d", len(got.hist), targetTicks)
	var want int64
	for i := 1; i <= targetTicks; i++ {
		want += tickValue(i, seed)
	}
	ckf(got.sum == want, "sum=%d want=%d", got.sum, want)
	for i := 0; i < len(got.hist) && i < targetTicks; i++ {
		w := tickValue(i+1, seed)
		if got.hist[i] != w {
			ckf(false, "history[%d]=%d want=%d", i, got.hist[i], w)
			break
		}
	}
	ck(atomic.LoadInt32(&workerBad) == 0, "worker anchor chain corrupted during loop GC")
	ck(chainOK(anchor, seed*5), "worker anchor chain corrupted")

	// Stop 之后计数确定：再 GC 并重复读取，状态不得变化。
	gcStress()
	ckf(got.n == targetTicks, "count changed after Stop: %d", got.n)
	ckf(got.sum == want, "sum changed after Stop: %d", got.sum)
	if len(got.hist) > 0 {
		ck(got.hist[len(got.hist)-1] == tickValue(targetTicks, seed), "last tick value changed after Stop")
	}
	gcStress()
	ckf(got.n == targetTicks, "count changed after final GC: %d", got.n)

	fmt.Printf("ticks=%d gc_every=%d\n", got.n, gcEvery)
	finish("l19_ticker_worker")
}
