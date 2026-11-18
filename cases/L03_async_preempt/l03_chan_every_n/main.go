// L03: 长循环每 64 次迭代与 worker 做一次 chan 请求/应答，消息携带堆指针；
// worker 每 8 次应答前分配垃圾并显式 GC，主循环另有 GC pump 并发。
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
	failed int32
	gcKeep [][]byte
	chKeep [][]byte
)

const (
	cSlots = 64
	cIters = 1 << 14
	cEvery = 64
	cGCDiv = 8
)

type cNode struct {
	v    int64
	next *cNode
}

type cMsg struct {
	seq int64
	p   *cNode
}

func ck(ok bool, msg string) {
	if !ok {
		atomic.AddInt32(&failed, 1)
		fmt.Println("FAIL:", msg)
	}
}

func finish(name string) {
	if n := atomic.LoadInt32(&failed); n != 0 {
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

func cIdxAt(i, step, off int64) int {
	return int((i*step + off) % cSlots)
}

func cAnswer(seq int64, idx int) int64 {
	v := int64(idx)*2 + 7
	nextV := int64((idx+1)%cSlots)*2 + 7
	return v + seq + nextV
}

func main() {
	seed := seedFromEnv()
	if seed < 0 {
		seed = -seed
	}
	rng := rand.New(rand.NewSource(seed))
	step := int64(2*rng.Intn(16) + 1)
	off := int64(rng.Intn(cSlots))

	backing := make([]cNode, cSlots)
	for i := range backing {
		backing[i].v = int64(i)*2 + 7
	}
	for i := range backing {
		backing[i].next = &backing[(i+1)%cSlots]
	}

	req := make(chan *cMsg, 1)
	rep := make(chan int64)
	workerDone := make(chan struct{})
	var workerN int64

	go func() {
		defer close(workerDone)
		n := 0
		for m := range req {
			var ans int64
			if m.p != nil && m.p.next != nil {
				ans = m.p.v + m.seq + m.p.next.v
			} else {
				ans = -1 << 40
			}
			n++
			if n%cGCDiv == 0 {
				junk := make([][]byte, 4)
				for i := range junk {
					junk[i] = make([]byte, 16384)
					junk[i][0] = byte(n + i)
				}
				chKeep = junk
				runtime.GC()
			}
			rep <- ans
		}
		workerN = int64(n)
	}()

	var epoch atomic.Int64
	var wg sync.WaitGroup
	start := make(chan struct{})
	wg.Add(1)
	go func() {
		defer wg.Done()
		<-start
		for r := 0; r < 8; r++ {
			junk := make([][]byte, 8)
			for i := range junk {
				junk[i] = make([]byte, 8192)
				junk[i][0] = byte(r + i)
			}
			gcKeep = junk
			runtime.GC()
			epoch.Add(1)
		}
		gcKeep = nil
	}()
	close(start)

	var sum int64
	var badExchange int32
	var exchanges int64
	var done int64
	for (epoch.Load() < 1 || done < cIters) && done < cIters*4 {
		idx := cIdxAt(done, step, off)
		p := &backing[idx]
		if done < cIters && done%cEvery == 0 {
			req <- &cMsg{seq: done, p: p}
			got := <-rep
			if got != cAnswer(done, idx) {
				atomic.AddInt32(&badExchange, 1)
			}
			exchanges++
		}
		// 跨 channel 同步点后继续使用同一指针。
		sum += p.v
		done++
	}
	close(req)
	<-workerDone
	now := epoch.Load()
	wg.Wait()

	var expected int64
	for i := int64(0); i < done; i++ {
		expected += int64(cIdxAt(i, step, off))*2 + 7
	}

	ck(now >= 1, fmt.Sprintf("GC pump completed during chan loop (seed=%d)", seed))
	ck(atomic.LoadInt32(&badExchange) == 0, fmt.Sprintf("chan request/reply payload (seed=%d)", seed))
	ck(exchanges == cIters/cEvery, fmt.Sprintf("chan exchange count (seed=%d)", seed))
	ck(workerN == exchanges, fmt.Sprintf("worker processed all messages (seed=%d)", seed))
	ck(sum == expected, fmt.Sprintf("chan-loop pointer checksum (seed=%d)", seed))

	corrupt := false
	for i := range backing {
		if backing[i].v != int64(i)*2+7 || backing[i].next != &backing[(i+1)%cSlots] {
			corrupt = true
		}
	}
	ck(!corrupt, fmt.Sprintf("chan-loop nodes intact after GC (seed=%d)", seed))
	runtime.KeepAlive(backing)
	runtime.KeepAlive(chKeep)
	finish("l03_chan_every_n")
}
