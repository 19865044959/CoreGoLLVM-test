// L04: WaitGroup 等待全部生产者后关闭 channel，消费者 range 排空；
// 消费者排空一半时阻塞，主协程 GC 后再放行，最后校验关闭后仍存活的指针。
package main

import (
	"fmt"
	"os"
	"runtime"
	"strconv"
	"sync"
	"sync/atomic"
)

type item struct {
	id   int
	v    int64
	next *item
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

func runRound(r int, seed int64, producers, consumers, perProd int) {
	total := producers * perProd
	half := total / 2
	jobs := make(chan *item, 8)
	release := make(chan struct{})
	halfDone := make(chan struct{})
	closerDone := make(chan struct{})
	all := make([]*item, total)

	var prodWG, consWG sync.WaitGroup
	prodWG.Add(producers)
	consWG.Add(consumers)

	for p := 0; p < producers; p++ {
		go func(p int) {
			defer prodWG.Done()
			for j := 0; j < perProd; j++ {
				id := p*perProd + j
				base := int64(id)*100 + seed*7 + int64(r)*1000003
				jobs <- &item{id: id, v: base, next: &item{v: base + 5}}
			}
		}(p)
	}
	go func() {
		prodWG.Wait()
		close(jobs)
		close(closerDone)
	}()

	var mu sync.Mutex
	consumed := 0
	var bad int32
	for c := 0; c < consumers; c++ {
		go func() {
			defer consWG.Done()
			for head := range jobs {
				mu.Lock()
				firstHalf := consumed < half
				if firstHalf {
					consumed++
					if consumed == half {
						close(halfDone)
					}
				}
				mu.Unlock()
				if !firstHalf {
					// 后半段先停在 release 上，主协程此时 GC。
					<-release
				}
				if head == nil || head.next == nil || head.next.v != head.v+5 {
					atomic.AddInt32(&bad, 1)
					continue
				}
				all[head.id] = head
			}
		}()
	}

	<-halfDone
	gcStress()
	close(release)
	<-closerDone
	consWG.Wait()
	gcStress()

	ckf(atomic.LoadInt32(&bad) == 0, "round %d chain corrupted", r)
	var sum, wantSum int64
	for id := 0; id < total; id++ {
		h := all[id]
		if h == nil || h.next == nil {
			ckf(false, "round %d item %d missing after close+GC", r, id)
			continue
		}
		if h.id != id || h.next.v != h.v+5 {
			ckf(false, "round %d item %d corrupted after close+GC", r, id)
		}
		sum += h.v + h.next.v
		base := int64(id)*100 + seed*7 + int64(r)*1000003
		wantSum += 2*base + 5
	}
	ckf(sum == wantSum, "round %d sum=%d want=%d", r, sum, wantSum)
}

func main() {
	seed := envSeed()
	rounds := envInt("TORTURE_ROUNDS", 2, 1, 20)
	items := envInt("TORTURE_ITEMS", 48, 4, 512)
	const producers = 6
	const consumers = 4
	for r := 0; r < rounds; r++ {
		runRound(r, seed, producers, consumers, items)
		gcStress()
	}
	finish("l04_waitgroup_close")
}
