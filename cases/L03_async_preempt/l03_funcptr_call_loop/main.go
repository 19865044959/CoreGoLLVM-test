// L03: 函数指针表驱动的长循环，每轮按种子派生的下标取一个函数值并间接调用；
// 调用返回后继续解引用同一节点指针。每 32768 轮显式 GC，另有 GC pump 并发，
// 校验间接调用结果、指针目标与校验和。UAF_SEED 决定访问顺序。
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
	fpSlots = 4096
	fpIters = 262144
	fpGCDiv = 32768
)

type fNode struct {
	v    int64
	w    int64
	next *fNode
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
func fpAddV(n *fNode) int64 { return n.v + 1 }

//go:noinline
func fpAddW(n *fNode) int64 { return n.w*2 + 3 }

//go:noinline
func fpSum(n *fNode) int64 { return n.v + n.w }

//go:noinline
func fpNextV(n *fNode) int64 { return n.next.v - n.w }

//go:noinline
func fpMix(n *fNode) int64 { return n.v*3 + n.w*5 + 7 }

//go:noinline
func fpNextW(n *fNode) int64 { return n.next.w*7 - n.v }

// fpRef 是与函数表等价的直接调用参考实现，用于校验间接调用结果。
func fpRef(i int, n *fNode) int64 {
	switch i {
	case 0:
		return n.v + 1
	case 1:
		return n.w*2 + 3
	case 2:
		return n.v + n.w
	case 3:
		return n.next.v - n.w
	case 4:
		return n.v*3 + n.w*5 + 7
	default:
		return n.next.w*7 - n.v
	}
}

func main() {
	seed := seedFromEnv()
	if seed < 0 {
		seed = -seed
	}
	rng := rand.New(rand.NewSource(seed))
	step := int64(2*rng.Intn(2048) + 1)
	off := int64(rng.Intn(fpSlots))
	step2 := int64(2*rng.Intn(32) + 1)

	table := []func(*fNode) int64{fpAddV, fpAddW, fpSum, fpNextV, fpMix, fpNextW}
	off2 := int64(rng.Intn(len(table)))

	backing := make([]fNode, fpSlots)
	for i := range backing {
		backing[i].v = int64(i)*13 + 5
		backing[i].w = int64(i)*7 + 9
	}
	for i := range backing {
		backing[i].next = &backing[(i+1)%fpSlots]
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

	var sum, expected, done int64
	var bad, mainGC int
	var sink []byte
	for (epoch.Load() < 1 || done < fpIters) && done < fpIters*2 {
		idx := int((done*step + off) % fpSlots)
		fi := int((done*step2 + off2) % int64(len(table)))
		n := &backing[idx]

		got := table[fi](n)
		want := fpRef(fi, n)
		nextV := n.next.v
		if got != want {
			bad++
		}
		sum += got + nextV
		expected += want + nextV
		done++

		if done%fpGCDiv == 0 {
			sink = make([]byte, 16384)
			sink[0] = byte(done)
			runtime.GC()
			mainGC++
		}
	}
	now := epoch.Load()
	wg.Wait()

	ck(now >= 1, fmt.Sprintf("concurrent GC ran during funcptr loop (seed=%d)", seed))
	ck(done >= fpIters, fmt.Sprintf("funcptr loop executed %d iterations (seed=%d)", done, seed))
	ck(bad == 0, fmt.Sprintf("indirect call results match reference (seed=%d)", seed))
	ck(sum == expected, fmt.Sprintf("funcptr checksum after GC (seed=%d)", seed))
	ck(mainGC >= fpIters/fpGCDiv, fmt.Sprintf("explicit GC every %d rounds (seed=%d)", fpGCDiv, seed))

	corrupt := false
	for i := range backing {
		if backing[i].v != int64(i)*13+5 || backing[i].w != int64(i)*7+9 ||
			backing[i].next != &backing[(i+1)%fpSlots] {
			corrupt = true
		}
	}
	ck(!corrupt, fmt.Sprintf("funcptr nodes intact after GC (seed=%d)", seed))
	runtime.KeepAlive(backing)
	runtime.KeepAlive(table)
	runtime.KeepAlive(sink)
	finish("l03_funcptr_call_loop")
}
