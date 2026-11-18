// L03: append 增长长循环。每轮从 cap=0 的指针切片开始按不同长度 append，
// 跨越多次容量增长；循环中周期性在切片存活时显式 GC，另有 GC pump 并发。
// 保留最近 8 个切片，结束后逐元素复核指针目标与值。
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
	agKeep [][][]byte
)

const (
	agSlots         = 128
	agRoundsDefault = 4096
	agGCDiv         = 32
	agMaxLen        = 64
)

type agNode struct {
	idx int64
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

func tortureRounds(def int) int {
	if s := os.Getenv("TORTURE_ROUNDS"); s != "" {
		if v, err := strconv.Atoi(s); err == nil && v > 0 {
			return v
		}
	}
	return def
}

func agLen(r int64) int { return 1 + int((r*13)&(agMaxLen-1)) }

func agIdx(r int64, k int, step, off int64) int {
	return int((r*step + int64(k)*7 + off) % agSlots)
}

func main() {
	seed := seedFromEnv()
	if seed < 0 {
		seed = -seed
	}
	rng := rand.New(rand.NewSource(seed))
	step := int64(2*rng.Intn(31) + 1)
	off := int64(rng.Intn(agSlots))
	rounds := int64(tortureRounds(agRoundsDefault))

	backing := make([]agNode, agSlots)
	for i := range backing {
		backing[i].idx = int64(i)
		backing[i].v = int64(i)*11 + 3
		backing[i].tag = "ag"
	}
	keep := make([][]*agNode, 8)
	keepRound := make([]int64, 8)
	keepSet := make([]bool, 8)

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
			agKeep = append(agKeep[:0], junk)
			runtime.GC()
			epoch.Add(1)
		}
		agKeep = agKeep[:0]
	}()
	close(start)

	var sum, elems int64
	var bad, badCap, gcRuns int64
	for r := int64(0); r < rounds; r++ {
		n := agLen(r)
		s := make([]*agNode, 0)
		for k := 0; k < n; k++ {
			idx := agIdx(r, k, step, off)
			s = append(s, &backing[idx])
		}
		if r%agGCDiv == 0 {
			junk := make([]byte, 16384)
			junk[0] = byte(r)
			runtime.GC()
			gcRuns++
			runtime.KeepAlive(junk)
		}
		if len(s) != n || cap(s) < n {
			badCap++
		}
		var local int64
		for k, p := range s {
			if p == nil || p.tag != "ag" || p.idx != int64(agIdx(r, k, step, off)) ||
				p.v != int64(agIdx(r, k, step, off))*11+3 {
				bad++
				continue
			}
			local += p.v + int64(k)
		}
		sum += local
		elems += int64(len(s))
		keep[r&7] = s
		keepRound[r&7] = r
		keepSet[r&7] = true
	}
	for epoch.Load() < 1 {
		runtime.Gosched()
	}
	now := epoch.Load()
	pumpWG.Wait()

	var expected, expElems int64
	for r := int64(0); r < rounds; r++ {
		n := agLen(r)
		expElems += int64(n)
		for k := 0; k < n; k++ {
			idx := agIdx(r, k, step, off)
			expected += int64(idx)*11 + 3 + int64(k)
		}
	}
	var badKeep int64
	for j := 0; j < 8; j++ {
		if !keepSet[j] {
			continue
		}
		r := keepRound[j]
		s := keep[j]
		n := agLen(r)
		if len(s) != n {
			badKeep++
			continue
		}
		for k, p := range s {
			idx := agIdx(r, k, step, off)
			if p == nil || p.idx != int64(idx) || p.v != int64(idx)*11+3 || p.tag != "ag" {
				badKeep++
			}
		}
	}

	ck(now >= 1, fmt.Sprintf("GC pump completed during append loop (seed=%d)", seed))
	ck(bad == 0, fmt.Sprintf("appended pointers/values readable after GC (seed=%d)", seed))
	ck(badCap == 0, fmt.Sprintf("slice length/capacity after growth (seed=%d)", seed))
	ck(sum == expected, fmt.Sprintf("append-growth checksum (seed=%d)", seed))
	ck(elems == expElems, fmt.Sprintf("appended element total (seed=%d)", seed))
	ck(gcRuns == (rounds-1)/agGCDiv+1, fmt.Sprintf("main explicit GC during loop (seed=%d)", seed))
	ck(badKeep == 0, fmt.Sprintf("retained slices intact after final GC (seed=%d)", seed))

	corrupt := false
	for i := range backing {
		if backing[i].idx != int64(i) || backing[i].v != int64(i)*11+3 || backing[i].tag != "ag" {
			corrupt = true
		}
	}
	ck(!corrupt, fmt.Sprintf("append-loop nodes intact after GC (seed=%d)", seed))
	runtime.KeepAlive(backing)
	runtime.KeepAlive(keep)
	runtime.KeepAlive(agKeep)
	finish("l03_append_growth_loop")
}
