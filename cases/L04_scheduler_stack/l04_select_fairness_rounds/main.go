// L04: select 调度公平性：每个 worker 在两条始终就绪的 channel 上做固定次数
// select，第三条永不就绪的 channel 必须永不被选中。中途所有 worker 停在屏障上
// 时 GC，之后继续；断言两个就绪分支都获得足够份额、无丢失 token、链指针完整。
package main

import (
	"fmt"
	"os"
	"runtime"
	"strconv"
	"sync"
	"sync/atomic"
)

type sfNode struct {
	v    int64
	next *sfNode
}

type sfResult struct {
	id     int
	ca     int
	cb     int
	never  int
	bad    int
	tokens []*sfNode
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

func runSelectFairness(seed int64, workers, iters int) {
	half := make(chan int, workers)
	goAhead := make(chan struct{})
	results := make(chan sfResult, workers)
	var wg sync.WaitGroup
	wg.Add(workers)

	for w := 0; w < workers; w++ {
		go func(w int) {
			defer wg.Done()
			base := seed*100003 + int64(w)*100003
			a := make(chan *sfNode, iters)
			b := make(chan *sfNode, iters)
			never := make(chan *sfNode)
			for k := 0; k < iters; k++ {
				va := base + int64(2*k)
				vb := base + int64(2*k+1)
				a <- &sfNode{v: va, next: &sfNode{v: va + 1}}
				b <- &sfNode{v: vb, next: &sfNode{v: vb + 1}}
			}
			res := sfResult{id: w, tokens: make([]*sfNode, 0, iters)}
			for k := 0; k < iters; k++ {
				select {
				case t := <-a:
					res.ca++
					res.tokens = append(res.tokens, t)
				case t := <-b:
					res.cb++
					res.tokens = append(res.tokens, t)
				case <-never:
					res.never++
				}
				if k+1 == iters/2 {
					half <- w
					<-goAhead
				}
			}
			results <- res
		}(w)
	}

	for i := 0; i < workers; i++ {
		<-half
	}
	// 每个 worker 都停在中点屏障、已完成 token 一半在 channel 一半在栈/切片上时 GC。
	gcStress()
	close(goAhead)
	wg.Wait()
	close(results)

	all := make([]*sfNode, 0, workers*iters)
	for res := range results {
		ckf(res.never == 0, "worker %d selected never-ready case %d times", res.id, res.never)
		ckf(res.ca+res.cb == iters, "worker %d selected=%d want=%d", res.id, res.ca+res.cb, iters)
		ckf(res.ca > 0 && res.cb > 0, "worker %d starved one branch (a=%d b=%d)", res.id, res.ca, res.cb)
		ckf(res.ca >= iters/8 && res.cb >= iters/8, "worker %d branch skew a=%d b=%d", res.id, res.ca, res.cb)
		ckf(len(res.tokens) == iters, "worker %d tokens=%d want=%d", res.id, len(res.tokens), iters)
		base := seed*100003 + int64(res.id)*100003
		seen := make([]bool, 2*iters)
		for i, t := range res.tokens {
			if t == nil || t.next == nil {
				ckf(false, "worker %d token %d missing", res.id, i)
				continue
			}
			off := t.v - base
			if off < 0 || off >= int64(2*iters) {
				ckf(false, "worker %d token %d value=%d out of range", res.id, i, t.v)
				continue
			}
			if seen[off] {
				ckf(false, "worker %d token %d duplicated", res.id, i)
			}
			seen[off] = true
			if t.next.v != t.v+1 {
				ckf(false, "worker %d token %d chain corrupted", res.id, i)
			}
			all = append(all, t)
		}
	}
	gcStress()
	for i, t := range all {
		if t == nil || t.next == nil || t.next.v != t.v+1 {
			ckf(false, "token %d corrupted after final GC", i)
		}
	}
	finish("l04_select_fairness_rounds")
}

func main() {
	seed := envSeed()
	workers := envInt("TORTURE_WORKERS", 6, 1, 16)
	iters := envInt("TORTURE_ITERS", 2048, 256, 16384)
	runSelectFairness(seed, workers, iters)
}
