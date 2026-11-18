// L02: select 公平性（etcd raft 事件循环 / grpc callback serializer 调度场景）：
// 多路始终就绪时各分支都必须被选中且无饥饿；closed channel 永远压过 default，
// nil channel 永不就绪；固定迭代 + 宽裕统计边界，不依赖墙钟。
package main

import (
	"fmt"
	"os"
	"runtime"
	"strconv"
	"sync"
	"sync/atomic"
)

var (
	failed int32
	gcMu   sync.Mutex
	gcSink [][]byte
)

type blob struct {
	v   int64
	tag string
}

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

func main() {
	seed := seedVal()

	// closed channel 始终就绪，default 不得被选中。
	a := make(chan int)
	b := make(chan int)
	close(a)
	close(b)
	def := 0
	ca, cb := 0, 0
	for i := 0; i < 4000; i++ {
		select {
		case <-a:
			ca++
		case <-b:
			cb++
		default:
			def++
		}
	}
	ck(def == 0, fmt.Sprintf("default selected %d times despite ready closed channels", def))
	ck(ca > 0 && cb > 0, "one closed channel starved")
	if ca+cb != 4000 {
		ck(false, fmt.Sprintf("closed selections=%d want=4000", ca+cb))
	}

	// nil channel 永不就绪。
	var nilCh chan int
	nilSel := 0
	for i := 0; i < 2000; i++ {
		select {
		case <-nilCh:
			nilSel++
		default:
		}
	}
	ck(nilSel == 0, "nil channel case must never be ready")

	// 4 路始终就绪：每个分支都必须被选中，份额落在宽裕区间。
	const iters = 20000
	cs := []chan struct{}{
		make(chan struct{}), make(chan struct{}),
		make(chan struct{}), make(chan struct{}),
	}
	for _, c := range cs {
		close(c)
	}
	counts := make([]int, 4)
	payload := &blob{v: seed + 3, tag: "select"}
	for i := 0; i < iters; i++ {
		select {
		case <-cs[0]:
			counts[0]++
		case <-cs[1]:
			counts[1]++
		case <-cs[2]:
			counts[2]++
		case <-cs[3]:
			counts[3]++
		}
		if i%4096 == 0 {
			gcStress()
			if payload.v != seed+3 || payload.tag != "select" {
				ck(false, "payload corrupted by GC")
			}
		}
	}
	for i, n := range counts {
		ck(n*100 >= iters*15 && n*100 <= iters*35,
			fmt.Sprintf("case %d share out of fair window: %d/%d", i, n, iters))
	}

	// 2 路公平性 + 无饥饿：任意一次连续同分支长度必须有界。
	d1 := make(chan struct{})
	d2 := make(chan struct{})
	close(d1)
	close(d2)
	lastBranch := -1
	lastAt := -1
	maxGap := 0
	n1, n2 := 0, 0
	for i := 0; i < 12000; i++ {
		branch := -1
		select {
		case <-d1:
			branch = 0
			n1++
		case <-d2:
			branch = 1
			n2++
		}
		if lastBranch == branch {
			if gap := i - lastAt; gap > maxGap {
				maxGap = gap
			}
		} else {
			lastBranch = branch
			lastAt = i
		}
		if i%4096 == 0 {
			gcStress()
		}
	}
	ck(n1 > 0 && n2 > 0, "two-way select starved a branch")
	ck(maxGap < 128, fmt.Sprintf("select starvation gap=%d want<128", maxGap))

	// 读写同时就绪时，select 也必须覆盖两路。
	ready := make(chan int, 1)
	ready <- seedPayload()
	sink := make(chan int, 1)
	sendHits, recvHits := 0, 0
	for i := 0; i < 4000; i++ {
		select {
		case <-ready:
			recvHits++
			ready <- seedPayload()
		case sink <- i:
			sendHits++
		}
	}
	ck(sendHits > 0 && recvHits > 0, "select did not cover both send and recv cases")

	fmt.Printf("fair_iters=%d cases=4 window=15..35 gap_bound=128\n", iters)
	finish("l02_select_fairness")
}

func seedPayload() int { return 1 }
