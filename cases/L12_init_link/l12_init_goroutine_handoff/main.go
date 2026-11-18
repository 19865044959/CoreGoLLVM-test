// L12: init 中启动 goroutine 并通过 channel 与 main 交接。init 启动的
// worker 在 startGate 上阻塞，main 在全部初始化完成后 close(startGate)，
// 以此建立 init 写入 -> worker 读取的 happens-before；随后并发读全局
// 指针图并穿插显式 GC。
package main

import (
	"fmt"
	"os"
	"runtime"
	"strconv"
	"sync"
	"sync/atomic"
)

const wantGraphSum int64 = 13776 // sum(3*i+1, i=0..95)

var (
	failed  int32
	gcSink  [][]byte
	seedVal int64
	seedStr string
)

func ck(ok bool, msg string) {
	if !ok {
		atomic.AddInt32(&failed, 1)
		fmt.Println("FAIL:", msg)
	}
}

func gcStress() {
	for i := 0; i < 16; i++ {
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
		fmt.Printf("FAILED %s (%d failed checks) seed=%s\n", name, n, seedStr)
		os.Exit(1)
	}
	fmt.Println("ALL PASS " + name)
}

func readSeed() int64 {
	s := os.Getenv("UAF_SEED")
	if s == "" {
		return 1
	}
	v, err := strconv.ParseInt(s, 10, 64)
	if err != nil || v < 0 {
		return 1
	}
	return v
}

func chainSum(head *node) int64 {
	sum := int64(0)
	for n := head; n != nil; n = n.next {
		sum += int64(n.score)
	}
	return sum
}

func main() {
	seedVal = readSeed()
	seedStr = strconv.FormatInt(seedVal, 10)

	ck(atomic.LoadInt32(&buildCount) == 1, "package variable graph built exactly once before init")
	ck(len(graph.nodes) == 96, "graph node count")
	ck(len(graph.nodes) > 0 && graph.head == graph.nodes[0], "graph head identity")
	ck(chainSum(graph.head) == wantGraphSum, "graph score sum")

	// Release the init-started goroutines only after all initialization is done.
	close(startGate)
	h := <-handoffCh
	readerSum := <-sumCh
	ready := <-readyCh
	ck(ready == 1, "init-started ready goroutine did not signal")
	ck(h.initDone, "init-started worker observed incomplete init state")
	ck(atomic.LoadInt32(&initSeenByWorkers) == 1, "worker that checks initDone ran more than once")
	ck(h.sum == wantGraphSum, "init worker computed wrong sum")
	ck(readerSum == wantGraphSum, "init reader computed wrong sum")
	ck(len(h.chain) == 32, fmt.Sprintf("chain sample length %d, want 32", len(h.chain)))
	prevID := -1
	for _, n := range h.chain {
		ck(n != nil, "nil node in handoff chain")
		if n == nil {
			break
		}
		ck(n.id%3 == 0 && n.id > prevID, "handoff chain ids not increasing multiples of 3")
		ck(n.id >= 0 && n.id < len(graph.nodes) && graph.nodes[n.id] == n, "handoff chain pointer identity")
		prevID = n.id
	}

	// main-side readers run concurrently with explicit GC while the init
	// reader goroutine is still parked on stopRead.
	var wg sync.WaitGroup
	var bad int32
	workers := 8
	start := int(seedVal % 96)
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < 150; i++ {
				if chainSum(graph.head) != wantGraphSum {
					atomic.AddInt32(&bad, 1)
					return
				}
				idx := (start + w*7 + i) % len(graph.nodes)
				nd := graph.nodes[idx]
				if nd == nil || nd.id != idx || len(nd.payload) != 8 {
					atomic.AddInt32(&bad, 1)
					return
				}
				for j := range nd.payload {
					if nd.payload[j] != byte((idx*5+j)&0xff) {
						atomic.AddInt32(&bad, 1)
						return
					}
				}
			}
		}(w)
	}
	gcRounds := 2 + int(seedVal%3)
	for r := 0; r < gcRounds; r++ {
		for i := 0; i < 16; i++ {
			b := make([]byte, 8192)
			b[i%len(b)] = byte(r)
			gcSink = append(gcSink[:0], b)
		}
		runtime.GC()
	}
	wg.Wait()
	gcSink = gcSink[:0]
	ck(atomic.LoadInt32(&bad) == 0, "concurrent graph read failed under GC")

	close(stopRead)
	initWG.Wait()

	gcStress()
	ck(chainSum(graph.head) == wantGraphSum, "graph sum changed after GC")
	ck(graph.head == graph.nodes[0], "graph head identity changed after GC")
	for _, n := range h.chain {
		if n == nil || n.id < 0 || n.id >= len(graph.nodes) || graph.nodes[n.id] != n {
			ck(false, "handoff chain pointer identity changed after GC")
			break
		}
	}
	if len(h.chain) == 0 {
		ck(false, "handoff chain empty after GC")
	} else {
		for j := range h.chain[0].payload {
			if h.chain[0].payload[j] != byte((h.chain[0].id*5+j)&0xff) {
				ck(false, "handoff payload corrupted after GC")
				break
			}
		}
	}

	finish("l12_init_goroutine_handoff")
}
