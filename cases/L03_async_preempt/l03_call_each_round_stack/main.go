// L03: 长循环每轮调用 noinline 深递归链（每帧 2KiB），使 goroutine 栈反复增长；
// 每 8 轮在浅栈上显式 GC 触发栈收缩，GC pump 并发在递归中途打断调用链。
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
	csKeep []byte
)

const (
	csSlots  = 64
	csDepth  = 20
	csRounds = 1024
	csGCDiv  = 8
)

type csNode struct {
	v    int64
	name string
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

//go:noinline
func deepChain(level int, p *csNode, salt int64) int64 {
	var frame [256]int64
	frame[level&255] = p.v + salt + int64(level)
	if level == 0 {
		return p.v + salt
	}
	return deepChain(level-1, p, salt+1) + frame[level&255]
}

func csIdxAt(i, step, off int64) int {
	return int((i*step + off) % csSlots)
}

func csExpected(idx int, salt int64) int64 {
	v := int64(idx)*2 + 6
	base := v + salt + int64(csDepth)
	return int64(csDepth+1) * base
}

func main() {
	seed := seedFromEnv()
	if seed < 0 {
		seed = -seed
	}
	rng := rand.New(rand.NewSource(seed))
	step := int64(2*rng.Intn(16) + 1)
	off := int64(rng.Intn(csSlots))
	rounds := tortureRounds(csRounds)

	backing := make([]csNode, csSlots)
	for i := range backing {
		backing[i].v = int64(i)*2 + 6
		backing[i].name = "c"
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

	var sum int64
	var done int64
	limit := int64(rounds) * 4
	for (epoch.Load() < 1 || done < int64(rounds)) && done < limit {
		idx := csIdxAt(done, step, off)
		p := &backing[idx]
		sum += deepChain(csDepth, p, done&7)
		if done%csGCDiv == 0 {
			csKeep = make([]byte, 16384)
			csKeep[0] = byte(done)
			runtime.GC()
		}
		done++
	}
	now := epoch.Load()
	wg.Wait()

	var expected int64
	for i := int64(0); i < done; i++ {
		expected += csExpected(csIdxAt(i, step, off), i&7)
	}

	ck(now >= 1, fmt.Sprintf("GC pump completed during call-per-round loop (seed=%d)", seed))
	ck(done >= int64(rounds), fmt.Sprintf("call-per-round loop executed (seed=%d)", seed))
	ck(sum == expected, fmt.Sprintf("call-per-round stack checksum (seed=%d)", seed))

	corrupt := false
	for i := range backing {
		if backing[i].v != int64(i)*2+6 || backing[i].name != "c" {
			corrupt = true
		}
	}
	ck(!corrupt, fmt.Sprintf("call-chain nodes intact after GC (seed=%d)", seed))
	runtime.KeepAlive(backing)
	runtime.KeepAlive(csKeep)
	finish("l03_call_each_round_stack")
}
