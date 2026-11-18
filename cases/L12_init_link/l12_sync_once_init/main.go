// L12: sync.Once 延迟初始化全局表；并发 + GC 下只构建一次。
package main

import (
	"fmt"
	"os"
	"runtime"
	"strconv"
	"sync"
	"sync/atomic"
)

var (
	failed  int32
	gcSink  [][]byte
	seedVal int64
	seedStr string
)

func ck(ok bool, msg string) {
	if !ok {
		atomic.AddInt32(&failed, 1)
		fmt.Println("FAIL:", msg)
	}
}

func gcStress() {
	for i := 0; i < 16; i++ {
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
		fmt.Printf("FAILED %s (%d failed checks) seed=%s\n", name, n, seedStr)
		os.Exit(1)
	}
	fmt.Println("ALL PASS " + name)
}

func readSeed() int64 {
	s := os.Getenv("UAF_SEED")
	if s == "" {
		return 1
	}
	v, err := strconv.ParseInt(s, 10, 64)
	if err != nil || v < 0 {
		return 1
	}
	return v
}

func tortureRounds() int {
	s := os.Getenv("TORTURE_ROUNDS")
	if s == "" {
		return 1
	}
	v, err := strconv.Atoi(s)
	if err != nil || v < 1 {
		return 1
	}
	if v > 16 {
		v = 16
	}
	return v
}

func checkTable(t *lookupTable, n int) {
	if t == nil {
		ck(false, "table nil")
		return
	}
	ck(len(t.m) == n, "table map size")
	ck(len(t.keys) == n, "table key slice size")
	for i := 0; i < n; i++ {
		k := keyOf(i)
		e := t.m[k]
		if e == nil {
			ck(false, "missing entry "+k)
			return
		}
		if e.key != k || e.val != int64(i)*3+7 {
			ck(false, "entry value "+k)
			return
		}
		if len(e.buf) != 24 || e.buf[0] != byte(i) || e.buf[23] != byte(i+23) {
			ck(false, "entry buffer "+k)
			return
		}
	}
	seen := 0
	for e := t.head; e != nil; e = e.next {
		seen++
		if seen > n+1 {
			ck(false, "table chain cycle")
			return
		}
	}
	ck(seen == n, "table chain length")
}

func main() {
	seedVal = readSeed()
	seedStr = strconv.FormatInt(seedVal, 10)
	rounds := tortureRounds()
	n := tableSize()

	const workers = 16
	iters := 256 * rounds
	if iters > 8192 {
		iters = 8192
	}

	ptrs := make([]*lookupTable, workers)
	var wg sync.WaitGroup
	var bad int32
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			t := ensureTable()
			ptrs[w] = t
			if t == nil {
				atomic.AddInt32(&bad, 1)
				return
			}
			start := int((seedVal + int64(w)) % int64(n))
			for i := 0; i < iters; i++ {
				cur := ensureTable()
				if cur != t {
					atomic.AddInt32(&bad, 1)
					return
				}
				k := keyOf((start + i) % n)
				e := t.m[k]
				if e == nil || e.key != k || e.val != int64((start+i)%n)*3+7 {
					atomic.AddInt32(&bad, 1)
					return
				}
				if i%64 == 0 {
					b := make([]byte, 1024)
					b[0] = byte(i)
					_ = b
				}
			}
		}(w)
	}

	// 并发构建期间周期性 GC。
	for i := 0; i < 8; i++ {
		for j := 0; j < 8; j++ {
			b := make([]byte, 8192)
			b[j] = byte(i)
			gcSink = append(gcSink[:0], b)
		}
		runtime.GC()
	}
	wg.Wait()
	ck(atomic.LoadInt32(&bad) == 0, "concurrent ensureTable/lookup")
	ck(atomic.LoadInt32(&tableInitCount) == 1, "table built exactly once")

	t0 := ensureTable()
	for w := 0; w < workers; w++ {
		ck(ptrs[w] == t0, "all goroutines observed the same table pointer")
	}
	ck(atomic.LoadInt32(&tableInitCount) == 1, "still one build after concurrent phase")
	checkTable(t0, n)

	gcStress()
	ck(ensureTable() == t0, "same table pointer after GC")
	ck(atomic.LoadInt32(&tableInitCount) == 1, "no rebuild after GC")
	checkTable(t0, n)

	top := t0.head
	ck(top != nil && top.key == keyOf(n-1) && top.val == int64(n-1)*3+7, "global table head after GC")

	finish("l12_sync_once_init")
}
