// L03: 长循环反复 panic/recover。每 3 轮在 4 层 noinline 调用深处 panic
// 一个堆节点指针，最外层 defer/recover 取回并解引用；部分轮次在 panic 前
// 显式 GC。另有 GC pump 并发，校验 recover 次数、载荷指针和累加和。
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
	failed       int32
	prKeep       [][]byte
	prRecovered  int64
	prSum        int64
	prBadPayload int64
	prGCs        int64
)

const (
	prSlots  = 128
	prRounds = 4096
	prGCDiv  = 97
	prGCStep = 3 * prGCDiv
	prDepth  = 3
)

type prNode struct {
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

//go:noinline
func prLeaf(p *prNode) {
	panic(p)
}

//go:noinline
func prDeep(p *prNode, depth int) {
	if depth == 0 {
		prLeaf(p)
		return
	}
	prDeep(p, depth-1)
}

//go:noinline
func prRound(p *prNode, r int64) (out int64, panicked bool) {
	defer func() {
		if rec := recover(); rec != nil {
			panicked = true
			prRecovered++
			q, ok := rec.(*prNode)
			if !ok || q != p || q.v != p.v || q.tag != "pr" {
				prBadPayload++
			}
			prSum += p.v + (r & 7)
		}
	}()

	if r%3 == 0 {
		if r%prGCDiv == 0 {
			junk := make([]byte, 16384)
			junk[0] = byte(r)
			runtime.GC()
			prGCs++
			runtime.KeepAlive(junk)
		}
		prDeep(p, prDepth)
	}
	return p.v*2 + r, false
}

func main() {
	seed := seedFromEnv()
	if seed < 0 {
		seed = -seed
	}
	rng := rand.New(rand.NewSource(seed))
	step := int64(2*rng.Intn(31) + 1)
	off := int64(rng.Intn(prSlots))

	backing := make([]prNode, prSlots)
	for i := range backing {
		backing[i].v = int64(i)*5 + 2
		backing[i].tag = "pr"
	}

	start := make(chan struct{})
	var epoch atomic.Int64
	var wg sync.WaitGroup
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
			prKeep = junk
			runtime.GC()
			epoch.Add(1)
		}
		prKeep = nil
	}()
	close(start)

	var outSum int64
	var expRecovered, expRecoverSum, expOut int64
	var mainGC int64
	for r := int64(0); r < prRounds; r++ {
		idx := int((r*step + off) % prSlots)
		p := &backing[idx]
		out, pan := prRound(p, r)
		if pan {
			expRecovered++
			expRecoverSum += p.v + (r & 7)
		} else {
			outSum += out
			expOut += p.v*2 + r
		}
		if r%1000 == 0 {
			junk := make([]byte, 8192)
			junk[0] = byte(r)
			runtime.GC()
			mainGC++
			runtime.KeepAlive(junk)
		}
	}
	for epoch.Load() < 1 {
		runtime.Gosched()
	}
	now := epoch.Load()
	wg.Wait()

	ck(now >= 1, fmt.Sprintf("GC pump completed during panic loop (seed=%d)", seed))
	ck(prRecovered == expRecovered, fmt.Sprintf("recover count (seed=%d)", seed))
	ck(prBadPayload == 0, fmt.Sprintf("recover payload pointer intact (seed=%d)", seed))
	ck(prSum == expRecoverSum, fmt.Sprintf("recovered pointer checksum (seed=%d)", seed))
	ck(outSum == expOut, fmt.Sprintf("non-panic return checksum (seed=%d)", seed))
	ck(prGCs == (prRounds-1)/prGCStep+1, fmt.Sprintf("GC inside panic path (seed=%d)", seed))
	ck(mainGC == prRounds/1000+1, fmt.Sprintf("main explicit GC during loop (seed=%d)", seed))

	corrupt := false
	for i := range backing {
		if backing[i].v != int64(i)*5+2 || backing[i].tag != "pr" {
			corrupt = true
		}
	}
	ck(!corrupt, fmt.Sprintf("panic-loop nodes intact after GC (seed=%d)", seed))
	runtime.KeepAlive(backing)
	runtime.KeepAlive(prKeep)
	finish("l03_panic_recover_loop")
}
