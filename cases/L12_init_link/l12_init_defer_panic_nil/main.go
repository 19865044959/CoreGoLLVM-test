// L12: init defer/recover boundaries. init A recovers panic(nil) (Go 1.21+
// yields *runtime.PanicNilError); init B panics inside a deferred function
// while already unwinding from another panic and the earlier-registered defer
// recovers the second panic; init C recovers through a helper and observes that
// a direct recover call outside a deferred function returns nil.
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
	trace []string

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

func initBlock(prefix string, n int) []string {
	for i := 0; i+n <= len(trace); i++ {
		if trace[i] == prefix+":enter" {
			return trace[i : i+n]
		}
	}
	return nil
}

func equalBlock(got []string, want ...string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}

func main() {
	seedVal = readSeed()
	seedStr = strconv.FormatInt(seedVal, 10)

	ck(len(trace) == 11, fmt.Sprintf("init trace length %d", len(trace)))
	ck(equalBlock(initBlock("a", 4), "a:enter", "a:before", "a:recover", "a:defer"), "init A defer/recover order")
	ck(equalBlock(initBlock("b", 4), "b:enter", "b:before", "b:defer-panic", "b:recover-second"), "init B nested panic order")
	ck(equalBlock(initBlock("c", 3), "c:enter", "c:after-helper", "c:defer"), "init C helper order")

	ck(nilRecovered != nil, "init A recover(panic(nil)) returned nil")
	ck(nilRecoveredType == "*runtime.PanicNilError", fmt.Sprintf("panic(nil) type %q", nilRecoveredType))
	if r, ok := nilRecovered.(error); ok {
		ck(r.Error() == "panic called with nil argument", fmt.Sprintf("panic(nil) text %q", r.Error()))
	} else {
		ck(false, "panic(nil) value does not implement error")
	}

	ck(bRecovered == "b-second", fmt.Sprintf("init B recovered %v", bRecovered))
	ck(bRecoveredType == "string", fmt.Sprintf("init B panic type %q", bRecoveredType))
	ck(cDirect == nil, "direct recover outside defer should be nil")
	ck(cRecovered == "c-helper", fmt.Sprintf("init C helper recovered %v", cRecovered))
	ck(cRecoveredType == "string", fmt.Sprintf("init C panic type %q", cRecoveredType))

	// Concurrent read-only view of the init-produced trace with explicit GC.
	workerRounds := 2 + int(seedVal%3)
	var wg sync.WaitGroup
	var bad int32
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 400; i++ {
				if len(trace) != 11 {
					atomic.AddInt32(&bad, 1)
					return
				}
				if !equalBlock(initBlock("a", 4), "a:enter", "a:before", "a:recover", "a:defer") {
					atomic.AddInt32(&bad, 1)
					return
				}
				if !equalBlock(initBlock("b", 4), "b:enter", "b:before", "b:defer-panic", "b:recover-second") {
					atomic.AddInt32(&bad, 1)
					return
				}
				if !equalBlock(initBlock("c", 3), "c:enter", "c:after-helper", "c:defer") {
					atomic.AddInt32(&bad, 1)
					return
				}
			}
		}()
	}
	gcStress(workerRounds)
	wg.Wait()
	ck(atomic.LoadInt32(&bad) == 0, "concurrent init trace read failed under GC")

	gcStress(2)
	ck(len(trace) == 11 && equalBlock(initBlock("a", 4), "a:enter", "a:before", "a:recover", "a:defer"), "init trace changed after GC")
	ck(nilRecoveredType == "*runtime.PanicNilError" && bRecovered == "b-second" && cRecovered == "c-helper", "recovered values changed after GC")

	finish("l12_init_defer_panic_nil")
}
