// L04: 协程阻塞在 sync.Mutex / RWMutex 的排他队列上时主协程 GC；
// 解锁唤醒后校验各 goroutine 私有指针链与共享只读链仍完整。
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

// yieldN 给已就绪但尚未进入 Lock 的 goroutine 固定次数的调度机会。
func yieldN(n int) {
	for i := 0; i < n; i++ {
		runtime.Gosched()
	}
}

type mutexResult struct {
	id  int
	bad int32
	sum int64
}

func main() {
	seed := envSeed()
	contenders := envInt("TORTURE_WORKERS", 16, 2, 64)
	readers := envInt("TORTURE_READERS", 8, 2, 64)

	var mu sync.Mutex
	mu.Lock() // 主协程持有锁，contender 全部阻塞在 Lock。
	ready := make(chan int, contenders)
	out := make(chan mutexResult, contenders)
	var wg sync.WaitGroup
	wg.Add(contenders)
	for id := 0; id < contenders; id++ {
		go func(id int) {
			defer wg.Done()
			base := int64(id)*100 + seed*13
			head := &node{v: base, next: &node{v: base + 1, next: &node{v: base + 2}}}
			want := base*3 + 3
			ready <- id
			mu.Lock()
			var sum int64
			var bad int32
			for p := head; p != nil; p = p.next {
				sum += p.v
			}
			if sum != want {
				bad = 1
			}
			for i := 0; i < 16; i++ {
				runtime.Gosched()
			}
			mu.Unlock()
			var sum2 int64
			for p := head; p != nil; p = p.next {
				sum2 += p.v
			}
			if sum2 != want {
				bad = 1
			}
			out <- mutexResult{id: id, bad: bad, sum: sum2}
		}(id)
	}
	for i := 0; i < contenders; i++ {
		<-ready
	}
	yieldN(2000)
	gcStress()
	mu.Unlock()
	wg.Wait()
	close(out)

	seen := make([]bool, contenders)
	var total, wantTotal int64
	for r := range out {
		if r.id < 0 || r.id >= contenders {
			ckf(false, "bad contender id %d", r.id)
			continue
		}
		seen[r.id] = true
		base := int64(r.id)*100 + seed*13
		want := base*3 + 3
		ckf(r.bad == 0, "contender %d chain corrupted around mutex/GC", r.id)
		ckf(r.sum == want, "contender %d sum=%d want=%d", r.id, r.sum, want)
		total += r.sum
		wantTotal += want
	}
	for id, ok := range seen {
		ckf(ok, "contender %d missing result", id)
	}
	ckf(total == wantTotal, "mutex total sum=%d want=%d", total, wantTotal)

	// RWMutex：写锁阻塞读者，GC 发生在读者排队等待时。
	sharedBase := seed*5 + 1
	shared := &node{v: sharedBase, next: &node{v: sharedBase + 2, next: &node{v: sharedBase + 4}}}
	sharedWant := sharedBase*3 + 6
	var rw sync.RWMutex
	rw.Lock()
	readReady := make(chan int, readers)
	readOut := make(chan int64, readers)
	var rwg sync.WaitGroup
	rwg.Add(readers)
	for id := 0; id < readers; id++ {
		go func(id int) {
			defer rwg.Done()
			readReady <- id
			rw.RLock()
			var sum int64
			for p := shared; p != nil; p = p.next {
				sum += p.v
			}
			runtime.Gosched()
			rw.RUnlock()
			readOut <- sum
		}(id)
	}
	for i := 0; i < readers; i++ {
		<-readReady
	}
	yieldN(2000)
	gcStress()
	rw.Unlock()
	rwg.Wait()
	close(readOut)

	gotReaders := 0
	for sum := range readOut {
		gotReaders++
		ckf(sum == sharedWant, "reader sum=%d want=%d", sum, sharedWant)
	}
	ckf(gotReaders == readers, "readers=%d want=%d", gotReaders, readers)
	// 关闭写锁后再 GC，确认共享只读链仍在。
	gcStress()
	var sharedSum int64
	for p := shared; p != nil; p = p.next {
		sharedSum += p.v
	}
	ckf(sharedSum == sharedWant, "shared chain after GC sum=%d want=%d", sharedSum, sharedWant)
	finish("l04_mutex_blocked_gc")
}
