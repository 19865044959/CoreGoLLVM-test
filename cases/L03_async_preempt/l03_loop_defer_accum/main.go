// L03: 循环体内同时做两种 defer：helper 内每轮执行一次的 defer，
// 以及主循环里逐轮登记、直到函数返回才执行的 defer（携带指针参数）。
// 循环中每 1024 轮显式 GC，另有 GC pump 并发，验证 defer 记录/栈上的指针位图。
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
	failed        int32
	gcKeep        [][]byte
	deferKeep     []byte
	deferReturnN  int64
	deferReturnSm int64
	accumN        int64
	accumSm       int64
)

const (
	dSlots = 128
	dIters = 8192
	dCap   = dIters * 2
	dGCDiv = 1024
)

type dNode struct {
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

func dIdxAt(i, step, off int64) int {
	return int((i*step + off) % dSlots)
}

//go:noinline
func deferWork(p *dNode) int64 {
	v := p.v + 1
	defer func(q *dNode) {
		deferReturnSm += q.v
		deferReturnN++
	}(p)
	return v
}

func main() {
	seed := seedFromEnv()
	if seed < 0 {
		seed = -seed
	}
	rng := rand.New(rand.NewSource(seed))
	step := int64(2*rng.Intn(16) + 1)
	off := int64(rng.Intn(dSlots))

	backing := make([]dNode, dSlots)
	for i := range backing {
		backing[i].v = int64(i)*11 + 5
		backing[i].tag = "d"
	}

	var epoch atomic.Int64
	var wg sync.WaitGroup
	var done int64
	var sum int64
	var flagGC int32

	defer func() {
		// 先于本 defer 登记的循环内 defer 已按 LIFO 全部执行完毕。
		var expectedV int64
		for i := int64(0); i < done; i++ {
			expectedV += int64(dIdxAt(i, step, off))*11 + 5
		}
		ck(epoch.Load() >= 1, fmt.Sprintf("GC pump completed during defer loop (seed=%d)", seed))
		ck(flagGC != 0, fmt.Sprintf("explicit GC ran inside loop (seed=%d)", seed))
		ck(sum == expectedV+done, fmt.Sprintf("defer helper checksum (seed=%d)", seed))
		ck(deferReturnN == done, fmt.Sprintf("per-call defer count (seed=%d)", seed))
		ck(deferReturnSm == expectedV, fmt.Sprintf("per-call defer sum (seed=%d)", seed))
		ck(accumN == done, fmt.Sprintf("loop-accumulated defer count (seed=%d)", seed))
		ck(accumSm == expectedV, fmt.Sprintf("loop-accumulated defer sum (seed=%d)", seed))
		corrupt := false
		for i := range backing {
			if backing[i].v != int64(i)*11+5 || backing[i].tag != "d" {
				corrupt = true
			}
		}
		ck(!corrupt, fmt.Sprintf("defer-loop nodes intact after GC (seed=%d)", seed))
		runtime.KeepAlive(backing)
		runtime.KeepAlive(deferKeep)
		finish("l03_loop_defer_accum")
	}()

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

	for (epoch.Load() < 1 || done < dIters) && done < dCap {
		idx := dIdxAt(done, step, off)
		p := &backing[idx]
		sum += deferWork(p)
		defer func(q *dNode) {
			accumSm += q.v
			accumN++
		}(p)
		if done%dGCDiv == 0 {
			deferKeep = make([]byte, 16384)
			deferKeep[0] = byte(done)
			runtime.GC()
			flagGC = 1
		}
		done++
	}
	wg.Wait()
}
