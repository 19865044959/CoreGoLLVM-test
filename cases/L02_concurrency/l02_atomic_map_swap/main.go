// L02: atomic 计数 + map 交换：worker 只做原子计数，单写者周期性构建不可变
// map 快照并用 atomic.Pointer 交换；读者校验快照自洽、代数单调、值不超过终值。
package main

import (
	"fmt"
	"os"
	"runtime"
	"strconv"
	"sync"
	"sync/atomic"
)

const (
	nKeys        = 16
	nWorkers     = 6
	nReaders     = 4
	baseOps      = 300
	baseSwaps    = 48
	baseReadLoop = 200
)

var (
	failed   int32
	gcMu     sync.Mutex
	gcSink   [][]byte
	curSeed  int64
	counters [nKeys]atomic.Int64
	totalCtr atomic.Int64
	current  atomic.Pointer[snap]
)

type snap struct {
	gen    int64
	counts map[int]int64
	sum    int64
}

func ck(ok bool, msg string) {
	if !ok {
		atomic.AddInt32(&failed, 1)
		fmt.Println("FAIL:", msg)
	}
}

func ckSeed(ok bool, msg string) {
	if !ok {
		atomic.AddInt32(&failed, 1)
		fmt.Printf("FAIL: %s (seed=%d)\n", msg, curSeed)
	}
}

func gcStress() {
	gcMu.Lock()
	for i := 0; i < 8; i++ {
		b := make([]byte, 2048)
		b[0] = byte(i)
		gcSink = append(gcSink[:0], b)
	}
	gcSink = gcSink[:0]
	gcMu.Unlock()
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

func tortureRounds(def int) int {
	s := os.Getenv("TORTURE_ROUNDS")
	if s == "" {
		return def
	}
	n, err := strconv.Atoi(s)
	if err != nil || n <= 0 {
		return def
	}
	if n > 50 {
		n = 50
	}
	return n
}

func buildSnap(gen int64) *snap {
	m := make(map[int]int64, nKeys)
	var sum int64
	for k := 0; k < nKeys; k++ {
		v := counters[k].Load()
		m[k] = v
		sum += v
	}
	return &snap{gen: gen, counts: m, sum: sum}
}

func snapOK(s *snap, maxKey int64) bool {
	if s == nil || len(s.counts) != nKeys {
		return false
	}
	var sum int64
	for k := 0; k < nKeys; k++ {
		v, ok := s.counts[k]
		if !ok || v < 0 || v > maxKey {
			return false
		}
		sum += v
	}
	return sum == s.sum
}

func builder(swaps int, maxKey int64, start <-chan struct{}, wg *sync.WaitGroup) {
	defer wg.Done()
	<-start
	for s := 0; s < swaps; s++ {
		n := buildSnap(int64(s + 1))
		current.Store(n)
		if s%8 == 0 {
			gcStress()
			if !snapOK(n, maxKey) {
				ckSeed(false, "builder produced inconsistent snapshot")
			}
		}
	}
}

func worker(id, ops, off int, start <-chan struct{}, wg *sync.WaitGroup) {
	defer wg.Done()
	<-start
	for i := 0; i < ops; i++ {
		k := (id*5 + i*3 + off) % nKeys
		counters[k].Add(1)
		totalCtr.Add(1)
		if i%47 == 0 {
			gcStress()
		}
	}
}

func reader(id, loops int, maxKey int64, start <-chan struct{}, wg *sync.WaitGroup) {
	defer wg.Done()
	<-start
	var lastGen int64
	for i := 0; i < loops; i++ {
		s := current.Load()
		if s == nil {
			ck(false, "reader observed nil snapshot")
			continue
		}
		if s.gen < lastGen {
			ckSeed(false, "snapshot generation regressed")
		}
		lastGen = s.gen
		if !snapOK(s, maxKey) {
			ckSeed(false, "reader observed inconsistent snapshot")
		}
		if i%29 == 0 {
			gcStress()
			if !snapOK(s, maxKey) {
				ckSeed(false, "snapshot corrupted after GC")
			}
		}
	}
}

func main() {
	curSeed = envSeed()
	rounds := tortureRounds(1)
	ops := baseOps * rounds
	swaps := baseSwaps * rounds
	readLoops := baseReadLoop * rounds
	off := int(uint64(curSeed) % nKeys)

	var want [nKeys]int64
	for w := 0; w < nWorkers; w++ {
		for i := 0; i < ops; i++ {
			want[(w*5+i*3+off)%nKeys]++
		}
	}
	var maxKey, wantSum int64
	for k := 0; k < nKeys; k++ {
		if want[k] > maxKey {
			maxKey = want[k]
		}
		wantSum += want[k]
	}

	current.Store(buildSnap(0))
	start := make(chan struct{})
	var bw, ww, rw sync.WaitGroup
	bw.Add(1)
	go builder(swaps, maxKey, start, &bw)
	for w := 0; w < nWorkers; w++ {
		ww.Add(1)
		go worker(w, ops, off, start, &ww)
	}
	for r := 0; r < nReaders; r++ {
		rw.Add(1)
		go reader(r, readLoops, maxKey, start, &rw)
	}
	close(start)
	ww.Wait()
	rw.Wait()
	bw.Wait()

	final := buildSnap(int64(swaps + 1))
	current.Store(final)
	ckSeed(totalCtr.Load() == wantSum,
		fmt.Sprintf("total counter got=%d want=%d", totalCtr.Load(), wantSum))
	bad := 0
	for k := 0; k < nKeys; k++ {
		if got := counters[k].Load(); got != want[k] {
			bad++
			if bad <= 4 {
				ckSeed(false, fmt.Sprintf("counter %d got=%d want=%d", k, got, want[k]))
			}
		}
		if got := final.counts[k]; got != want[k] {
			bad++
			if bad <= 8 {
				ckSeed(false, fmt.Sprintf("final snapshot key %d got=%d want=%d", k, got, want[k]))
			}
		}
	}
	ckSeed(bad == 0, fmt.Sprintf("final mismatches=%d", bad))
	ckSeed(final.sum == wantSum, fmt.Sprintf("final snapshot sum got=%d want=%d", final.sum, wantSum))
	gcStress()
	loaded := current.Load()
	ckSeed(loaded != nil && loaded.gen == int64(swaps+1), "final snapshot generation mismatch")
	ckSeed(snapOK(loaded, maxKey), "final snapshot invalid after GC")
	ckSeed(loaded.sum == wantSum, "final snapshot sum changed after GC")

	fmt.Printf("workers=%d readers=%d keys=%d swaps=%d increments=%d final_gen=%d\n",
		nWorkers, nReaders, nKeys, swaps, wantSum, swaps+1)
	finish("l02_atomic_map_swap")
}
