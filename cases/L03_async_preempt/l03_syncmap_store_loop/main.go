// L03: sync.Map 更新循环。主 goroutine 每轮对固定键集做 Store/Load/Delete，
// 后台 Range goroutine 持续遍历（遍历顺序不参与判定），循环中周期性显式 GC，
// 另有 GC pump 并发。结束时按键号顺序和 Range 汇总双重校验。
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
	smKeep [][]byte
)

const (
	smKeys          = 512
	smRoundsDefault = 4096
	smOps           = 4
	smGCDiv         = 64
)

type smNode struct {
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

func smVal(idx int, gen int64) int64 { return int64(idx)*1000 + (gen & 1023) }

func main() {
	seed := seedFromEnv()
	if seed < 0 {
		seed = -seed
	}
	rng := rand.New(rand.NewSource(seed))
	step := int64(2*rng.Intn(63) + 1)
	off := int64(rng.Intn(smKeys))
	rounds := int64(tortureRounds(smRoundsDefault))

	keys := make([]string, smKeys)
	for i := range keys {
		keys[i] = fmt.Sprintf("sm%03d", i)
	}
	present := make([]bool, smKeys)
	last := make([]int64, smKeys)
	m := &sync.Map{}
	for i := 0; i < smKeys; i++ {
		present[i] = true
		last[i] = 0
		m.Store(keys[i], &smNode{idx: int64(i), gen: 0, v: smVal(i, 0), tag: "sm"})
	}

	stopRange := make(chan struct{})
	rangeReady := make(chan struct{})
	var rangeWG sync.WaitGroup
	var rangeObserved atomic.Int64
	var rangeBad atomic.Int32
	rangeWG.Add(1)
	go func() {
		defer rangeWG.Done()
		ready := false
		for {
			select {
			case <-stopRange:
				return
			default:
			}
			m.Range(func(k, v any) bool {
				nd, ok := v.(*smNode)
				if !ok || nd == nil || nd.tag != "sm" ||
					nd.idx < 0 || nd.idx >= smKeys ||
					nd.v != smVal(int(nd.idx), nd.gen) {
					rangeBad.Add(1)
					return true
				}
				rangeObserved.Add(1)
				return true
			})
			if !ready {
				ready = true
				close(rangeReady)
			}
			runtime.Gosched()
		}
	}()
	<-rangeReady

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
			smKeep = junk
			runtime.GC()
			epoch.Add(1)
		}
		smKeep = nil
	}()
	close(start)

	var sum, elems int64
	var badOps, gcRuns int64
	for r := int64(0); r < rounds; r++ {
		for j := int64(0); j < smOps; j++ {
			idx := int((r*7 + j*step + off) % smKeys)
			key := keys[idx]
			if (r+j)%5 == 0 {
				m.Delete(key)
				present[idx] = false
				last[idx] = -1
				if _, ok := m.Load(key); ok {
					badOps++
				}
				continue
			}
			nd := &smNode{idx: int64(idx), gen: r, v: smVal(idx, r), tag: "sm"}
			m.Store(key, nd)
			present[idx] = true
			last[idx] = r
			v, ok := m.Load(key)
			if !ok {
				badOps++
				continue
			}
			got, ok := v.(*smNode)
			if !ok || got == nil || got.idx != int64(idx) || got.gen != r ||
				got.v != nd.v || got.tag != "sm" {
				badOps++
			}
			sum += nd.v
			elems++
		}
		if r%smGCDiv == 0 {
			junk := make([]byte, 16384)
			junk[0] = byte(r)
			runtime.GC()
			gcRuns++
			runtime.KeepAlive(junk)
		}
	}
	close(stopRange)
	rangeWG.Wait()
	for epoch.Load() < 1 {
		runtime.Gosched()
	}
	now := epoch.Load()
	pumpWG.Wait()

	var expCnt, expSum int64
	for i := 0; i < smKeys; i++ {
		if present[i] {
			expCnt++
			expSum += smVal(i, last[i])
		}
	}
	var gotCnt, gotSum int64
	var finalBad int64
	m.Range(func(k, v any) bool {
		nd, ok := v.(*smNode)
		if !ok || nd == nil || nd.tag != "sm" ||
			nd.idx < 0 || nd.idx >= smKeys ||
			nd.v != smVal(int(nd.idx), nd.gen) {
			finalBad++
			return true
		}
		gotCnt++
		gotSum += nd.v
		return true
	})

	var keyBad int64
	for i := 0; i < smKeys; i++ {
		v, ok := m.Load(keys[i])
		if ok != present[i] {
			keyBad++
			continue
		}
		if !ok {
			continue
		}
		nd, ok := v.(*smNode)
		if !ok || nd == nil || nd.idx != int64(i) || nd.gen != last[i] ||
			nd.v != smVal(i, last[i]) || nd.tag != "sm" {
			keyBad++
		}
	}

	ck(now >= 1, fmt.Sprintf("GC pump completed during sync.Map loop (seed=%d)", seed))
	ck(badOps == 0, fmt.Sprintf("sync.Map Store/Load/Delete results (seed=%d)", seed))
	ck(keyBad == 0, fmt.Sprintf("per-key final state (seed=%d)", seed))
	ck(finalBad == 0, fmt.Sprintf("Range saw only valid immutable values (seed=%d)", seed))
	ck(gotCnt == expCnt && gotSum == expSum,
		fmt.Sprintf("final Range count/sum (seed=%d)", seed))
	ck(gcRuns == (rounds-1)/smGCDiv+1, fmt.Sprintf("main explicit GC during loop (seed=%d)", seed))
	ck(rangeObserved.Load() > 0, fmt.Sprintf("background Range observed entries (seed=%d)", seed))
	ck(rangeBad.Load() == 0, fmt.Sprintf("background Range value integrity (seed=%d)", seed))
	ck(elems > 0, fmt.Sprintf("store path executed (seed=%d)", seed))
	runtime.KeepAlive(sum)
	runtime.KeepAlive(keys)
	runtime.KeepAlive(smKeep)
	finish("l03_syncmap_store_loop")
}
