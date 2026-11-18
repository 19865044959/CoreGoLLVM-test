// L18: finalizer 与链式可达性——两条全局独立单链，head 指向下一个节点。
// 链 A/B 均可达时不得触发任何 finalizer；释放 A 后仅 A 终结，B 保持可达；
// 释放 B 后全部终结且每个对象只终结一次。只断言可达性与最终终结，不读 payload。
package main

import (
	"fmt"
	"os"
	"runtime"
	"strconv"
	"sync/atomic"
	"time"
)

const (
	frChainN  = 32
	frFlagN   = 256
	frIDBaseA = 100
	frIDBaseB = 200
	frTicks   = 20
	frWait    = 4 * time.Second
)

type frNode struct {
	id   int32
	data [24]byte
	next *frNode
}

var (
	failed  int32
	frBad   int32
	frDup   int32
	frFlags []int32
	frA     *frNode
	frB     *frNode
	frSeed  int64
)

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

func frSalt(seed int64) int {
	s := int(seed % 241)
	if s < 0 {
		s = -s
	}
	return s
}

func frByte(id int32, i, salt int) byte {
	return byte((int(id)*41 + i*13 + salt) % 251)
}

func seedValue() int64 {
	s := os.Getenv("UAF_SEED")
	if s == "" {
		return 1
	}
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return 1
	}
	return n
}

//go:noinline
func frBuildChain(base int32, n int, seed int64) *frNode {
	nodes := make([]*frNode, n)
	for i := range nodes {
		id := base + int32(i)
		p := &frNode{id: id}
		for j := range p.data {
			p.data[j] = frByte(id, j, frSalt(seed))
		}
		runtime.SetFinalizer(p, func(q *frNode) {
			idx := q.id
			if idx < 0 || int(idx) >= len(frFlags) {
				atomic.AddInt32(&frBad, 1)
				return
			}
			if atomic.AddInt32(&frFlags[idx], 1) > 1 {
				atomic.AddInt32(&frDup, 1)
			}
		})
		nodes[i] = p
	}
	for i := 0; i < n-1; i++ {
		nodes[i].next = nodes[i+1]
	}
	return nodes[0]
}

func frCount(base int32, n int) int {
	c := 0
	for i := 0; i < n; i++ {
		if atomic.LoadInt32(&frFlags[base+int32(i)]) != 0 {
			c++
		}
	}
	return c
}

func frPoll(base int32, n int) bool {
	deadline := time.Now().Add(frWait)
	for {
		if frCount(base, n) == n {
			return true
		}
		if time.Now().After(deadline) {
			return false
		}
		runtime.GC()
		runtime.Gosched()
		time.Sleep(time.Millisecond)
	}
}

func main() {
	frSeed = seedValue()
	frFlags = make([]int32, frFlagN)
	frA = frBuildChain(frIDBaseA, frChainN, frSeed)
	frB = frBuildChain(frIDBaseB, frChainN, frSeed)

	for i := 0; i < frTicks; i++ {
		runtime.GC()
		runtime.Gosched()
		time.Sleep(time.Millisecond)
	}
	ck(frCount(frIDBaseA, frChainN) == 0, fmt.Sprintf("chain A finalized while globally reachable (seed=%d)", frSeed))
	ck(frCount(frIDBaseB, frChainN) == 0, fmt.Sprintf("chain B finalized while globally reachable (seed=%d)", frSeed))

	frA = nil
	okA := frPoll(frIDBaseA, frChainN)
	ck(okA, fmt.Sprintf("chain A not fully finalized after release (seed=%d)", frSeed))
	ck(frCount(frIDBaseB, frChainN) == 0, fmt.Sprintf("chain B finalized while still reachable (seed=%d)", frSeed))

	frB = nil
	okB := frPoll(frIDBaseB, frChainN)
	ck(okB, fmt.Sprintf("chain B not fully finalized after release (seed=%d)", frSeed))

	for i := 0; i < frChainN; i++ {
		ck(atomic.LoadInt32(&frFlags[frIDBaseA+int32(i)]) == 1, fmt.Sprintf("chain A node %d finalizer count != 1 (seed=%d)", i, frSeed))
		ck(atomic.LoadInt32(&frFlags[frIDBaseB+int32(i)]) == 1, fmt.Sprintf("chain B node %d finalizer count != 1 (seed=%d)", i, frSeed))
	}
	ck(atomic.LoadInt32(&frDup) == 0, fmt.Sprintf("duplicate finalizer deliveries=%d", atomic.LoadInt32(&frDup)))
	ck(atomic.LoadInt32(&frBad) == 0, fmt.Sprintf("invalid finalizer id deliveries=%d", atomic.LoadInt32(&frBad)))

	finish("l18_finalizer_chain_release")
}
