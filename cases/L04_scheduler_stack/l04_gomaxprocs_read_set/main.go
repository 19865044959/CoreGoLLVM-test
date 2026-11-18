// L04: GOMAXPROCS 读取/设置：保存旧值，依次切到 1/2/4/旧值，每个设置下运行
// 固定协调量工作负载并穿插 GC；并发读者持续读取，所有观测值必须落在合法集合内。
package main

import (
	"fmt"
	"os"
	"runtime"
	"strconv"
	"sync"
	"sync/atomic"
)

type gNode struct {
	v    int64
	next *gNode
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

func expectedLocal(w, iters int) int64 {
	var s int64
	for i := 0; i < iters; i++ {
		s += int64((i ^ w) & 7)
	}
	return s
}

func procWorkload(procs, workers, iters int, seed int64) {
	nodes := make([]*gNode, workers)
	sums := make([]int64, workers)
	var wg sync.WaitGroup
	var bad int32

	wg.Add(workers)
	for w := 0; w < workers; w++ {
		go func(w int) {
			defer wg.Done()
			b := seed*131 + int64(procs)*1000 + int64(w)*17
			head := &gNode{v: b, next: &gNode{v: b + 1}}
			local := int64(0)
			for i := 0; i < iters; i++ {
				local += int64((i ^ w) & 7)
				if i&63 == 0 {
					runtime.Gosched()
				}
			}
			if head.next == nil || head.next.v != b+1 {
				atomic.AddInt32(&bad, 1)
			}
			nodes[w] = head
			sums[w] = local + b + head.next.v
		}(w)
	}
	wg.Wait()

	for w := 0; w < workers; w++ {
		b := seed*131 + int64(procs)*1000 + int64(w)*17
		want := expectedLocal(w, iters) + b + b + 1
		ckf(sums[w] == want, "procs=%d worker %d sum=%d want=%d", procs, w, sums[w], want)
	}
	ckf(atomic.LoadInt32(&bad) == 0, "procs=%d chain corrupted", procs)

	gcStress()
	for w := 0; w < workers; w++ {
		b := seed*131 + int64(procs)*1000 + int64(w)*17
		h := nodes[w]
		if h == nil || h.next == nil {
			ckf(false, "procs=%d worker %d chain missing after GC", procs, w)
			continue
		}
		ckf(h.v == b && h.next.v == b+1, "procs=%d worker %d chain corrupted after GC", procs, w)
	}
}

func main() {
	seed := envSeed()
	old := runtime.GOMAXPROCS(0)
	ck(old >= 1, "initial GOMAXPROCS < 1")
	ck(runtime.NumCPU() >= 1, "NumCPU < 1")
	ck(runtime.GOMAXPROCS(-1) == old, "GOMAXPROCS(-1) changed the setting")

	var observedBad int32
	readerDone := make(chan struct{})
	go func() {
		defer close(readerDone)
		for i := 0; i < 1<<18; i++ {
			v := runtime.GOMAXPROCS(0)
			if v != 1 && v != 2 && v != 4 && v != old {
				atomic.AddInt32(&observedBad, 1)
			}
			if i&63 == 0 {
				runtime.Gosched()
			}
		}
	}()

	okPrev := true
	prevExpected := old
	for _, p := range []int{1, 2, 4, old} {
		prev := runtime.GOMAXPROCS(p)
		if prev != prevExpected {
			okPrev = false
		}
		if got := runtime.GOMAXPROCS(0); got != p {
			ckf(false, "GOMAXPROCS(0)=%d after set %d", got, p)
		}
		procWorkload(p, 8, 4096, seed)
		prevExpected = p
	}
	runtime.GOMAXPROCS(old)
	<-readerDone

	ck(okPrev, "GOMAXPROCS returned unexpected previous value")
	ck(atomic.LoadInt32(&observedBad) == 0, "concurrent reader observed torn GOMAXPROCS value")
	ck(runtime.GOMAXPROCS(0) == old, "GOMAXPROCS was not restored")
	finish("l04_gomaxprocs_read_set")
}
