// L04: 反复创建/汇合大量 goroutine，每个 goroutine 持有独立指针链；
// 所有 goroutine 在同步点阻塞时触发 GC，汇合后校验指针内容与计数不丢失。
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

func chainLen(id int, seed int64) int {
	return 3 + (id*7+int(seed%5))%5
}

func chainBase(id int, seed int64) int64 {
	return int64(id)*1000 + seed*13
}

func buildChain(id int, seed int64) *node {
	n := chainLen(id, seed)
	base := chainBase(id, seed)
	var head *node
	for j := n - 1; j >= 0; j-- {
		head = &node{v: base + int64(j)*7, next: head}
	}
	return head
}

func walkChain(head *node) (int64, int, bool) {
	var sum int64
	n := 0
	for p := head; p != nil; p = p.next {
		sum += p.v
		n++
		if p.next != nil && p.next.v != p.v+7 {
			return 0, 0, false
		}
	}
	return sum, n, true
}

func expectedChain(id int, seed int64) (int64, int) {
	n := chainLen(id, seed)
	base := chainBase(id, seed)
	var sum int64
	for j := 0; j < n; j++ {
		sum += base + int64(j)*7
	}
	return sum, n
}

type churnResult struct {
	id  int
	sum int64
	n   int
	ok  bool
}

func churnBatch(workers int, seed int64) {
	ready := make(chan struct{}, workers)
	release := make(chan struct{})
	results := make(chan churnResult, workers)
	var wg sync.WaitGroup
	wg.Add(workers)
	for id := 0; id < workers; id++ {
		go func(id int) {
			defer wg.Done()
			head := buildChain(id, seed)
			sum0, n0, ok0 := walkChain(head)
			// 通知主协程：本 goroutine 已持有指针链并即将阻塞。
			ready <- struct{}{}
			<-release
			sum1, n1, ok1 := walkChain(head)
			results <- churnResult{id: id, sum: sum1, n: n1, ok: ok0 && ok1 && sum0 == sum1 && n0 == n1}
			runtime.KeepAlive(head)
		}(id)
	}
	for i := 0; i < workers; i++ {
		<-ready
	}
	// 全部 goroutine 阻塞在 release 上，指针链只存在于各自栈/寄存器中。
	gcStress()
	close(release)
	wg.Wait()
	close(results)

	seen := make([]bool, workers)
	for r := range results {
		if r.id < 0 || r.id >= workers {
			ckf(false, "bad worker id %d", r.id)
			continue
		}
		seen[r.id] = true
		expSum, expN := expectedChain(r.id, seed)
		ckf(r.ok, "worker %d chain changed across GC", r.id)
		ckf(r.n == expN && r.sum == expSum, "worker %d chain mismatch got(n=%d,sum=%d) want(n=%d,sum=%d)", r.id, r.n, r.sum, expN, expSum)
	}
	for id, ok := range seen {
		ckf(ok, "worker %d missing result", id)
	}
}

func main() {
	seed := envSeed()
	rounds := envInt("TORTURE_ROUNDS", 6, 1, 50)
	workers := envInt("TORTURE_WORKERS", 96, 8, 512)
	for r := 0; r < rounds; r++ {
		churnBatch(workers, seed+int64(r)*101)
		gcStress()
	}
	ckf(workers > 0 && rounds > 0, "empty configuration")
	finish("l04_goroutine_churn")
}
