// L04: 单个 goroutine 内逐层放大固定栈帧，触发多次 morestack/栈复制；
// 在最深帧阻塞并由主协程 GC，回溯时逐层校验外层帧指针槽仍指向原对象。
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

const (
	l1Slots = 256  // 2KB
	l2Slots = 512  // 4KB
	l3Slots = 1024 // 8KB
	l4Slots = 2048 // 16KB
	l5Slots = 4096 // 32KB
)

func buildChain(base int64) []*node {
	nodes := make([]*node, 4)
	for i := len(nodes) - 1; i >= 0; i-- {
		nodes[i] = &node{v: base + int64(i)*11}
		if i+1 < len(nodes) {
			nodes[i].next = nodes[i+1]
		}
	}
	return nodes
}

func fillAndSum(slots []*node, chain []*node, off int) (int32, int64) {
	for i := range slots {
		slots[i] = chain[(i+off)&3]
	}
	var sum int64
	for i := range slots {
		want := chain[(i+off)&3]
		if slots[i] != want || slots[i].v != want.v {
			return 1, 0
		}
		sum += slots[i].v
	}
	return 0, sum
}

//go:noinline
func growL5(chain []*node, ready chan<- int, release <-chan struct{}) (int32, int64) {
	var e [l5Slots]*node
	bad, _ := fillAndSum(e[:], chain, 4)
	ready <- 1
	<-release
	bad2, sum := fillAndSum(e[:], chain, 4)
	return bad | bad2, sum
}

//go:noinline
func growL4(chain []*node, ready chan<- int, release <-chan struct{}) (int32, int64) {
	var d [l4Slots]*node
	bad, _ := fillAndSum(d[:], chain, 3)
	bad5, sum5 := growL5(chain, ready, release)
	bad2, sum4 := fillAndSum(d[:], chain, 3)
	return bad | bad5 | bad2, sum4 + sum5
}

//go:noinline
func growL3(chain []*node, ready chan<- int, release <-chan struct{}) (int32, int64) {
	var c [l3Slots]*node
	bad, _ := fillAndSum(c[:], chain, 2)
	bad5, sum5 := growL4(chain, ready, release)
	bad2, sum := fillAndSum(c[:], chain, 2)
	return bad | bad5 | bad2, sum + sum5
}

//go:noinline
func growL2(chain []*node, ready chan<- int, release <-chan struct{}) (int32, int64) {
	var b [l2Slots]*node
	bad, _ := fillAndSum(b[:], chain, 1)
	bad5, sum5 := growL3(chain, ready, release)
	bad2, sum := fillAndSum(b[:], chain, 1)
	return bad | bad5 | bad2, sum + sum5
}

//go:noinline
func growL1(chain []*node, ready chan<- int, release <-chan struct{}) (int32, int64) {
	var a [l1Slots]*node
	bad, _ := fillAndSum(a[:], chain, 0)
	bad5, sum5 := growL2(chain, ready, release)
	bad2, sum := fillAndSum(a[:], chain, 0)
	return bad | bad5 | bad2, sum + sum5
}

type growResult struct {
	bad  int32
	sum  int64
	want int64
}

func runCycle(chain []*node, want int64) growResult {
	ready := make(chan int, 1)
	release := make(chan struct{})
	res := make(chan growResult, 1)
	go func() {
		bad, sum := growL1(chain, ready, release)
		res <- growResult{bad: bad, sum: sum, want: want}
	}()
	<-ready
	// 最深帧存活且五层指针槽全部在栈上，此时 GC 必须正确扫描/复制整条栈。
	gcStress()
	close(release)
	return <-res
}

func main() {
	seed := envSeed()
	const cycles = 6
	base := seed * 41
	chain := buildChain(base)
	chainSum := base*4 + 66
	totalSlots := int64(l1Slots + l2Slots + l3Slots + l4Slots + l5Slots)
	want := (totalSlots / 4) * chainSum

	for c := 0; c < cycles; c++ {
		r := runCycle(chain, want)
		ckf(r.bad == 0, "cycle %d outer/inner frame pointer corrupted", c)
		ckf(r.sum == r.want, "cycle %d sum=%d want=%d", c, r.sum, r.want)
		gcStress()
	}
	// 全部子 goroutine 退出后，链对象只由主协程持有，再 GC 一次并校验。
	gcStress()
	var got int64
	for _, p := range chain {
		got += p.v
	}
	ckf(got == chainSum, "chain after cycles sum=%d want=%d", got, chainSum)
	finish("l04_goroutine_stack_growth")
}
