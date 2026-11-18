// L04: 并发栈收缩：多个 worker 反复深递归并返回，每轮返回后由 main GC 触发
// 栈收缩；同时一个 anchor goroutine 一直停在深递归叶帧上，跨所有收缩 GC 保持
// 整条深栈活跃。收缩后再进入深递归，校验帧槽与堆链指针始终正确。
package main

import (
	"fmt"
	"os"
	"runtime"
	"strconv"
	"sync"
	"sync/atomic"
)

type shNode struct {
	v int64
}

type shAnchor struct {
	peak    chan struct{}
	release chan struct{}
	result  chan int64
}

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

func finish(name string) {
	if n := atomic.LoadInt32(&failed); n != 0 {
		fmt.Printf("FAILED %s (%d failed checks)\n", name, n)
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

func envInt(name string, def, lo, hi int) int {
	s := os.Getenv(name)
	if s == "" {
		return def
	}
	n, err := strconv.Atoi(s)
	if err != nil || n < lo {
		return def
	}
	if n > hi {
		n = hi
	}
	return n
}

func shAt(chain []*shNode, idx int) *shNode {
	return chain[idx&3]
}

//go:noinline
func shrinkWorkerRec(level, depth, salt int, chain []*shNode) int64 {
	var frame [24]*shNode
	for i := range frame {
		frame[i] = shAt(chain, i+level+salt)
	}
	if level == depth {
		var s int64
		for i := range frame {
			if frame[i] != shAt(chain, i+level+salt) {
				return -1 << 60
			}
			s += frame[i].v
		}
		return s
	}
	child := shrinkWorkerRec(level+1, depth, salt, chain)
	if child == -1<<60 {
		return child
	}
	var s int64
	for i := range frame {
		if frame[i] != shAt(chain, i+level+salt) {
			return -1 << 60
		}
		s += frame[i].v
	}
	return s + child
}

//go:noinline
func shrinkAnchorRec(level, depth, salt int, chain []*shNode, a *shAnchor) int64 {
	var frame [24]*shNode
	for i := range frame {
		frame[i] = shAt(chain, i+level+salt)
	}
	if level == depth {
		var leaf [1024]*shNode
		for i := range leaf {
			leaf[i] = shAt(chain, i+level+salt)
		}
		a.peak <- struct{}{}
		<-a.release
		var s int64
		for i := range leaf {
			if leaf[i] != shAt(chain, i+level+salt) {
				return -1 << 60
			}
			s += leaf[i].v
		}
		for i := range frame {
			if frame[i] != shAt(chain, i+level+salt) {
				return -1 << 60
			}
			s += frame[i].v
		}
		return s
	}
	child := shrinkAnchorRec(level+1, depth, salt, chain, a)
	if child == -1<<60 {
		return child
	}
	var s int64
	for i := range frame {
		if frame[i] != shAt(chain, i+level+salt) {
			return -1 << 60
		}
		s += frame[i].v
	}
	return s + child
}

func shrinkWant(depth, salt int, chain []*shNode) int64 {
	var s int64
	for d := 0; d <= depth; d++ {
		for i := 0; i < 24; i++ {
			s += shAt(chain, i+d+salt).v
		}
	}
	return s
}

func anchorWant(depth, salt int, chain []*shNode) int64 {
	var s int64
	for d := 0; d <= depth; d++ {
		for i := 0; i < 24; i++ {
			s += shAt(chain, i+d+salt).v
		}
	}
	for i := 0; i < 1024; i++ {
		s += shAt(chain, i+depth+salt).v
	}
	return s
}

func makeShChain(seed int64, tag int) []*shNode {
	b := seed*100003 + int64(tag)*313
	return []*shNode{{v: b + 1}, {v: b + 2}, {v: b + 3}, {v: b + 4}}
}

func runShrink(seed int64, workers, cycles int) {
	const workerDepth = 420
	const anchorDepth = 360

	anchorChain := makeShChain(seed, 9001)
	anchorSalt := 17
	a := &shAnchor{peak: make(chan struct{}), release: make(chan struct{}), result: make(chan int64, 1)}
	go func() {
		a.result <- shrinkAnchorRec(0, anchorDepth, anchorSalt, anchorChain, a)
	}()
	<-a.peak

	acks := make([]chan struct{}, cycles)
	for c := 0; c < cycles; c++ {
		acks[c] = make(chan struct{})
	}
	cycleDone := make(chan int, workers)
	finished := make(chan int64, workers)
	kept := make([][]*shNode, workers*(cycles+1))
	var wg sync.WaitGroup
	wg.Add(workers)

	for w := 0; w < workers; w++ {
		go func(w int) {
			defer wg.Done()
			var sum int64
			for c := 0; c < cycles; c++ {
				chain := makeShChain(seed+int64(c), w*100+c)
				kept[w*(cycles+1)+c] = chain
				salt := int(seed%7) + c*3 + w*5
				got := shrinkWorkerRec(0, workerDepth, salt, chain)
				want := shrinkWant(workerDepth, salt, chain)
				if got != want {
					atomic.AddInt32(&failed, 1)
					fmt.Printf("FAIL: worker %d cycle %d got=%d want=%d\n", w, c, got, want)
				}
				sum += got
				cycleDone <- w
				<-acks[c]
			}
			// 收缩后重新进入深递归，确认增长路径仍正常。
			chain := makeShChain(seed+int64(cycles), w*100+cycles)
			kept[w*(cycles+1)+cycles] = chain
			salt := int(seed%7) + cycles*3 + w*5
			got := shrinkWorkerRec(0, workerDepth, salt, chain)
			want := shrinkWant(workerDepth, salt, chain)
			if got != want {
				atomic.AddInt32(&failed, 1)
				fmt.Printf("FAIL: worker %d re-entry got=%d want=%d\n", w, got, want)
			}
			finished <- got
		}(w)
	}

	for c := 0; c < cycles; c++ {
		for i := 0; i < workers; i++ {
			<-cycleDone
		}
		// worker 全部已从深递归返回、anchor 仍在深栈上；GC 触发收缩。
		gcStress()
		close(acks[c])
	}
	for i := 0; i < workers; i++ {
		<-finished
	}
	wg.Wait()

	gcStress()
	close(a.release)
	got := <-a.result
	want := anchorWant(anchorDepth, anchorSalt, anchorChain)
	ckf(got == want, "anchor got=%d want=%d", got, want)
	ckf(got != -1<<60, "anchor frame slot corrupted across shrink GCs")
	gcStress()
	for i := range kept {
		for j, n := range kept[i] {
			if n == nil {
				ckf(false, "kept[%d][%d] missing after shrink GC", i, j)
			}
		}
	}
	// anchor 返回后其深栈也收缩；再 GC 一次确认无残留破坏。
	gcStress()
	finish("l04_stack_shrink_concurrent")
}

func main() {
	seed := envSeed()
	workers := envInt("TORTURE_WORKERS", 4, 1, 12)
	cycles := envInt("TORTURE_ROUNDS", 4, 1, 12)
	runShrink(seed, workers, cycles)
}
