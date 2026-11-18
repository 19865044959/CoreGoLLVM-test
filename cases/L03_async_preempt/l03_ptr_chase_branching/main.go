// L03: 分支指针链追逐。96 条链、每条 64 个节点，每个节点有 next[0]/next[1]
// 两条出边（跳 1 或跳 2）；每轮按节点值和轮次/种子决定的 bit 选择下一跳，
// 逐节点解引用直到 nil，并与整数模型逐轮比对。每 16 轮在一次追逐的第 16 跳
// 处显式 GC，GC 后继续沿同一指针前进；另有并发 GC pump。
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
	pcFailed int32
)

const (
	pcChains = 96
	pcLen    = 64
	pcRounds = 512
	pcGCDiv  = 16
)

type pcNode struct {
	v    int64
	idx  int32
	tag  string
	next [2]*pcNode
}

type pcModel struct {
	v    int64
	idx  int32
	next [2]int32
}

func ck(ok bool, msg string) {
	if !ok {
		atomic.AddInt32(&pcFailed, 1)
		fmt.Println("FAIL:", msg)
	}
}

func finish(name string) {
	if n := atomic.LoadInt32(&pcFailed); n != 0 {
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

func roundsFromEnv(def int) int {
	s := os.Getenv("TORTURE_ROUNDS")
	if s == "" {
		return def
	}
	v, err := strconv.Atoi(s)
	if err != nil || v < 1 {
		return def
	}
	if v > 1<<16 {
		v = 1 << 16
	}
	return v
}

//go:noinline
func pcWalk(head *pcNode, r, seed int64, gcAt int, gcCount *int64) (cnt, sum int64, ok bool) {
	ok = true
	n := head
	hops := 0
	for n != nil {
		if hops == gcAt {
			wantV := n.v
			junk := make([]byte, 8192)
			junk[0] = byte(r)
			runtime.GC()
			*gcCount++
			if n.v != wantV || n.tag != "pc" || n.idx < 0 || int(n.idx) >= pcLen {
				ok = false
			}
			runtime.KeepAlive(junk)
		}
		sum += n.v ^ int64(n.idx)
		cnt++
		bit := (n.v + r*7 + seed + int64(n.idx)) & 1
		n = n.next[bit]
		hops++
		if hops > 2*pcLen+4 {
			return cnt, sum, false
		}
	}
	return cnt, sum, ok
}

func pcModelWalk(chain []pcModel, r, seed int64) (cnt, sum int64) {
	i := int32(0)
	for i >= 0 {
		m := chain[i]
		sum += m.v ^ int64(m.idx)
		cnt++
		bit := (m.v + r*7 + seed + int64(m.idx)) & 1
		i = m.next[bit]
	}
	return cnt, sum
}

func main() {
	seed := seedFromEnv()
	if seed < 0 {
		seed = -seed
	}
	rng := rand.New(rand.NewSource(seed))
	step := int64(2*rng.Intn(31) + 1)
	off := int64(rng.Intn(pcChains))
	rounds := roundsFromEnv(pcRounds)

	chains := make([][]pcNode, pcChains)
	models := make([][]pcModel, pcChains)
	for c := 0; c < pcChains; c++ {
		chains[c] = make([]pcNode, pcLen)
		models[c] = make([]pcModel, pcLen)
		for i := 0; i < pcLen; i++ {
			v := int64(c*1000 + i*3 + 1)
			chains[c][i] = pcNode{v: v, idx: int32(i), tag: "pc"}
			models[c][i] = pcModel{v: v, idx: int32(i), next: [2]int32{-1, -1}}
		}
		for i := 0; i < pcLen; i++ {
			if i+1 < pcLen {
				chains[c][i].next[0] = &chains[c][i+1]
				models[c][i].next[0] = int32(i + 1)
			}
			if i+2 < pcLen {
				chains[c][i].next[1] = &chains[c][i+2]
				models[c][i].next[1] = int32(i + 2)
			}
		}
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

	var badWalk, badModel, gcCount int64
	for r := 0; r < rounds; r++ {
		c := int((int64(r)*step + off) % pcChains)
		gcAt := -1
		if r%pcGCDiv == 0 {
			gcAt = 16
		}
		cnt, sum, ok := pcWalk(&chains[c][0], int64(r), seed, gcAt, &gcCount)
		if !ok {
			badWalk++
		}
		wcnt, wsum := pcModelWalk(models[c], int64(r), seed)
		if cnt != wcnt || sum != wsum {
			badModel++
		}
	}
	for epoch.Load() < 1 {
		runtime.Gosched()
	}
	now := epoch.Load()
	pumpWG.Wait()

	var badNode int64
	for c := 0; c < pcChains; c++ {
		for i := 0; i < pcLen; i++ {
			n := &chains[c][i]
			if n.v != int64(c*1000+i*3+1) || n.idx != int32(i) || n.tag != "pc" {
				badNode++
			}
		}
	}

	ck(now >= 1, fmt.Sprintf("GC pump completed during pointer chase (seed=%d)", seed))
	ck(gcCount == int64((rounds+pcGCDiv-1)/pcGCDiv),
		fmt.Sprintf("mid-chase explicit GC count (seed=%d)", seed))
	ck(badWalk == 0, fmt.Sprintf("mid-chase pointer survives GC (seed=%d)", seed))
	ck(badModel == 0, fmt.Sprintf("branchy pointer chase matches model (seed=%d)", seed))
	ck(badNode == 0, fmt.Sprintf("chain nodes intact after loop (seed=%d)", seed))
	runtime.KeepAlive(chains)
	runtime.KeepAlive(models)
	finish("l03_ptr_chase_branching")
}
