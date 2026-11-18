// L04: goroutine 创建风暴并立即退出：每轮一次性拉起大量短生命周期 goroutine，
// 每个 goroutine 在私有栈上构造指针链、固定让出若干次后立刻退出；
// 风暴期间与结束后显式 GC，校验完成计数、唯一性与指针链完整性。
package main

import (
	"fmt"
	"os"
	"runtime"
	"strconv"
	"sync"
	"sync/atomic"
)

type snode struct {
	id   int
	v    int64
	next *snode
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

func runStorm(round, size int, seed int64) {
	base := int64(round)*1000003 + seed*131
	keep := make([]*snode, size)
	var wg sync.WaitGroup
	var done, bad int32
	var sum int64

	wg.Add(size)
	for i := 0; i < size; i++ {
		go func(i int) {
			defer wg.Done()
			b := base + int64(i)*17
			head := &snode{id: i, v: b, next: &snode{id: i, v: b + 1, next: &snode{id: i, v: b + 2}}}
			// 立即退出路径：只有固定次数的让出与栈上指针复验，不等待任何协调点。
			s := int64(0)
			for k := 0; k < 8; k++ {
				runtime.Gosched()
				if head.next == nil || head.next.next == nil || head.v != b {
					atomic.AddInt32(&bad, 1)
					return
				}
				s += head.v + head.next.v + head.next.next.v
			}
			keep[i] = head
			atomic.AddInt64(&sum, s)
			atomic.AddInt32(&done, 1)
		}(i)
	}

	// 创建风暴仍在进行时触发 GC：新栈与新堆链同时被扫描。
	gcStress()
	wg.Wait()

	ckf(atomic.LoadInt32(&done) == int32(size), "round %d done=%d want=%d", round, done, size)
	ckf(atomic.LoadInt32(&bad) == 0, "round %d chain corrupted during storm", round)

	var want int64
	for i := 0; i < size; i++ {
		b := base + int64(i)*17
		want += 8 * (3*b + 3)
	}
	ckf(atomic.LoadInt64(&sum) == want, "round %d sum=%d want=%d", round, sum, want)

	gcStress()
	for i := 0; i < size; i++ {
		h := keep[i]
		if h == nil || h.next == nil || h.next.next == nil {
			ckf(false, "round %d item %d missing after storm GC", round, i)
			continue
		}
		b := base + int64(i)*17
		ckf(h.id == i && h.v == b && h.next.v == b+1 && h.next.next.v == b+2,
			"round %d item %d corrupted after storm GC", round, i)
	}
}

func main() {
	seed := envSeed()
	rounds := envInt("TORTURE_ROUNDS", 4, 1, 16)
	size := envInt("TORTURE_SPAWN", 512, 8, 4096)
	for r := 0; r < rounds; r++ {
		runStorm(r, size, seed)
	}
	finish("l04_spawn_storm_exit")
}
