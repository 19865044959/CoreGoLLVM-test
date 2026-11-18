// L03: select 事件循环中的 nil-channel 禁用/恢复。单生产者按阶段向两个
// 无缓冲 channel 交替发送，worker 通过把当前 channel 置 nil 暂停读取，
// 再在另一个 channel 达到阈值后恢复。worker 每收 8 条显式 GC，另有 GC
// pump 并发。判定只依赖计数与和，不依赖 select 的随机选择顺序。
package main

import (
	"fmt"
	"os"
	"runtime"
	"strconv"
	"sync"
	"sync/atomic"
)

var failed int32

const (
	snCount = 32
	snHalf  = snCount / 2
	snGCDiv = 8
)

type snNode struct {
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
	aOff := int(seed % snCount)
	bOff := int((seed / 7) % snCount)

	aNodes := make([]snNode, snCount)
	bNodes := make([]snNode, snCount)
	var expA, expB int64
	for i := 0; i < snCount; i++ {
		aNodes[i] = snNode{id: int64(i), v: int64(i)*5 + 2, tag: "a"}
		bNodes[i] = snNode{id: int64(i), v: int64(i)*7 + 3, tag: "b"}
		expA += aNodes[i].v
		expB += bNodes[i].v
	}

	aCh := make(chan *snNode)
	bCh := make(chan *snNode)
	phase := make(chan int, 4)
	quit := make(chan struct{})
	workerDone := make(chan struct{})

	var producerWG sync.WaitGroup
	producerWG.Add(1)
	go func() {
		defer producerWG.Done()
		for i := 0; i < snHalf; i++ {
			aCh <- &aNodes[(aOff+i)%snCount]
		}
		<-phase
		for i := 0; i < snHalf; i++ {
			bCh <- &bNodes[(bOff+i)%snCount]
		}
		<-phase
		for i := snHalf; i < snCount; i++ {
			aCh <- &aNodes[(aOff+i)%snCount]
		}
		<-phase
		for i := snHalf; i < snCount; i++ {
			bCh <- &bNodes[(bOff+i)%snCount]
		}
		close(aCh)
		close(bCh)
	}()

	var gotA, gotB int64
	var sum int64
	var workerGC, workerBad int64
	go func() {
		defer close(workerDone)
		cA, cB := aCh, bCh
		for gotA+gotB < 2*snCount {
			select {
			case nd, ok := <-cA:
				if !ok {
					cA = nil
					continue
				}
				if nd == nil || nd.tag != "a" {
					workerBad++
				} else {
					sum += nd.v
				}
				gotA++
				if gotA%snGCDiv == 0 {
					junk := make([]byte, 8192)
					junk[0] = byte(gotA)
					runtime.GC()
					workerGC++
					runtime.KeepAlive(junk)
				}
				if gotA == snHalf {
					cA = nil
					cB = bCh
					phase <- 1
				} else if gotA == snCount {
					cA = nil
					cB = bCh
					phase <- 3
				}
			case nd, ok := <-cB:
				if !ok {
					cB = nil
					continue
				}
				if nd == nil || nd.tag != "b" {
					workerBad++
				} else {
					sum += nd.v
				}
				gotB++
				if gotB%snGCDiv == 0 {
					junk := make([]byte, 8192)
					junk[0] = byte(gotB)
					runtime.GC()
					workerGC++
					runtime.KeepAlive(junk)
				}
				if gotB == snHalf {
					cA = aCh
					cB = nil
					phase <- 2
				}
			case <-quit:
				return
			}
		}
	}()

	var epoch atomic.Int64
	var pumpWG sync.WaitGroup
	start := make(chan struct{})
	pumpWG.Add(1)
	go func() {
		defer pumpWG.Done()
		<-start
		for r := 0; r < 8; r++ {
			junk := make([][]byte, 8)
			for i := range junk {
				junk[i] = make([]byte, 8192)
				junk[i][0] = byte(r + i)
			}
			runtime.GC()
			epoch.Add(1)
			runtime.KeepAlive(junk)
		}
	}()
	close(start)

	<-workerDone
	producerWG.Wait()
	for epoch.Load() < 1 {
		runtime.Gosched()
	}
	now := epoch.Load()
	pumpWG.Wait()
	close(quit)

	ck(now >= 1, fmt.Sprintf("GC pump completed during select loop (seed=%d)", seed))
	ck(gotA == snCount && gotB == snCount, fmt.Sprintf("select loop received all values from both channels (seed=%d)", seed))
	ck(workerBad == 0, fmt.Sprintf("nil-disabled select delivered only intact nodes (seed=%d)", seed))
	ck(sum == expA+expB, fmt.Sprintf("select loop checksum (seed=%d)", seed))
	ck(workerGC == 8, fmt.Sprintf("worker explicit GC inside select loop (seed=%d)", seed))
	aOK, bOK := true, true
	for i := range aNodes {
		if aNodes[i].v != int64(i)*5+2 || aNodes[i].tag != "a" || aNodes[i].id != int64(i) {
			aOK = false
		}
		if bNodes[i].v != int64(i)*7+3 || bNodes[i].tag != "b" || bNodes[i].id != int64(i) {
			bOK = false
		}
	}
	ck(aOK && bOK, fmt.Sprintf("select-loop nodes intact after GC (seed=%d)", seed))
	runtime.KeepAlive(aNodes)
	runtime.KeepAlive(bNodes)
	finish("l03_select_nil_disable")
}
