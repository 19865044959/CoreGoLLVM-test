// L03: map + slice 混合容器的长循环。map[int64][]*msNode 从 cap=8 开始按
// 种子决定的键序增长到 128 个键；每轮 append 新节点跨 slice 容量边界，
// 周期性压缩切片（换新 backing）与 delete/重插键。每 32 轮显式 GC 并逐键
// 复核全部节点，另有一个 GC pump 并发。
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
	msFailed int32
)

const (
	msKeys   = 128
	msRounds = 2048
	msGCDiv  = 32
)

type msNode struct {
	v   int64
	tag string
}

func ck(ok bool, msg string) {
	if !ok {
		atomic.AddInt32(&msFailed, 1)
		fmt.Println("FAIL:", msg)
	}
}

func finish(name string) {
	if n := atomic.LoadInt32(&msFailed); n != 0 {
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
	step := int64(2*rng.Intn(127) + 1)
	off := int64(rng.Intn(msKeys))

	m := make(map[int64][]*msNode, 8)
	var lens [msKeys]int
	var sums [msKeys]int64
	var gen, bad, gcCount, totalAppend int64
	appendVal := func(key int64) *msNode {
		gen++
		n := &msNode{v: gen*1000 + key, tag: "ms"}
		m[key] = append(m[key], n)
		lens[key]++
		sums[key] += n.v
		totalAppend++
		return n
	}
	checkAll := func() int64 {
		var badLocal int64
		for k := int64(0); k < msKeys; k++ {
			b := m[k]
			if len(b) != lens[k] {
				badLocal++
			}
			var s int64
			for _, n := range b {
				if n == nil || n.tag != "ms" || n.v%1000 != k {
					badLocal++
					continue
				}
				s += n.v
			}
			if s != sums[k] {
				badLocal++
			}
		}
		return badLocal
	}

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
	close(start)

	for r := int64(0); r < msRounds; r++ {
		key := (r*step + off) % msKeys
		n := appendVal(key)
		if n == nil || m[key][len(m[key])-1] != n || n.v%1000 != key {
			bad++
		}
		if r%7 == 0 {
			ckey := (r*5 + off + 1) % msKeys
			b := m[ckey]
			if len(b) >= 4 {
				kept := make([]*msNode, 0, len(b)/2+1)
				var ks int64
				for j := 1; j < len(b); j += 2 {
					e := b[j]
					if e == nil || e.tag != "ms" || e.v%1000 != ckey {
						bad++
						continue
					}
					kept = append(kept, e)
					ks += e.v
				}
				m[ckey] = kept
				lens[ckey] = len(kept)
				sums[ckey] = ks
			}
		}
		if r%5 == 0 {
			dkey := (r*3 + off + 2) % msKeys
			delete(m, dkey)
			lens[dkey] = 0
			sums[dkey] = 0
		}
		if r%msGCDiv == 0 {
			junk := make([]byte, 16384)
			junk[0] = byte(r)
			runtime.GC()
			bad += checkAll()
			gcCount++
			runtime.KeepAlive(junk)
		}
	}
	for epoch.Load() < 1 {
		runtime.Gosched()
	}
	now := epoch.Load()
	pumpWG.Wait()

	bad += checkAll()
	// 顺序无关的 map range 汇总，用于核对键数与节点总数。
	var rangedKeys, rangedNodes int
	var rangedSum int64
	for _, b := range m {
		if len(b) > 0 {
			rangedKeys++
		}
		rangedNodes += len(b)
		for _, n := range b {
			rangedSum += n.v
		}
	}
	var modelNodes int
	var modelSum int64
	for k := 0; k < msKeys; k++ {
		modelNodes += lens[k]
		modelSum += sums[k]
	}

	ck(now >= 1, fmt.Sprintf("GC pump completed during map/slice loop (seed=%d)", seed))
	ck(gcCount == msRounds/msGCDiv, fmt.Sprintf("explicit GC count (seed=%d)", seed))
	ck(bad == 0, fmt.Sprintf("map/slice entries intact across GC (seed=%d)", seed))
	ck(totalAppend == msRounds, fmt.Sprintf("append count (seed=%d)", seed))
	ck(rangedNodes == modelNodes && rangedSum == modelSum && rangedKeys > 0,
		fmt.Sprintf("map range summary matches model (seed=%d)", seed))
	runtime.KeepAlive(m)
	finish("l03_map_slice_append_loop")
}
