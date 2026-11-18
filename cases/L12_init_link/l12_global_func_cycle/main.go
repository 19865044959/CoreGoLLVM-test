// L12: global function-value cycles across a package boundary (module). hooks
// wires even/odd (mutual recursion through package variables), a
// self-referential fib, method values bound to a global pointer receiver, a
// dispatch table and an interface-held function during its init. main's own
// package-level initializers call those function values, proving the import
// initialization order, then dispatches concurrently with explicit GC.
package main

import (
	"fmt"
	"os"
	"runtime"
	"strconv"
	"sync"
	"sync/atomic"

	"l12_global_func_cycle/hooks"
)

var (
	// These package-level initializers run after hooks.init has wired the
	// function-value graph.
	probeFib   = hooks.Fib(10)
	probeHop   = hooks.Lookup("hop")(5)
	probeIface = hooks.Iff().(func(int) int)(41)
	viaChain   = hooks.Chain()[1](21)

	unset func(int) int
	late  func(int) int
)

var (
	failed     int32
	gcSink     [][]byte
	seedVal    int64
	seedStr    string
	initProbes bool
	fibRef     = [11]int{0, 1, 1, 2, 3, 5, 8, 13, 21, 34, 55}
)

func init() {
	late = func(n int) int { return n * 7 }
	initProbes = probeFib == 55 && probeHop == 15 && probeIface == 42 && viaChain == 42 && unset == nil
}

func ck(ok bool, msg string) {
	if !ok {
		atomic.AddInt32(&failed, 1)
		fmt.Println("FAIL:", msg)
	}
}

func gcStress(rounds int) {
	for r := 0; r < rounds; r++ {
		for i := 0; i < 16; i++ {
			b := make([]byte, 4096)
			b[0] = byte(i + r)
			gcSink = append(gcSink[:0], b)
		}
		runtime.GC()
	}
	gcSink = gcSink[:0]
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

func readRounds() int {
	s := os.Getenv("TORTURE_ROUNDS")
	if s == "" {
		return 1
	}
	v, err := strconv.Atoi(s)
	if err != nil || v < 1 {
		return 1
	}
	if v > 8 {
		return 8
	}
	return v
}

func main() {
	seedVal = readSeed()
	seedStr = strconv.FormatInt(seedVal, 10)
	rounds := readRounds()

	ck(initProbes, "main package-level initializers saw incomplete hooks graph")
	ck(probeFib == 55 && probeHop == 15 && probeIface == 42 && viaChain == 42, "package-level function-value probes")
	ck(unset == nil, "unset global function value should be nil")
	ck(late(6) == 42, "late-assigned global function value")

	// Mutual recursion through package-level function variables.
	ck(hooks.Even(0) && !hooks.Odd(0), "even/odd base case")
	ck(hooks.Even(10) && !hooks.Odd(10), "even/odd depth 10")
	ck(!hooks.Even(1001) && hooks.Odd(1001), "even/odd depth 1001")
	ck(hooks.Even(2048) && !hooks.Odd(2048), "even/odd depth 2048")

	// Self-referential global function value.
	for i := 0; i <= 10; i++ {
		if got := hooks.Fib(i); got != fibRef[i] {
			ck(false, fmt.Sprintf("fib(%d)=%d", i, got))
			break
		}
	}
	ck(hooks.Fib(20) == 6765, "fib(20)")

	// Function slices, tables, interface-held values and method values.
	chain := hooks.Chain()
	ck(len(chain) == 3, "chain length")
	ck(chain[0](4) == 5 && chain[1](4) == 8 && chain[2](10) == 55, "chain dispatch")
	chainLen, tableLen := hooks.ExportsMeta()
	ck(chainLen == 3 && tableLen == 5, "hooks exported shape")
	ck(hooks.Lookup("inc")(9) == 10 && hooks.Lookup("double")(9) == 18, "table dispatch")
	ck(hooks.Lookup("fib")(11) == 89, "table fib")
	ck(hooks.Lookup("missing") == nil, "missing table key should be nil")
	ck(hooks.Iff().(func(int) int)(7) == 8, "interface-held function value")

	// Method values bind the pointer receiver: later mutation is visible.
	hooks.SetBase(9)
	ck(hooks.Lookup("method")(1) == 10, "method value base 9")
	ck(hooks.Lookup("method")(3) == 12, "method value repeated")
	hooks.SetBase(11)
	ck(hooks.Lookup("method")(4) == 15, "method value sees mutated receiver")
	ck(hooks.Calls() >= 3, "method value calls not counted")

	// Concurrent dispatch through the same global function values with GC.
	workers := 8 * rounds
	iters := 400 * rounds
	var wg sync.WaitGroup
	var bad int32
	start := int(seedVal % 11)
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < iters; i++ {
				n := (start + w + i) % 64
				if hooks.Even(n) == hooks.Odd(n) {
					atomic.AddInt32(&bad, 1)
					return
				}
				k := n % 11
				if hooks.Fib(k) != fibRef[k] {
					atomic.AddInt32(&bad, 1)
					return
				}
				if hooks.Lookup("double")(k) != k*2 {
					atomic.AddInt32(&bad, 1)
					return
				}
				if hooks.Lookup("method")(1) != 12 {
					atomic.AddInt32(&bad, 1)
					return
				}
				if hooks.Chain()[0](k) != k+1 {
					atomic.AddInt32(&bad, 1)
					return
				}
			}
		}(w)
	}
	gcStress(3)
	wg.Wait()
	ck(atomic.LoadInt32(&bad) == 0, "concurrent global function-value dispatch failed")

	gcStress(2)
	ck(hooks.Fib(25) == 75025, "fib changed after GC")
	ck(hooks.Even(4096) && hooks.Odd(4095), "recursive cycle changed after GC")
	ck(hooks.Lookup("hop")(5) == 15 && hooks.Lookup("method")(1) == 12, "tables changed after GC")
	ck(len(hooks.Chain()) == 3 && hooks.Iff().(func(int) int)(1) == 2, "function graph changed after GC")
	ck(probeFib == 55 && probeHop == 15 && probeIface == 42 && viaChain == 42, "package-level probes changed after GC")

	finish("l12_global_func_cycle")
}
