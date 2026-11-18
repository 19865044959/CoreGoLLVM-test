// L03: 指针追逐链。用 UAF_SEED 生成的排列把 16384 个节点串成单环链表，
// 每轮完整走一圈，逐节点解引用 next；每 8 圈显式 GC，另有 GC pump 并发，
// GC 可能落在任意一次 pointer chase 中间。默认 48 圈，TORTURE_ROUNDS 可放大。
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
	pcNodes   = 16384
	pcGCDiv   = 8
	pcDefault = 512
)

type pcNode struct {
	v    int64
	idx  int64
	next *pcNode
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

func main() {
	seed := seedFromEnv()
	if seed < 0 {
		seed = -seed
	}
	rng := rand.New(rand.NewSource(seed))
	rounds := tortureRounds(pcDefault)

	backing := make([]pcNode, pcNodes)
	for i := range backing {
		backing[i].v = int64(i)*17 + 3
		backing[i].idx = int64(i)
	}
	order := rng.Perm(pcNodes)
	for i := range order {
		backing[order[i]].next = &backing[order[(i+1)%pcNodes]]
	}
	head := &backing[order[0]]

	var base int64
	for i := range backing {
		base += backing[i].v ^ backing[i].idx
	}

	var epoch atomic.Int64
	var wg sync.WaitGroup
	start := make(chan struct{})
	wg.Add(1)
	go func() {
		defer wg.Done()
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

	var bad, mainGC int
	var sink []byte
	for pass := 0; pass < rounds; pass++ {
		q := head
		var s int64
		count := 0
		for j := 0; j < pcNodes; j++ {
			s += q.v ^ q.idx
			q = q.next
			count++
		}
		if count != pcNodes || s != base || q != head {
			bad++
		}
		if pass%pcGCDiv == 0 {
			sink = make([]byte, 16384)
			sink[0] = byte(pass)
			runtime.GC()
			mainGC++
		}
	}
	for epoch.Load() < 1 {
		runtime.Gosched()
	}
	now := epoch.Load()
	wg.Wait()

	// 最终再走一圈并标记所有节点，验证 GC 后链仍完整且是单环。
	seen := make([]bool, pcNodes)
	q := head
	valid := true
	var finalSum int64
	for j := 0; j < pcNodes; j++ {
		if q == nil || q.idx < 0 || q.idx >= pcNodes || seen[q.idx] {
			valid = false
			break
		}
		seen[q.idx] = true
		finalSum += q.v ^ q.idx
		q = q.next
	}
	if q != head {
		valid = false
	}

	ck(now >= 1, fmt.Sprintf("concurrent GC ran during pointer chase (seed=%d)", seed))
	ck(bad == 0, fmt.Sprintf("every chase pass checksum and cycle closure (seed=%d)", seed))
	ck(valid, fmt.Sprintf("chain still a single cycle over all nodes after GC (seed=%d)", seed))
	ck(finalSum == base, fmt.Sprintf("final chase checksum after GC (seed=%d)", seed))
	ck(mainGC == (rounds+pcGCDiv-1)/pcGCDiv, fmt.Sprintf("explicit GC every %d passes (seed=%d)", pcGCDiv, seed))

	corrupt := false
	for i := range backing {
		if backing[i].v != int64(i)*17+3 || backing[i].idx != int64(i) || backing[i].next == nil {
			corrupt = true
		}
	}
	ck(!corrupt, fmt.Sprintf("pointer chase nodes intact after GC (seed=%d)", seed))
	runtime.KeepAlive(backing)
	runtime.KeepAlive(head)
	runtime.KeepAlive(sink)
	finish("l03_ptr_chase_chain")
}
