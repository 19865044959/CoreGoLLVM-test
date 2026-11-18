// L03: 两级 goroutine 嵌套（外层 worker 再起内层 worker），每层都是持针紧循环；
// 所有 worker 跑到至少一轮后台 GC 完成后收尾，各自校验和确定。
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
	gcKeep [][]byte
	ngOut  [ngFanout]int64
	ngCnt  [ngFanout][ngFanout]int64
)

const (
	ngSlots    = 256
	ngFanout   = 4
	ngDefIters = 1 << 20
)

type nNode struct {
	v   int64
	tag uint64
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

func tortureIters(def int) int {
	if s := os.Getenv("TORTURE_ROUNDS"); s != "" {
		if v, err := strconv.Atoi(s); err == nil && v > 0 {
			return v
		}
	}
	return def
}

func ngIdx(i, step, off int64) int {
	return int((i*step + off) & (ngSlots - 1))
}

func ngExpected(count, step, off int64) int64 {
	var s int64
	for i := int64(0); i < count; i++ {
		s += int64(ngIdx(i, step, off))*5 + 11
	}
	return s
}

func main() {
	seed := seedFromEnv()
	if seed < 0 {
		seed = -seed
	}
	iters := int64(tortureIters(ngDefIters))

	var entered atomic.Int64
	var missedGC int32

	backing := make([]nNode, ngSlots)
	for i := range backing {
		backing[i].v = int64(i)*5 + 11
		backing[i].tag = uint64(i) | 1<<40
	}

	var epoch atomic.Int64
	var pump sync.WaitGroup
	start := make(chan struct{})
	pump.Add(1)
	go func() {
		defer pump.Done()
		<-start
		for entered.Load() < int64(ngFanout*ngFanout) {
			runtime.Gosched()
		}
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

	var outer sync.WaitGroup
	outer.Add(ngFanout)
	for w := 0; w < ngFanout; w++ {
		w := w
		go func() {
			defer outer.Done()
			inner := make(chan int64, ngFanout)
			var iw sync.WaitGroup
			iw.Add(ngFanout)
			for j := 0; j < ngFanout; j++ {
				j := j
				go func() {
					defer iw.Done()
					step := int64(2*j + 1)
					off := int64((w*ngFanout + j + int(seed%int64(ngSlots))) % ngSlots)
					var sum int64
					entered.Add(1)
					for i := int64(0); i < iters; i++ {
						sum += backing[ngIdx(i, step, off)].v
					}
					// 紧循环结束后等待并发 GC 完成；Gosched 让出 P，
					// 避免忙等挤占 pump 的调度。
					spins := 0
					for epoch.Load() < 1 && spins < 1<<26 {
						runtime.Gosched()
						spins++
					}
					if epoch.Load() < 1 {
						atomic.AddInt32(&missedGC, 1)
					}
					ngCnt[w][j] = iters
					inner <- sum
				}()
			}
			iw.Wait()
			close(inner)
			var s int64
			for v := range inner {
				s += v
			}
			ngOut[w] = s
		}()
	}
	outer.Wait()
	pump.Wait()
	now := epoch.Load()

	bad := int32(0)
	for w := 0; w < ngFanout; w++ {
		var exp int64
		for j := 0; j < ngFanout; j++ {
			step := int64(2*j + 1)
			off := int64((w*ngFanout + j + int(seed%int64(ngSlots))) % ngSlots)
			exp += ngExpected(ngCnt[w][j], step, off)
		}
		if ngOut[w] != exp {
			atomic.AddInt32(&bad, 1)
		}
		if ngCnt[w][0] < iters || ngCnt[w][ngFanout-1] < iters {
			atomic.AddInt32(&bad, 1)
		}
	}

	ck(now >= 1, fmt.Sprintf("GC pump completed during nested goroutines (seed=%d)", seed))
	ck(atomic.LoadInt32(&missedGC) == 0, fmt.Sprintf("nested workers observed concurrent GC (seed=%d)", seed))
	ck(atomic.LoadInt32(&bad) == 0, fmt.Sprintf("nested worker checksums (seed=%d)", seed))

	corrupt := false
	for i := range backing {
		if backing[i].v != int64(i)*5+11 || backing[i].tag != uint64(i)|1<<40 {
			corrupt = true
		}
	}
	ck(!corrupt, fmt.Sprintf("nested-loop nodes intact after GC (seed=%d)", seed))
	runtime.KeepAlive(backing)
	finish("l03_nested_goroutines")
}
