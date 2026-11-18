// L04: 大量 goroutine 同时进入大栈帧函数并在帧内阻塞，触发每个 goroutine
// 独立的 morestack/栈复制；阻塞点由主协程 GC，唤醒后逐槽校验指针身份。
package main

import (
	"fmt"
	"os"
	"runtime"
	"strconv"
	"sync"
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

const (
	bigSlots  = 3072 // 24KB 指针槽
	auxSlots  = 1536 // 12KB 指针槽
	leafSlots = 2048 // 16KB 指针槽
)

func buildChain(id int, seed int64) []*node {
	base := int64(id)*1000 + seed*17
	nodes := make([]*node, 4)
	for i := len(nodes) - 1; i >= 0; i-- {
		nodes[i] = &node{v: base + int64(i)*11}
		if i+1 < len(nodes) {
			nodes[i].next = nodes[i+1]
		}
	}
	return nodes
}

func slotIndex(i, id int, seed int64, mul, add int) int {
	return (i*mul + id + add + int(seed%3)) & 3
}

//go:noinline
func leafFrame(ns []*node, id int, seed int64) (int32, int64) {
	var leaf [leafSlots]*node
	for i := 0; i < leafSlots; i++ {
		leaf[i] = ns[slotIndex(i, id, seed, 1, 2)]
	}
	var sum int64
	for i := 0; i < leafSlots; i++ {
		want := ns[slotIndex(i, id, seed, 1, 2)]
		if leaf[i] != want || leaf[i].v != want.v {
			return 1, 0
		}
		sum += leaf[i].v
	}
	return 0, sum
}

//go:noinline
func bigFrameStage(id int, seed int64, ready chan<- int, release <-chan struct{}) (int32, int64) {
	ns := buildChain(id, seed)
	var slots [bigSlots]*node
	var aux [auxSlots]*node
	for i := 0; i < bigSlots; i++ {
		slots[i] = ns[slotIndex(i, id, seed, 1, 0)]
	}
	for i := 0; i < auxSlots; i++ {
		aux[i] = ns[slotIndex(i, id, seed, 3, 1)]
	}
	verify := func() (int32, int64) {
		var sum int64
		for i := 0; i < bigSlots; i++ {
			want := ns[slotIndex(i, id, seed, 1, 0)]
			if slots[i] != want || slots[i].v != want.v {
				return 1, 0
			}
			sum += slots[i].v
		}
		for i := 0; i < auxSlots; i++ {
			want := ns[slotIndex(i, id, seed, 3, 1)]
			if aux[i] != want || aux[i].v != want.v {
				return 1, 0
			}
			sum += aux[i].v
		}
		return 0, sum
	}
	badPre, _ := verify()
	// 即使预校验失败也必须到达阻塞点，避免主协程等待 ready 死锁。
	ready <- id
	<-release
	badPost, sum := verify()
	if badPre != 0 || badPost != 0 {
		return 1, 0
	}
	badLeaf, leafSum := leafFrame(ns, id, seed)
	if badLeaf != 0 {
		return 1, 0
	}
	return 0, sum + leafSum
}

func expectedFrameSum(id int, seed int64) int64 {
	ns := buildChain(id, seed)
	var sum int64
	for i := 0; i < bigSlots; i++ {
		sum += ns[slotIndex(i, id, seed, 1, 0)].v
	}
	for i := 0; i < auxSlots; i++ {
		sum += ns[slotIndex(i, id, seed, 3, 1)].v
	}
	for i := 0; i < leafSlots; i++ {
		sum += ns[slotIndex(i, id, seed, 1, 2)].v
	}
	return sum
}

type frameResult struct {
	id   int
	bad  int32
	sum  int64
	want int64
}

func main() {
	seed := envSeed()
	workers := envInt("TORTURE_WORKERS", 48, 8, 128)

	ready := make(chan int, workers)
	release := make(chan struct{})
	out := make(chan frameResult, workers)
	var wg sync.WaitGroup
	wg.Add(workers)
	for id := 0; id < workers; id++ {
		go func(id int) {
			defer wg.Done()
			bad, sum := bigFrameStage(id, seed, ready, release)
			out <- frameResult{id: id, bad: bad, sum: sum, want: expectedFrameSum(id, seed)}
		}(id)
	}
	for i := 0; i < workers; i++ {
		<-ready
	}
	// 所有 goroutine 都停在 24KB+12KB 指针帧内的 channel 接收上。
	gcStress()
	close(release)
	wg.Wait()
	close(out)

	seen := make([]bool, workers)
	var total int64
	for r := range out {
		if r.id < 0 || r.id >= workers {
			ckf(false, "bad worker id %d", r.id)
			continue
		}
		seen[r.id] = true
		ckf(r.bad == 0, "worker %d big-frame slots corrupted across GC", r.id)
		ckf(r.sum == r.want, "worker %d sum=%d want=%d", r.id, r.sum, r.want)
		total += r.sum
	}
	for id, ok := range seen {
		ckf(ok, "worker %d missing result", id)
	}
	var wantTotal int64
	for id := 0; id < workers; id++ {
		wantTotal += expectedFrameSum(id, seed)
	}
	ckf(total == wantTotal, "total sum=%d want=%d", total, wantTotal)
	finish("l04_many_goroutines_big_frames")
}
