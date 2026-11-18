// L04: sync.Cond 广播波次：每波主协程在持锁状态下替换共享指针链并 Broadcast，
// 全部 waiter 必须从 Wait 返回、读到本波指针后再次入睡。每波广播前 waiter 全部
// 阻塞在 cond 上时 GC；结束后校验唤醒次数、波次顺序与旧链在 GC 后仍存活。
package main

import (
	"fmt"
	"os"
	"runtime"
	"strconv"
	"sync"
	"sync/atomic"
)

type cbNode struct {
	wave int
	v    int64
	next *cbNode
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

func runBroadcastWaves(seed int64, waiters, waves int) {
	var mu sync.Mutex
	cond := sync.NewCond(&mu)
	gen := 0
	var payload *cbNode

	started := make(chan int, waiters)
	done := make(chan int, waiters*waves)
	var wg sync.WaitGroup
	wg.Add(waiters)
	seen := make([][]*cbNode, waiters)
	wakeCount := make([]int, waiters)

	for w := 0; w < waiters; w++ {
		go func(w int) {
			defer wg.Done()
			mu.Lock()
			started <- w
			var mine []*cbNode
			wakes := 0
			for wave := 1; wave <= waves; wave++ {
				for gen < wave {
					wakes++
					cond.Wait()
				}
				p := payload
				if p == nil || p.wave != wave {
					atomic.AddInt32(&failed, 1)
					fmt.Printf("FAIL: waiter %d wave %d got wrong payload\n", w, wave)
				}
				mine = append(mine, p)
				done <- w
			}
			seen[w] = mine
			wakeCount[w] = wakes
			mu.Unlock()
		}(w)
	}

	for i := 0; i < waiters; i++ {
		<-started
	}
	for wave := 1; wave <= waves; wave++ {
		// waiter 在上一次 Broadcast 后重新入睡；广播前 GC。
		gcStress()
		mu.Lock()
		gen = wave
		v := seed*1000003 + int64(wave)*37
		payload = &cbNode{wave: wave, v: v, next: &cbNode{wave: wave, v: v + 1}}
		cond.Broadcast()
		mu.Unlock()
		for i := 0; i < waiters; i++ {
			<-done
		}
	}
	wg.Wait()

	ckf(gen == waves, "final gen=%d want=%d", gen, waves)
	all := make([]*cbNode, 0, waiters*waves)
	for w := 0; w < waiters; w++ {
		ckf(len(seen[w]) == waves, "waiter %d saw %d waves want=%d", w, len(seen[w]), waves)
		ckf(wakeCount[w] == waves, "waiter %d wake count=%d want=%d", w, wakeCount[w], waves)
		for i, p := range seen[w] {
			wave := i + 1
			if p == nil || p.wave != wave || p.next == nil || p.next.v != p.v+1 {
				ckf(false, "waiter %d wave %d recorded chain corrupted", w, wave)
				continue
			}
			all = append(all, p)
		}
	}
	gcStress()
	for i, p := range all {
		if p == nil || p.next == nil || p.next.v != p.v+1 {
			ckf(false, "recorded payload %d corrupted after wave GC", i)
		}
	}
	for w := 0; w < waiters; w++ {
		for i, p := range seen[w] {
			want := seed*1000003 + int64(i+1)*37
			if p == nil || p.v != want {
				ckf(false, "waiter %d wave %d v mismatch after GC", w, i+1)
			}
		}
	}
	finish("l04_cond_broadcast_waves")
}

func main() {
	seed := envSeed()
	waiters := envInt("TORTURE_WAITERS", 8, 2, 24)
	waves := envInt("TORTURE_ROUNDS", 5, 1, 16)
	runBroadcastWaves(seed, waiters, waves)
}
