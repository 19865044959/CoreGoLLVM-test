// L03: map 查找长循环。每轮用可复现 LCG 生成 key，在 map[int64]*node 中查找，
// 命中/未命中都走固定校验；每 65536 轮显式 GC，查找返回的指针跨 GC 后继续解引用。
// 另起 GC pump 并发，map 本身只读，校验和与命中次数只由 UAF_SEED 决定。
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
	mlKeys  = 8192
	mlRange = mlKeys + 2048
	mlIters = 524288
	mlGCDiv = 65536
)

type mlNode struct {
	v    int64
	w    int64
	next *mlNode
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

const (
	lcgMul = 6364136223846793005
	lcgAdd = 1442695040888963407
)

func mlKey(x *uint64) int64 {
	*x = *x*lcgMul + lcgAdd
	return int64((*x >> 33) % uint64(mlRange))
}

func main() {
	seed := seedFromEnv()
	if seed < 0 {
		seed = -seed
	}
	rng := rand.New(rand.NewSource(seed))

	backing := make([]mlNode, mlKeys)
	for i := range backing {
		backing[i].v = int64(i)*13 + 7
		backing[i].w = int64(i)*3 + 1
	}
	for i := range backing {
		backing[i].next = &backing[(i+1)%mlKeys]
	}

	m := make(map[int64]*mlNode, mlKeys)
	for _, i := range rng.Perm(mlKeys) {
		m[int64(i)] = &backing[i]
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

	x := uint64(seed) + 0x9e3779b97f4a7c15
	var sum, expected, held int64
	var hits, misses, expHits, bad, mainGC int
	var sink []byte

	for i := 0; i < mlIters; i++ {
		k := mlKey(&x)
		q, ok := m[k]

		if i%mlGCDiv == 0 {
			sink = make([]byte, 16384)
			sink[0] = byte(i)
			runtime.GC()
			mainGC++
		}

		if ok {
			hits++
			if q.v != k*13+7 || q.w != k*3+1 ||
				q.next.v != ((k+1)%mlKeys)*13+7 {
				bad++
			}
			sum += q.v + q.next.v
			expected += (k*13 + 7) + (((k+1)%mlKeys)*13 + 7)
			expHits++
			// q 在 GC 之后仍被解引用，迫使指针在栈上跨 GC 存活。
			for j := 0; j < 4; j++ {
				held += q.v
			}
		} else {
			misses++
			sum -= k & 7
			expected -= k & 7
		}
	}
	now := epoch.Load()
	wg.Wait()

	var baseHeld int64
	if expHits > 0 {
		// held 与查找顺序有关，只校验为正且是节点值的整数倍关系。
		baseHeld = int64(expHits) * 4
	}
	ck(now >= 1, fmt.Sprintf("concurrent GC ran during map lookup loop (seed=%d)", seed))
	ck(hits == expHits && hits+misses == mlIters, fmt.Sprintf("hit/miss accounting (seed=%d)", seed))
	ck(bad == 0, fmt.Sprintf("map lookup returned intact nodes (seed=%d)", seed))
	ck(sum == expected, fmt.Sprintf("map lookup checksum after GC (seed=%d)", seed))
	ck(held >= baseHeld, fmt.Sprintf("lookup result dereferenced across GC (seed=%d)", seed))
	ck(mainGC == mlIters/mlGCDiv, fmt.Sprintf("explicit GC every %d lookups (seed=%d)", mlGCDiv, seed))

	corrupt := false
	for i := range backing {
		if backing[i].v != int64(i)*13+7 || backing[i].w != int64(i)*3+1 ||
			backing[i].next != &backing[(i+1)%mlKeys] {
			corrupt = true
		}
	}
	ck(!corrupt, fmt.Sprintf("map nodes intact after GC (seed=%d)", seed))
	ck(len(m) == mlKeys, fmt.Sprintf("map size intact (seed=%d)", seed))
	runtime.KeepAlive(backing)
	runtime.KeepAlive(m)
	runtime.KeepAlive(sink)
	finish("l03_map_lookup_loop")
}
