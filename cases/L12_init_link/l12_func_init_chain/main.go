// L12: package-level variables initialized by function calls.
// The log order is defined only by initialization dependencies, never by
// source-file name order.
package main

import (
	"fmt"
	"os"
	"runtime"
	"strconv"
	"strings"
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

func indexOf(log []string, name string) int {
	for i, v := range log {
		if v == name {
			return i
		}
	}
	return -1
}

func main() {
	seedVal = readSeed()
	seedStr = strconv.FormatInt(seedVal, 10)

	// Values produced by function-call initializers.
	ck(a == 3, "a via function call")
	ck(b == 7, "b depends on a and a function call")
	ck(c == 14, "c depends on b")
	ck(d == 15, "d depends on c plus another function call")
	ck(e != nil && e.id == 42 && e.v == 15 && e.tag == "e-tag", "e constructed by function call")
	ck(root == e, "root aliases e")
	ck(registry[42] == e, "registration side effect during variable initialization")

	ck(atomic.LoadInt32(&mkCount) == 4, "mkInt called exactly four times")
	ck(atomic.LoadInt32(&tagCount) == 1, "mkTag called exactly once")
	ck(atomic.LoadInt32(&regCount) == 1, "registerInto called exactly once")
	ck(initSeen == 30, "init observes d and e after variable initialization")
	ck(init2Seen == 1, "second init observes registration table")

	// This sequence is fully fixed by dependencies: a -> b -> c -> d -> e -> root,
	// and every package-level variable initializer runs before any init function.
	want := []string{"a", "b", "c", "d", "e", "root", "init"}
	if len(initLog) != len(want) {
		ck(false, "initialization log length")
	} else {
		for i := range want {
			ck(initLog[i] == want[i], "initialization order at "+strconv.Itoa(i))
		}
	}
	if indexOf(initLog, "init") >= 0 {
		for _, name := range []string{"a", "b", "c", "d", "e", "root"} {
			ck(indexOf(initLog, name) < indexOf(initLog, "init"),
				"package variable before init: "+name)
		}
	}

	// Concurrent readers plus GC: the initialized globals must not change and
	// the initializers must not run again.
	const workers = 8
	start := int(seedVal % int64(len(want)))
	var bad int32
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < 300; i++ {
				name := want[(start+w+i)%len(want)]
				if indexOf(initLog, name) < 0 {
					atomic.AddInt32(&bad, 1)
					return
				}
				if root != e || registry[42] != e || d != 15 {
					atomic.AddInt32(&bad, 1)
					return
				}
			}
		}(w)
	}
	for i := 0; i < 4; i++ {
		for j := 0; j < 8; j++ {
			p := make([]byte, 8192)
			p[j] = byte(i)
			gcSink = append(gcSink[:0], p)
		}
		runtime.GC()
	}
	wg.Wait()
	ck(atomic.LoadInt32(&bad) == 0, "globals stable under concurrent readers and GC")
	gcSink = gcSink[:0]

	gcStress()
	ck(atomic.LoadInt32(&mkCount) == 4 && atomic.LoadInt32(&tagCount) == 1 &&
		atomic.LoadInt32(&regCount) == 1, "initializers are not re-run after GC")
	ck(len(initLog) == len(want) && strings.Join(initLog, ",") == strings.Join(want, ","),
		"initialization log unchanged after GC")
	ck(root == e && e.payloadOK() && registry[42] == e, "pointers survive GC")

	finish("l12_func_init_chain")
}
