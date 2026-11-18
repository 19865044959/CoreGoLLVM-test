// L03: defer + 闭包链循环。每轮调用 8 层 noinline 递归，每层登记一个捕获
// 节点指针的 defer 闭包；最深层周期性显式 GC，随后逐层返回时闭包按 LIFO
// 执行。另有 GC pump 并发，校验 defer 次数、LIFO 和与返回值和。
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
	failed   int32
	dcKeep   [][]byte
	dcDefers int64
	dcDefSum int64
)

const (
	dcSlots  = 96
	dcRounds = 4096
	dcDepth  = 8
	dcGCDiv  = 97
)

type dcNode struct {
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
func dcChain(p *dcNode, depth int, r int64) int64 {
	if depth == 0 {
		if r%dcGCDiv == 0 {
			junk := make([]byte, 16384)
			junk[0] = byte(r)
			runtime.GC()
			runtime.KeepAlive(junk)
		}
		return p.v
	}
	defer func(q *dcNode) {
		dcDefers++
		dcDefSum += q.v + int64(depth)
	}(p)
	return dcChain(p, depth-1, r) + 1
}

func main() {
	seed := seedFromEnv()
	if seed < 0 {
		seed = -seed
	}
	rng := rand.New(rand.NewSource(seed))
	step := int64(2*rng.Intn(31) + 1)
	off := int64(rng.Intn(dcSlots))

	backing := make([]dcNode, dcSlots)
	for i := range backing {
		backing[i].v = int64(i)*9 + 4
		backing[i].tag = "dc"
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
			dcKeep = junk
			runtime.GC()
			epoch.Add(1)
		}
		dcKeep = nil
	}()
	close(start)

	var outSum, expOut, expDefSum int64
	var mainGC int64
	for r := int64(0); r < dcRounds; r++ {
		idx := int((r*step + off) % dcSlots)
		p := &backing[idx]
		outSum += dcChain(p, dcDepth, r)
		nv := p.v
		expOut += nv + dcDepth
		expDefSum += int64(dcDepth)*nv + int64(dcDepth*(dcDepth+1)/2)
		if r%997 == 0 {
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

	ck(now >= 1, fmt.Sprintf("GC pump completed during defer-chain loop (seed=%d)", seed))
	ck(dcDefers == dcRounds*dcDepth, fmt.Sprintf("defer closure count (seed=%d)", seed))
	ck(dcDefSum == expDefSum, fmt.Sprintf("defer closure LIFO sum (seed=%d)", seed))
	ck(outSum == expOut, fmt.Sprintf("defer-chain return sum (seed=%d)", seed))
	ck(mainGC == dcRounds/997+1, fmt.Sprintf("main explicit GC during loop (seed=%d)", seed))

	corrupt := false
	for i := range backing {
		if backing[i].v != int64(i)*9+4 || backing[i].tag != "dc" {
			corrupt = true
		}
	}
	ck(!corrupt, fmt.Sprintf("defer-chain nodes intact after GC (seed=%d)", seed))
	runtime.KeepAlive(backing)
	runtime.KeepAlive(dcKeep)
	finish("l03_defer_closure_chain")
}
