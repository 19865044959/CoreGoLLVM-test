// L12: registration sequence and stable ordering.
// Three init functions register one handler each in undefined file order.
// The case asserts only defined properties: sequence numbers are contiguous,
// the order slice matches sequence order, removal does not renumber, and a
// stable priority view preserves registration order among equal priorities.
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

func checkInitial() {
	list := ordered()
	ck(len(list) == 3 && len(reg.m) == 3, "three init registrations")
	seen := map[string]bool{}
	for i, h := range list {
		ck(h.seq == i, "initial sequence is contiguous and matches order")
		seen[h.name] = true
	}
	ck(seen["alpha"] && seen["beta"] && seen["gamma"], "all init handlers registered")
	ck(reg.nextSeq == 3, "next sequence after init")
}

func checkStable(view []*handler) {
	lastPrio := -1 << 30
	lastSeq := -1
	for _, h := range view {
		if h.prio < lastPrio {
			ck(false, "priority view is not sorted")
			return
		}
		if h.prio == lastPrio && h.seq <= lastSeq {
			ck(false, "equal priority lost registration order")
			return
		}
		lastPrio = h.prio
		lastSeq = h.seq
	}
}

func main() {
	seedVal = readSeed()
	seedStr = strconv.FormatInt(seedVal, 10)

	checkInitial()

	// Explicit registrations append to the end in call order, independent of
	// name or priority.
	delta := register("delta", 1, 21)
	eps := register("epsilon", 5, 22)
	ck(delta.seq == 3 && eps.seq == 4, "explicit registration gets next sequence")
	list := ordered()
	ck(list[3] == delta && list[4] == eps, "order slice appends in call order")

	// Priorities are distinct per init handler, so this full order is defined.
	view := byPriority()
	ck(len(view) == 5, "priority view size")
	names := make([]string, len(view))
	for i, h := range view {
		names[i] = h.name
	}
	ck(names[0] == "beta" && names[1] == "delta" && names[2] == "gamma" &&
		names[3] == "alpha" && names[4] == "epsilon", "stable priority order")
	checkStable(view)

	// Removal must not renumber live handlers or reuse sequence numbers.
	first := ordered()[0]
	removedName := first.name
	ck(unregister(removedName), "unregister first init handler")
	ck(unregister("missing") == false, "unregister unknown handler is a no-op")
	ck(reg.nextSeq == 5, "unregister does not change next sequence")
	list = ordered()
	ck(len(list) == 4, "order size after unregister")
	zeta := register("zeta", 0, 23)
	ck(zeta.seq == 5, "sequence number is not reused")
	ck(reg.nextSeq == 6, "next sequence advances past hole")
	list = ordered()
	ck(len(list) == 5 && list[4] == zeta, "new handler appended after hole")
	checkStable(byPriority())
	ck(byPriority()[0] == zeta, "priority 0 handler sorts first")

	// Concurrent read-only access with GC in the middle.
	live := ordered()
	start := int(seedVal % int64(len(live)))
	const workers = 8
	const iters = 300
	var wg sync.WaitGroup
	var bad int32
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < iters; i++ {
				h := live[(start+w+i)%len(live)]
				if lookup(h.name) != h || reg.m[h.name] != h {
					atomic.AddInt32(&bad, 1)
					return
				}
				if h.weight < 11 || h.seq < 0 {
					atomic.AddInt32(&bad, 1)
					return
				}
			}
		}(w)
	}
	for i := 0; i < 4; i++ {
		for j := 0; j < 8; j++ {
			b := make([]byte, 8192)
			b[j] = byte(i)
			gcSink = append(gcSink[:0], b)
		}
		runtime.GC()
	}
	wg.Wait()
	ck(atomic.LoadInt32(&bad) == 0, "concurrent lookups under GC")
	gcSink = gcSink[:0]

	gcStress()
	ck(lookup(zeta.name) == zeta && lookup(removedName) == nil, "lookups survive GC")
	ck(zeta.seq == 5 && reg.nextSeq == 6, "sequence state survives GC")
	checkStable(byPriority())

	finish("l12_register_sequence")
}
