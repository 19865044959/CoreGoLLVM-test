// L12: multiple package-level sync.Once values. Covers independent Once
// instances, a dynamic per-slot Once table, concurrent first use, panic
// semantics (a panicking f still marks the Once done), and GC between uses.
// P2: TORTURE_ROUNDS scales the slot table and iteration count.
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
	if v > 64 {
		v = 64
	}
	return v
}

func checkTables(ta map[string]*cell, tb []*cell) {
	ck(len(ta) == 16 && len(tb) == 16, "both Once-built tables have 16 entries")
	for i := 0; i < 16; i++ {
		a := ta[keyOf(i)]
		b := tb[i]
		if a == nil || b == nil {
			ck(false, "Once table entry missing")
			return
		}
		if a.key != keyOf(i) || a.v != int64(i)*11+5 || b.key != keyOf(i) || b.v != int64(i)*11+5 {
			ck(false, "Once table entry values")
			return
		}
		if a.buf == nil || b.buf == nil || a.buf[31] != byte(i+31) || b.buf[31] != byte(i+31) {
			ck(false, "Once table payload")
			return
		}
	}
}

func main() {
	seedVal = readSeed()
	seedStr = strconv.FormatInt(seedVal, 10)
	rounds := tortureRounds()

	nslots := 8 * rounds
	initSlotTable(nslots)
	iters := 256 * rounds
	if iters > 8192 {
		iters = 8192
	}

	const workers = 32
	ptrsA := make([]map[string]*cell, workers)
	ptrsB := make([][]*cell, workers)
	var wg sync.WaitGroup
	var bad int32
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			ta := ensureA()
			tb := ensureB()
			ptrsA[w] = ta
			ptrsB[w] = tb
			if ta == nil || tb == nil {
				atomic.AddInt32(&bad, 1)
				return
			}
			start := int((seedVal + int64(w)*7) % int64(nslots))
			for i := 0; i < iters; i++ {
				slot := (start + i) % nslots
				v := ensureSlot(slot)
				if v != int64(slot)*7+3 {
					atomic.AddInt32(&bad, 1)
					return
				}
				key := keyOf((start + i) % 16)
				c := ta[key]
				if c == nil || c.v != int64((start+i)%16)*11+5 {
					atomic.AddInt32(&bad, 1)
					return
				}
			}
		}(w)
	}

	// Run GC while the goroutines are still doing first-use initialization.
	for i := 0; i < 8; i++ {
		for j := 0; j < 8; j++ {
			b := make([]byte, 8192)
			b[j] = byte(i)
			gcSink = append(gcSink[:0], b)
		}
		runtime.GC()
	}
	wg.Wait()
	gcSink = gcSink[:0]
	ck(atomic.LoadInt32(&bad) == 0, "concurrent Once first use")
	ck(atomic.LoadInt32(&buildA) == 1 && atomic.LoadInt32(&buildB) == 1,
		"global Once builders ran exactly once")

	a0 := ensureA()
	b0 := ensureB()
	for w := 0; w < workers; w++ {
		if len(ptrsA[w]) != 16 || len(ptrsB[w]) != 16 {
			ck(false, "worker observed a missing Once table")
			continue
		}
		ck(ptrsA[w][keyOf(0)] == a0[keyOf(0)], "all workers saw the same map entries")
		ck(ptrsB[w][0] == b0[0], "all workers saw the same slice entries")
	}
	checkTables(a0, b0)
	for i := 0; i < nslots; i++ {
		ck(atomic.LoadInt32(&slotBuilds[i]) == 1, "slot built exactly once")
		ck(ensureSlot(i) == int64(i)*7+3, "slot value")
	}

	// Panic semantics: first call panics, subsequent calls skip f entirely.
	ck(tryRisky(), "first Once.Do panic is observable")
	ck(!tryRisky(), "second Once.Do does not panic")
	ck(!tryRisky(), "third Once.Do does not panic")
	ck(atomic.LoadInt32(&panicRuns) == 1, "panicking Once body ran only once")

	// GC must not reset Once state or rebuild tables.
	gcStress()
	ck(ensureA()[keyOf(0)] == a0[keyOf(0)] && ensureB()[0] == b0[0],
		"Once tables are not rebuilt after GC")
	ck(atomic.LoadInt32(&buildA) == 1 && atomic.LoadInt32(&buildB) == 1,
		"global build counters unchanged after GC")
	checkTables(ensureA(), ensureB())
	for i := 0; i < nslots; i++ {
		ck(ensureSlot(i) == int64(i)*7+3, "slot value survives GC")
	}
	ck(atomic.LoadInt32(&panicRuns) == 1, "panicking Once stays done after GC")

	finish("l12_global_once_matrix")
}
