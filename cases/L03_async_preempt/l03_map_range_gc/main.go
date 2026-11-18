// L03: map range 长循环每轮遍历 4096 个 *node 并追一次 next 指针，
// 遍历轮次之间显式 GC、另有 GC pump 并发；map 遍历顺序随机但校验和不变。
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
	failed     int32
	gcKeep     [][]byte
	mapKeep    []byte
	mapBadPass int32
)

const (
	mSlots  = 4096
	mPasses = 96
)

type mNode struct {
	v    int64
	name string
	next *mNode
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
	rng := rand.New(rand.NewSource(seed))

	backing := make([]mNode, mSlots)
	for i := range backing {
		backing[i].v = int64(i)*7 + 3
		backing[i].name = "m"
	}
	for i := range backing {
		backing[i].next = &backing[(i+1)%mSlots]
	}

	m := make(map[int]*mNode, mSlots)
	var passBase int64
	for _, idx := range rng.Perm(mSlots) {
		m[idx] = &backing[idx]
		passBase += backing[idx].v + 1 + backing[idx].next.v
	}

	var epoch atomic.Int64
	var wg sync.WaitGroup
	start := make(chan struct{})
	wg.Add(1)
	go func() {
		defer wg.Done()
		<-start
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

	var total int64
	passes := 0
	for (epoch.Load() < 1 || passes < mPasses) && passes < mPasses*16 {
		var s int64
		elems := 0
		for k, q := range m {
			if k < 0 {
				s -= 1 << 20
			}
			s += q.v + 1
			if q.next != nil {
				s += q.next.v
			}
			elems++
		}
		if s != passBase || elems != mSlots {
			atomic.AddInt32(&mapBadPass, 1)
		}
		total += s
		passes++
		if passes%8 == 0 {
			mapKeep = make([]byte, 16384)
			mapKeep[0] = byte(passes)
			runtime.GC()
		}
	}
	now := epoch.Load()
	wg.Wait()

	ck(now >= 1, fmt.Sprintf("GC completed during map range loop (seed=%d)", seed))
	ck(atomic.LoadInt32(&mapBadPass) == 0, fmt.Sprintf("every map range pass checksum (seed=%d)", seed))
	ck(total == int64(passes)*passBase, fmt.Sprintf("map range aggregate checksum (seed=%d)", seed))
	ck(len(m) == mSlots, fmt.Sprintf("map length after GC (seed=%d)", seed))

	corrupt := false
	for i := range backing {
		if backing[i].v != int64(i)*7+3 || backing[i].name != "m" ||
			backing[i].next != &backing[(i+1)%mSlots] {
			corrupt = true
		}
	}
	ck(!corrupt, fmt.Sprintf("map nodes intact after GC (seed=%d)", seed))
	runtime.KeepAlive(backing)
	runtime.KeepAlive(m)
	runtime.KeepAlive(mapKeep)
	finish("l03_map_range_gc")
}
