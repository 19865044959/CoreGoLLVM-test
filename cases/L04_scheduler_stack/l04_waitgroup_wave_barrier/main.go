// L04: WaitGroup 波次屏障：每波使用全新的 WaitGroup 拉起 N 个 worker，
// Wait 返回后才进入下一波。worker 在波内屏障处阻塞时 GC；共享 wave 标记与
// active 计数用于检测跨波重叠，结果按波次校验唯一性与指针链完整性。
package main

import (
	"fmt"
	"os"
	"runtime"
	"strconv"
	"sync"
	"sync/atomic"
)

type wvNode struct {
	v    int64
	next *wvNode
}

var (
	failed int32
	gcSink [][]byte
	wvAct  int32
	wvMax  int32
	wvWave int32
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

func bumpWvMax(v int32) {
	for {
		m := atomic.LoadInt32(&wvMax)
		if v <= m || atomic.CompareAndSwapInt32(&wvMax, m, v) {
			return
		}
	}
}

func runWaves(seed int64, waves, per int) {
	var keep []*wvNode
	var totalSum, wantSum int64

	for w := 1; w <= waves; w++ {
		atomic.StoreInt32(&wvWave, int32(w))
		gate := make(chan struct{})
		ready := make(chan int, per)
		var wg sync.WaitGroup
		wg.Add(per)
		heads := make([]*wvNode, per)
		sums := make([]int64, per)
		counts := make([]int32, per)
		var waveBad int32

		for id := 0; id < per; id++ {
			go func(id int) {
				defer wg.Done()
				if atomic.LoadInt32(&wvWave) != int32(w) {
					atomic.AddInt32(&waveBad, 1)
				}
				b := seed*100003 + int64(w)*1000 + int64(id)*7
				h := &wvNode{v: b, next: &wvNode{v: b + 1}}
				heads[id] = h
				bumpWvMax(atomic.AddInt32(&wvAct, 1))
				ready <- id
				<-gate
				if atomic.LoadInt32(&wvWave) != int32(w) {
					atomic.AddInt32(&waveBad, 1)
				}
				if h == nil || h.next == nil || h.next.v != b+1 {
					atomic.AddInt32(&waveBad, 1)
				}
				sums[id] = h.v + h.next.v
				counts[id]++
				atomic.AddInt32(&wvAct, -1)
			}(id)
		}

		for i := 0; i < per; i++ {
			<-ready
		}
		ckf(atomic.LoadInt32(&wvAct) == int32(per), "wave %d active=%d want=%d", w, wvAct, per)
		gcStress()
		close(gate)
		wg.Wait()
		ckf(atomic.LoadInt32(&wvAct) == 0, "wave %d active after wait=%d want=0", w, wvAct)

		for id := 0; id < per; id++ {
			b := seed*100003 + int64(w)*1000 + int64(id)*7
			ckf(heads[id] != nil && heads[id].v == b && heads[id].next != nil && heads[id].next.v == b+1,
				"wave %d worker %d chain corrupted", w, id)
			ckf(sums[id] == 2*b+1, "wave %d worker %d sum=%d want=%d", w, id, sums[id], 2*b+1)
			ckf(counts[id] == 1, "wave %d worker %d ran %d times", w, id, counts[id])
			totalSum += sums[id]
			wantSum += 2*b + 1
			keep = append(keep, heads[id])
		}
		ckf(atomic.LoadInt32(&waveBad) == 0, "wave %d workers observed overlapping wave marker", w)
		// 波与波之间：结果已保留，worker 已退出；GC 后进入下一波。
		gcStress()
	}

	ckf(atomic.LoadInt32(&wvWave) == int32(waves), "final wave marker=%d want=%d", wvWave, waves)
	ckf(atomic.LoadInt32(&wvMax) == int32(per), "max active=%d want=%d", wvMax, per)
	ckf(totalSum == wantSum, "total=%d want=%d", totalSum, wantSum)
	for i, n := range keep {
		if n == nil || n.next == nil {
			ckf(false, "keep[%d] chain missing after wave GC", i)
		}
	}
	finish("l04_waitgroup_wave_barrier")
}

func main() {
	seed := envSeed()
	waves := envInt("TORTURE_ROUNDS", 5, 1, 16)
	per := envInt("TORTURE_WORKERS", 8, 2, 24)
	runWaves(seed, waves, per)
}
