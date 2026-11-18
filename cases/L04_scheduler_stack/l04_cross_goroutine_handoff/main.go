// L04: 两个 goroutine 之间按轮次交接指针所有权；指针分别停留在 channel
// 缓冲区与接收方栈上时各 GC 一次，最终由主协程在 GC 后校验整条链。
package main

import (
	"fmt"
	"os"
	"runtime"
	"strconv"
	"sync/atomic"
)

type node struct {
	v    int64
	next *node
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

const handoffLen = 8

func buildHandoffChain(base int64) *node {
	var head *node
	for j := handoffLen - 1; j >= 0; j-- {
		head = &node{v: base + int64(j)*13, next: head}
	}
	return head
}

func handoffSum(head *node) (int64, int, bool) {
	var sum int64
	n := 0
	for p := head; p != nil; p = p.next {
		sum += p.v
		n++
		if p.next != nil && p.next.v != p.v+13 {
			return 0, 0, false
		}
	}
	return sum, n, true
}

func main() {
	seed := envSeed()
	const rounds = 8

	chAB := make(chan *node, 4)
	chBA := make(chan *node, 4)
	sent := make(chan int, rounds)
	permitB := make(chan struct{})
	gateCh := make(chan chan struct{}, 1)
	gotB := make(chan int, rounds)
	doneA := make(chan int, rounds)
	bDone := make(chan struct{})

	returned := make([]*node, rounds)
	var badA, badB int32

	go func() { // B：接收 → GC 后校验 → 原地改值 → 交还
		defer close(bDone)
		for r := 0; r < rounds; r++ {
			<-permitB
			head := <-chAB
			gotB <- r
			gate := <-gateCh
			<-gate
			if _, _, ok := handoffSum(head); !ok {
				atomic.StoreInt32(&badB, 1)
			}
			for p := head; p != nil; p = p.next {
				p.v += 1000
			}
			chBA <- head
		}
	}()

	go func() { // A：构建 → 交出 → 收回 → 校验
		for r := 0; r < rounds; r++ {
			base := seed*97 + int64(r)*100003
			head := buildHandoffChain(base)
			chAB <- head
			sent <- r
			head = <-chBA
			if sum, n, ok := handoffSum(head); !ok || n != handoffLen || sum != base*handoffLen+int64(handoffLen*(handoffLen-1)/2)*13+1000*handoffLen {
				atomic.StoreInt32(&badA, 1)
			}
			returned[r] = head
			doneA <- r
		}
	}()

	for r := 0; r < rounds; r++ {
		<-sent
		// 指针只存在于 chAB 缓冲区。
		gcStress()
		permitB <- struct{}{}
		<-gotB
		// 指针只存在于 B 的栈上；关闭 gate 前 GC。
		gcStress()
		g := make(chan struct{})
		gateCh <- g
		close(g)
		<-doneA
	}

	<-bDone
	gcStress()
	ckf(atomic.LoadInt32(&badA) == 0, "A observed corrupted chain after handoff")
	ckf(atomic.LoadInt32(&badB) == 0, "B observed corrupted chain after GC")
	for r := 0; r < rounds; r++ {
		head := returned[r]
		base := seed*97 + int64(r)*100003
		sum, n, ok := handoffSum(head)
		want := base*handoffLen + int64(handoffLen*(handoffLen-1)/2)*13 + 1000*handoffLen
		ckf(ok && n == handoffLen, "round %d chain shape broken", r)
		ckf(sum == want, "round %d sum=%d want=%d", r, sum, want)
	}
	finish("l04_cross_goroutine_handoff")
}
