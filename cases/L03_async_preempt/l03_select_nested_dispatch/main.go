// L03: 三层嵌套 select 的事件分发循环。外层 select 在 jobCh/quit 之间选择；
// 收到任务后，内层 select 非阻塞消费 urgentCh；进入 urgent 分支后再用第三层
// select 在 laneA/laneB 之间选择。每轮由生产者预先填充缓冲 channel，保证三层
// 分支选择确定；worker 每 64 个任务在持三枚节点指针时显式 GC。
package main

import (
	"fmt"
	"math/rand"
	"os"
	"runtime"
	"strconv"
	"sync"
	"sync/atomic"
)

var (
	sdFailed int32
)

const (
	sdRounds = 512
	sdGCDiv  = 64
)

type sdNode struct {
	v   int64
	tag string
}

type sdAck struct {
	val  int64
	lane string
}

type sdCounters struct {
	jobs     int64
	urgents  int64
	defaults int64
	lanes    int64
	laneA    int64
	laneB    int64
	gc       int64
	bad      int64
	sum      int64
	done     chan struct{}
}

func ck(ok bool, msg string) {
	if !ok {
		atomic.AddInt32(&sdFailed, 1)
		fmt.Println("FAIL:", msg)
	}
}

func finish(name string) {
	if n := atomic.LoadInt32(&sdFailed); n != 0 {
		fmt.Printf("FAILED %s (%d failed checks)\n", name, n)
		os.Exit(1)
	}
	fmt.Println("ALL PASS " + name)
}

func seedFromEnv() int64 {
	if s := os.Getenv("UAF_SEED"); s != "" {
		if v, err := strconv.ParseInt(s, 10, 64); err == nil {
			return v
		}
	}
	return 1
}

//go:noinline
func sdWorker(jobCh <-chan *sdNode, urgentCh <-chan *sdNode, laneA, laneB <-chan *sdNode,
	ack chan<- sdAck, quit <-chan struct{}, c *sdCounters) {
	defer close(c.done)
	for {
		select {
		case n, ok := <-jobCh:
			if !ok {
				return
			}
			c.jobs++
			var u, lane *sdNode
			laneName := ""
			select {
			case uu := <-urgentCh:
				u = uu
				c.urgents++
				select {
				case a := <-laneA:
					lane = a
					laneName = "A"
					c.laneA++
				case b := <-laneB:
					lane = b
					laneName = "B"
					c.laneB++
				}
				c.lanes++
			default:
				c.defaults++
			}
			if c.jobs%sdGCDiv == 1 {
				junk := make([]byte, 16384)
				junk[0] = byte(c.jobs)
				runtime.GC()
				c.gc++
				// GC 后复核仍持有的任务/紧急/车道节点。
				if n.tag != "sd" {
					c.bad++
				}
				if u != nil && u.tag != "urg" {
					c.bad++
				}
				if lane != nil && lane.tag != "lane" {
					c.bad++
				}
				runtime.KeepAlive(junk)
			}
			val := n.v
			if u != nil {
				val += u.v * 2
			}
			if lane != nil {
				val += lane.v * 3
			}
			c.sum += val
			ack <- sdAck{val: val, lane: laneName}
		case <-quit:
			return
		}
	}
}

func main() {
	seed := seedFromEnv()
	if seed < 0 {
		seed = -seed
	}
	rng := rand.New(rand.NewSource(seed))
	step := int64(2*rng.Intn(31) + 1)
	off := int64(rng.Intn(5))

	jobCh := make(chan *sdNode)
	urgentCh := make(chan *sdNode, 1)
	laneA := make(chan *sdNode, 1)
	laneB := make(chan *sdNode, 1)
	ack := make(chan sdAck)
	quit := make(chan struct{})
	counters := &sdCounters{done: make(chan struct{})}

	start := make(chan struct{})
	var epoch atomic.Int64
	var pumpWG sync.WaitGroup
	pumpWG.Add(1)
	go func() {
		defer pumpWG.Done()
		<-start
		for r := 0; r < 8; r++ {
			junk := make([][]byte, 8)
			for j := range junk {
				junk[j] = make([]byte, 8192)
				junk[j][0] = byte(r + j)
			}
			runtime.GC()
			runtime.KeepAlive(junk)
			epoch.Add(1)
		}
	}()
	workerDone := make(chan struct{})
	go func() {
		sdWorker(jobCh, urgentCh, laneA, laneB, ack, quit, counters)
		close(workerDone)
	}()
	close(start)

	var bad, expSum int64
	var expJobs, expUrgents, expDefaults, expLanes, expA, expB int64
	for r := int64(0); r < sdRounds; r++ {
		n := &sdNode{v: r*7 + 1, tag: "sd"}
		var u, lane *sdNode
		laneName := ""
		// 由种子决定哪些轮次跳过 urgent（走 default 分支）。
		if (r+off)%5 != 0 {
			u = &sdNode{v: r*11 + 3, tag: "urg"}
			lane = &sdNode{v: r*13 + 5, tag: "lane"}
			urgentCh <- u
			if (r/5+step)%2 == 0 {
				laneA <- lane
				laneName = "A"
				expA++
			} else {
				laneB <- lane
				laneName = "B"
				expB++
			}
			expLanes++
			expUrgents++
		} else {
			expDefaults++
		}
		jobCh <- n
		a := <-ack
		exp := n.v
		if u != nil {
			exp += u.v * 2
		}
		if lane != nil {
			exp += lane.v * 3
		}
		expSum += exp
		expJobs++
		if a.val != exp || a.lane != laneName {
			bad++
		}
	}
	close(jobCh)
	close(quit)
	<-workerDone
	for epoch.Load() < 1 {
		runtime.Gosched()
	}
	now := epoch.Load()
	pumpWG.Wait()

	ck(now >= 1, fmt.Sprintf("GC pump completed during nested select loop (seed=%d)", seed))
	ck(bad == 0, fmt.Sprintf("nested select ack values/lane (seed=%d)", seed))
	ck(counters.bad == 0, fmt.Sprintf("nodes held across worker GC (seed=%d)", seed))
	ck(counters.jobs == expJobs && expJobs == sdRounds,
		fmt.Sprintf("job count (seed=%d)", seed))
	ck(counters.urgents == expUrgents && counters.defaults == expDefaults,
		fmt.Sprintf("inner select urgent/default split (seed=%d)", seed))
	ck(counters.lanes == expLanes && counters.laneA == expA && counters.laneB == expB,
		fmt.Sprintf("third-level lane split (seed=%d)", seed))
	ck(counters.sum == expSum, fmt.Sprintf("nested select checksum (seed=%d)", seed))
	ck(counters.gc == sdRounds/sdGCDiv, fmt.Sprintf("worker explicit GC count (seed=%d)", seed))
	finish("l03_select_nested_dispatch")
}
