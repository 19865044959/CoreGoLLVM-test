// L04: 多 goroutine 自旋并每轮 Gosched；主协程在所有自旋者就绪后 GC 再置位
// epoch，验证自旋者不会饿死且栈上指针链在反复让出/抢占后仍完整。
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

type spinResult struct {
	id    int
	bad   int32
	spins int64
}

func main() {
	seed := envSeed()
	workers := envInt("TORTURE_WORKERS", 8, 2, 32)
	const maxSpins = 1 << 24

	var epoch int32
	ready := make(chan int, workers)
	out := make(chan spinResult, workers)
	var wg sync.WaitGroup
	wg.Add(workers)
	for id := 0; id < workers; id++ {
		go func(id int) {
			defer wg.Done()
			base := int64(id)*1000 + seed*53
			head := &node{v: base, next: &node{v: base + 7, next: &node{v: base + 14}}}
			want := base*3 + 21
			ready <- id
			var spins int64
			var bad int32
			for {
				if atomic.LoadInt32(&epoch) != 0 {
					break
				}
				runtime.Gosched()
				spins++
				if spins >= maxSpins {
					bad = 1
					break
				}
			}
			var sum int64
			n := 0
			for p := head; p != nil; p = p.next {
				sum += p.v
				n++
			}
			if n != 3 || sum != want {
				bad = 1
			}
			out <- spinResult{id: id, bad: bad, spins: spins}
		}(id)
	}
	for r := 0; r < workers; r++ {
		<-ready
	}
	// 所有 worker 都在热点自旋且栈上持有指针链，此时 GC。
	gcStress()
	atomic.StoreInt32(&epoch, 1)
	wg.Wait()
	close(out)

	seen := make([]bool, workers)
	var totalSpins int64
	for r := range out {
		if r.id < 0 || r.id >= workers {
			ckf(false, "bad worker id %d", r.id)
			continue
		}
		seen[r.id] = true
		ckf(r.bad == 0, "worker %d starved or chain corrupted (spins=%d)", r.id, r.spins)
		totalSpins += r.spins
	}
	for id, ok := range seen {
		ckf(ok, "worker %d missing result", id)
	}
	ckf(totalSpins > 0, "workers never executed the spin loop")
	finish("l04_gosched_spin")
}
