// L03: 非阻塞 channel 轮询循环。消费者用 select+default 轮询无缓冲队列，
// default 分支持有最近一次收到的节点指针做短紧循环；生产者等待 gate 后逐条发送。
// 每收到 512 条消息显式 GC，另有 GC pump 并发；校验消息完整性与指针目标。
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

var failed int32

const (
	cpCount = 4096
	cpGCDiv = 512
)

type cpNode struct {
	id  int64
	v   int64
	tag string
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

func main() {
	seed := seedFromEnv()
	if seed < 0 {
		seed = -seed
	}

	rng := rand.New(rand.NewSource(seed))
	order := rng.Perm(cpCount)

	backing := make([]cpNode, cpCount)
	for i := range backing {
		backing[i].id = int64(i)
		backing[i].v = int64(i)*3 + 11
		backing[i].tag = "cp"
	}

	ch := make(chan *cpNode)
	var gate atomic.Bool
	var produced int64
	var producerWG sync.WaitGroup
	producerWG.Add(1)
	go func() {
		defer producerWG.Done()
		for !gate.Load() {
			runtime.Gosched()
		}
		for _, i := range order {
			ch <- &backing[i]
			produced++
		}
		close(ch)
	}()

	var epoch atomic.Int64
	var pumpWG sync.WaitGroup
	start := make(chan struct{})
	pumpWG.Add(1)
	go func() {
		defer pumpWG.Done()
		<-start
		for r := 0; r < 8; r++ {
			junk := make([]byte, 16384)
			for i := 0; i < len(junk); i += 512 {
				junk[i] = byte(r + i)
			}
			runtime.GC()
			epoch.Add(1)
			runtime.KeepAlive(junk)
		}
	}()
	close(start)

	seen := make([]bool, cpCount)
	var last *cpNode
	var sum, spin int64
	got, dups, polls, mainGC := 0, 0, 0, 0
	var sink []byte

poll:
	for {
		select {
		case q, ok := <-ch:
			if !ok {
				break poll
			}
			last = q
			if seen[q.id] {
				dups++
			} else {
				seen[q.id] = true
				sum += q.v
				got++
			}
			if got%cpGCDiv == 0 {
				sink = make([]byte, 8192)
				sink[0] = byte(got)
				runtime.GC()
				mainGC++
			}
		default:
			polls++
			if last != nil {
				p := last
				for k := 0; k < 8; k++ {
					spin += p.v
				}
			}
			if polls%64 == 0 {
				runtime.Gosched()
			}
			if polls >= 128 {
				gate.Store(true)
			}
		}
	}
	for epoch.Load() < 1 {
		runtime.Gosched()
	}
	now := epoch.Load()
	producerWG.Wait()
	pumpWG.Wait()

	var base int64
	allSeen := true
	for i := range backing {
		base += backing[i].v
		if !seen[i] {
			allSeen = false
		}
		if backing[i].v != int64(i)*3+11 || backing[i].tag != "cp" {
			allSeen = false
		}
	}

	ck(got == cpCount, fmt.Sprintf("all messages received (seed=%d)", seed))
	ck(dups == 0, fmt.Sprintf("no duplicate messages (seed=%d)", seed))
	ck(allSeen, fmt.Sprintf("every message id seen with intact payload (seed=%d)", seed))
	ck(sum == base, fmt.Sprintf("received checksum (seed=%d)", seed))
	ck(polls >= 128, fmt.Sprintf("non-blocking default branch exercised (seed=%d)", seed))
	ck(spin > 0, fmt.Sprintf("default branch held last pointer across GC (seed=%d)", seed))
	ck(mainGC == cpCount/cpGCDiv, fmt.Sprintf("explicit GC every %d messages (seed=%d)", cpGCDiv, seed))
	ck(now >= 1, fmt.Sprintf("concurrent GC pump ran (seed=%d)", seed))
	ck(produced == cpCount, fmt.Sprintf("producer sent every message (seed=%d)", seed))
	runtime.KeepAlive(backing)
	runtime.KeepAlive(last)
	runtime.KeepAlive(sink)
	finish("l03_chan_poll_loop")
}
