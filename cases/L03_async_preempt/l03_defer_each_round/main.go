// L03: 每轮 defer。每轮调用 noinline 的 roundDefer，函数内登记 1~2 个 defer，
// 其中部分轮次在 defer 登记后 panic 并由最外层 defer recover；每 97 轮在 defer
// 尚未执行时显式 GC。另有 GC pump 并发；校验 defer 执行次数、LIFO 顺序和和。
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

var (
	deferN    int64
	deferSum  int64
	recovered int64
)

const (
	drSlots = 256
	drIters = 2048
	drGCDiv = 97
	drPanic = 211
)

type d2Node struct {
	v int64
	w int64
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

//go:noinline
func drGC(p *d2Node, marker int64) {
	junk := make([]byte, 16384)
	junk[0] = byte(marker)
	runtime.GC()
	runtime.KeepAlive(junk)
	runtime.KeepAlive(p)
}

//go:noinline
func roundDefer(p *d2Node, r int64) (out int64, pan bool) {
	defer func() {
		if rec := recover(); rec != nil {
			pan = true
			recovered++
		}
	}()
	defer func() {
		deferN++
		deferSum += p.v + 2
	}()

	if r%drPanic == 0 {
		panic(p.v)
	}

	defer func() {
		deferN++
		deferSum += p.w + 3
	}()

	if r%drGCDiv == 0 {
		drGC(p, r&0xff)
	}

	out = p.v + p.w
	return
}

func main() {
	seed := seedFromEnv()
	if seed < 0 {
		seed = -seed
	}
	rng := rand.New(rand.NewSource(seed))
	step := int64(2*rng.Intn(128) + 1)
	off := int64(rng.Intn(drSlots))

	backing := make([]d2Node, drSlots)
	for i := range backing {
		backing[i].v = int64(i)*11 + 5
		backing[i].w = int64(i)*7 + 3
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

	var outSum, expN, expSum, expOut int64
	var panics int64
	var mainGC int
	var sink []byte
	for r := int64(0); r < drIters; r++ {
		idx := int((r*step + off) % drSlots)
		v, _ := roundDefer(&backing[idx], r)
		outSum += v

		idxExpected := int((r*step + off) % drSlots)
		pv := int64(idxExpected)*11 + 5
		pw := int64(idxExpected)*7 + 3
		expN += 2
		expSum += pv + 2 + pw + 3
		if r%drPanic == 0 {
			panics++
			expN--
			expSum -= pw + 3
		} else {
			expOut += pv + pw
		}

		if r%1000 == 0 {
			sink = make([]byte, 8192)
			sink[0] = byte(r)
			runtime.GC()
			mainGC++
		}
	}
	for epoch.Load() < 1 {
		runtime.Gosched()
	}
	now := epoch.Load()
	wg.Wait()

	ck(now >= 1, fmt.Sprintf("concurrent GC ran during defer loop (seed=%d)", seed))
	ck(deferN == expN, fmt.Sprintf("per-round defer count (seed=%d)", seed))
	ck(deferSum == expSum, fmt.Sprintf("per-round defer LIFO sum (seed=%d)", seed))
	ck(recovered == panics, fmt.Sprintf("recover count (seed=%d)", seed))
	ck(outSum == expOut, fmt.Sprintf("non-panic return values (seed=%d)", seed))
	ck(mainGC == drIters/1000+1, fmt.Sprintf("main-thread GC during loop (seed=%d)", seed))

	corrupt := false
	for i := range backing {
		if backing[i].v != int64(i)*11+5 || backing[i].w != int64(i)*7+3 {
			corrupt = true
		}
	}
	ck(!corrupt, fmt.Sprintf("defer nodes intact after GC (seed=%d)", seed))
	runtime.KeepAlive(backing)
	runtime.KeepAlive(sink)
	finish("l03_defer_each_round")
}
