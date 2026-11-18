// L04: 栈收缩 + panic 回卷。worker goroutine 先深递归（每帧 64 个指针槽）
// 并在叶帧显式 GC，然后 panic 携带指针 payload；recover 回到浅栈后主协程
// 再 GC，给 runtime 收缩栈的机会；同一 goroutine 反复增长/回卷。
package main

import (
	"fmt"
	"os"
	"runtime"
	"strconv"
	"sync/atomic"
)

type suNode struct {
	v    int64
	next *suNode
}

type suPayload struct {
	depth int
	sum   int64
	chain *suNode
}

const suSlots = 64

var (
	failed int32
	gcSink [][]byte
)

func ck(ok bool, msg string) {
	if !ok {
		atomic.AddInt32(&failed, 1)
		fmt.Println("FAIL:", msg)
	}
}

func ckf(ok bool, format string, args ...any) {
	if !ok {
		atomic.AddInt32(&failed, 1)
		fmt.Printf("FAIL: "+format+"\n", args...)
	}
}

func gcStress() {
	for i := 0; i < 8; i++ {
		b := make([]byte, 4096)
		b[0] = byte(i)
		gcSink = append(gcSink[:0], b)
	}
	gcSink = gcSink[:0]
	runtime.GC()
	runtime.GC()
}

func finish(name string, seed int64) {
	if n := atomic.LoadInt32(&failed); n != 0 {
		fmt.Printf("FAILED %s (UAF_SEED=%d, %d failed checks)\n", name, seed, n)
		os.Exit(1)
	}
	fmt.Println("ALL PASS " + name)
}

func envSeed() int64 {
	s := os.Getenv("UAF_SEED")
	if s == "" {
		return 1
	}
	v, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return 1
	}
	return v
}

func envRounds() int {
	s := os.Getenv("TORTURE_ROUNDS")
	if s == "" {
		return 1
	}
	v, err := strconv.Atoi(s)
	if err != nil || v < 1 {
		return 1
	}
	return v
}

func suDeep(depth, maxDepth int, shared []*suNode) int64 {
	var slots [suSlots]*suNode
	for i := range slots {
		slots[i] = shared[(depth+i)%len(shared)]
	}
	var sum int64
	for _, n := range slots {
		sum += n.v
	}
	if depth >= maxDepth {
		// 最深处显式 GC：指针同时位于全部祖先帧、叶帧槽位和堆链上。
		gcStress()
		panic(&suPayload{depth: depth, sum: sum, chain: shared[0]})
	}
	return sum + suDeep(depth+1, maxDepth, shared)
}

func main() {
	seed := envSeed()
	rounds := 3 * envRounds()
	depth := 400 + int(seed%80)
	mul := int64(1009 + seed%13)

	shared := make([]*suNode, 4)
	for i := range shared {
		shared[i] = &suNode{v: seed*100003 + int64(i)*mul}
	}
	for i := range shared {
		shared[i].next = shared[(i+1)%len(shared)]
	}
	wantSum := func(d int) int64 {
		var s int64
		for i := 0; i < suSlots; i++ {
			s += shared[(d+i)%len(shared)].v
		}
		return s
	}

	paused := make(chan int, 1)
	resume := make(chan struct{})
	report := make(chan int, rounds+1)

	go func() {
		defer close(report)
		for r := 0; r < rounds; r++ {
			func() {
				defer func() {
					v := recover()
					if v == nil {
						report <- -1
						return
					}
					p, ok := v.(*suPayload)
					if !ok || p == nil {
						ckf(false, "round %d recover type %T", r, v)
						report <- -1
						return
					}
					ok = p.depth == depth && p.sum == wantSum(depth) && p.chain == shared[0]
					ckf(ok, "round %d payload depth/sum/chain identity mismatch", r)
					if p.chain == nil || p.chain.next == nil || p.chain.next.next == nil {
						ckf(false, "round %d payload chain missing after unwind", r)
					} else if p.chain.v != seed*100003 || p.chain.next.v != seed*100003+mul {
						ckf(false, "round %d payload chain values corrupted after unwind", r)
					}
					report <- p.depth
				}()
				_ = suDeep(0, depth, shared)
				ckf(false, "round %d deep recursion returned without panic", r)
				report <- -2
			}()
			// 回卷完成，goroutine 已回到浅栈；等主协程 GC 触发栈收缩。
			paused <- r
			<-resume
		}
	}()

	for r := 0; r < rounds; r++ {
		got := <-report
		ckf(got >= 0, "round %d panic/recover failed (code=%d)", r, got)
		if got >= 0 {
			ckf(got == depth, "round %d recovered depth=%d want=%d", r, got, depth)
		}
		<-paused
		// 当前 goroutine 栈使用量远低于峰值，此处 GC 触发 scanstack/shrinkstack。
		gcStress()
		resume <- struct{}{}
	}
	<-report // report 关闭后读到零值退出
	gcStress()
	for i, n := range shared {
		ok := n != nil && n.v == seed*100003+int64(i)*mul
		ckf(ok, "shared chain node %d corrupted after final GC", i)
	}

	finish("l04_stack_shrink_unwind", seed)
}
