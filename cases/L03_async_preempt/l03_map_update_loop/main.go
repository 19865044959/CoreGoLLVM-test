// L03: map 更新长循环。固定键集上交替执行更新、删除、重新插入，map 值
// 指向堆节点；循环中周期性显式 GC，另有 GC pump 并发。结束后按键号顺序
// 与 Range 汇总双重校验最终状态。
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
	muKeep [][]byte
)

const (
	muKeys          = 1024
	muRoundsDefault = 8192
	muGCDiv         = 128
)

type muNode struct {
	idx int64
	gen int64
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

func muVal(idx int, gen int64) int64 { return int64(idx)*1000 + (gen & 1023) }

func main() {
	seed := seedFromEnv()
	if seed < 0 {
		seed = -seed
	}
	rng := rand.New(rand.NewSource(seed))
	step := int64(2*rng.Intn(63) + 1)
	off := int64(rng.Intn(muKeys))
	rounds := int64(tortureRounds(muRoundsDefault))

	m := make(map[int]*muNode, muKeys)
	present := make([]bool, muKeys)
	last := make([]int64, muKeys)
	for i := 0; i < muKeys; i++ {
		present[i] = true
		last[i] = 0
		m[i] = &muNode{idx: int64(i), gen: 0, v: muVal(i, 0), tag: "m"}
	}

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
			muKeep = junk
			runtime.GC()
			epoch.Add(1)
		}
		muKeep = nil
	}()
	close(start)

	var badUpdate, badDelete, badLoad, gcRuns int64
	for r := int64(0); r < rounds; r++ {
		idx := int((r*step + off) % muKeys)
		m[idx] = &muNode{idx: int64(idx), gen: r, v: muVal(idx, r), tag: "m"}
		present[idx] = true
		last[idx] = r
		if p := m[idx]; p == nil || p.gen != r || p.v != muVal(idx, r) {
			badUpdate++
		}

		if r%7 == 0 {
			di := int((r*step + 3333 + off) % muKeys)
			delete(m, di)
			present[di] = false
			last[di] = -1
			if _, ok := m[di]; ok {
				badDelete++
			}
		}
		if r%11 == 0 {
			ii := int((r*step + 7777 + off) % muKeys)
			m[ii] = &muNode{idx: int64(ii), gen: r, v: muVal(ii, r), tag: "m"}
			present[ii] = true
			last[ii] = r
		}
		if r%3 == 0 {
			p, ok := m[idx]
			if present[idx] {
				if !ok || p == nil || p.idx != int64(idx) || p.gen != last[idx] ||
					p.v != muVal(idx, last[idx]) || p.tag != "m" {
					badLoad++
				}
			} else if ok {
				badLoad++
			}
		}
		if r%muGCDiv == 0 {
			junk := make([]byte, 16384)
			junk[0] = byte(r)
			runtime.GC()
			gcRuns++
			runtime.KeepAlive(junk)
		}
	}
	for epoch.Load() < 1 {
		runtime.Gosched()
	}
	now := epoch.Load()
	pumpWG.Wait()

	var expCnt, expSum int64
	for i := 0; i < muKeys; i++ {
		if present[i] {
			expCnt++
			expSum += muVal(i, last[i])
		}
	}
	var keyBad int64
	for i := 0; i < muKeys; i++ {
		p, ok := m[i]
		if ok != present[i] {
			keyBad++
			continue
		}
		if !ok {
			continue
		}
		if p == nil || p.idx != int64(i) || p.gen != last[i] ||
			p.v != muVal(i, last[i]) || p.tag != "m" {
			keyBad++
		}
	}
	var gotCnt, gotSum int64
	var rangeBad int64
	for k, v := range m {
		if v == nil || v.idx != int64(k) || v.v != muVal(k, v.gen) || v.tag != "m" {
			rangeBad++
			continue
		}
		gotCnt++
		gotSum += v.v
	}

	ck(now >= 1, fmt.Sprintf("GC pump completed during map update loop (seed=%d)", seed))
	ck(badUpdate == 0, fmt.Sprintf("map update writes visible (seed=%d)", seed))
	ck(badDelete == 0, fmt.Sprintf("map deletes remove keys (seed=%d)", seed))
	ck(badLoad == 0, fmt.Sprintf("map loads after update/delete (seed=%d)", seed))
	ck(keyBad == 0, fmt.Sprintf("per-key final state (seed=%d)", seed))
	ck(rangeBad == 0, fmt.Sprintf("Range values intact after GC (seed=%d)", seed))
	ck(gotCnt == expCnt && gotSum == expSum && int64(len(m)) == expCnt,
		fmt.Sprintf("final map count/sum (seed=%d)", seed))
	ck(gcRuns == (rounds-1)/muGCDiv+1, fmt.Sprintf("main explicit GC during loop (seed=%d)", seed))
	runtime.KeepAlive(m)
	runtime.KeepAlive(muKeep)
	finish("l03_map_update_loop")
}
