// L03: 长循环逐轮构造 map/slice/链式指针，GC 穿插；默认规模快，TORTURE_ROUNDS 可放大。
// 插入顺序由 UAF_SEED 决定，校验只依赖集合内容。
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
	gcKeep [][]byte
)

const perRound = 96

type node struct {
	v    int64
	next *node
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
	rounds := tortureRounds(16)
	rng := rand.New(rand.NewSource(seed))

	var epoch atomic.Int64
	var wg sync.WaitGroup
	start := make(chan struct{})
	wg.Add(1)
	go func() {
		defer wg.Done()
		<-start
		for r := 0; r < 6; r++ {
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

	var total int64
	var lastMap map[int64]*node
	var lastSlice []*node
	var lastHead *node
	for round := 0; round < rounds; round++ {
		m := make(map[int64]*node, perRound)
		s := make([]*node, 0, perRound)
		order := rng.Perm(perRound)
		var head *node
		for _, i := range order {
			nd := &node{v: int64(round*1000 + i)}
			nd.next = head
			head = nd
			m[nd.v] = nd
			s = append(s, nd)
			total += nd.v
		}
		lastMap, lastSlice, lastHead = m, s, head
		if round%4 == 3 {
			runtime.GC()
		}
	}
	now := epoch.Load()
	wg.Wait()

	ck(now >= 1, fmt.Sprintf("GC completed during build loop (seed=%d)", seed))

	var expected int64
	for round := 0; round < rounds; round++ {
		for i := 0; i < perRound; i++ {
			expected += int64(round*1000 + i)
		}
	}
	ck(total == expected, fmt.Sprintf("build-loop checksum (seed=%d)", seed))

	corrupt := false
	if len(lastMap) != perRound || len(lastSlice) != perRound {
		corrupt = true
	}
	for i := 0; i < perRound; i++ {
		v := int64((rounds-1)*1000 + i)
		nd, ok := lastMap[v]
		if !ok || nd.v != v {
			corrupt = true
		}
	}
	var seen [perRound]bool
	count := 0
	for q := lastHead; q != nil; q = q.next {
		idx := int(q.v % 1000)
		if idx < 0 || idx >= perRound || seen[idx] {
			corrupt = true
			break
		}
		seen[idx] = true
		count++
	}
	ck(!corrupt, fmt.Sprintf("map/slice/chain intact after GC (seed=%d)", seed))
	ck(count == perRound, fmt.Sprintf("chain length (seed=%d)", seed))
	runtime.KeepAlive(lastMap)
	runtime.KeepAlive(lastSlice)
	runtime.KeepAlive(lastHead)
	finish("l03_build_loop_gc")
}
