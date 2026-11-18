// L12: multi-package module initialization order.
// Rules checked here are defined by the Go spec:
//   - a package is fully initialized (vars, then init) before its importers;
//   - package-level variables are initialized before init functions;
//   - init runs exactly once per package.
//
// Sibling packages (mid/left/right) have no defined relative order, so the
// trace is only checked for the defined partial order.
package main

import (
	"fmt"
	"os"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"

	"l12_module_init_order/base"
	"l12_module_init_order/left"
	"l12_module_init_order/mid"
	"l12_module_init_order/right"
	"l12_module_init_order/sink"
	"l12_module_init_order/top"
)

var (
	failed  int32
	gcSink  [][]byte
	seedVal int64
	seedStr string

	// mainSnapshot runs after every imported package, including top, and
	// before main.init. It must contain exactly the eleven imported marks.
	mainSnapshot = sink.Marks()
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

func init() {
	sink.Add("main.init")
}

func checkMiddleBlocks(marks []string) {
	if len(marks) != 12 {
		ck(false, "unexpected trace length")
		return
	}
	ck(marks[0] == "sink.init", "sink package initializes before importers")
	ck(marks[1] == "base.var" && marks[2] == "base.init",
		"base vars then base init, before dependents")
	ck(marks[9] == "top.var" && marks[10] == "top.init" && marks[11] == "main.init",
		"top block and main.init are last")

	prefixes := make([]string, 0, 3)
	for i := 3; i < 9; i += 2 {
		v := marks[i]
		in := marks[i+1]
		if !strings.HasSuffix(v, ".var") {
			ck(false, "middle block does not start with a var mark: "+v)
			continue
		}
		p := strings.TrimSuffix(v, ".var")
		ck(in == p+".init", "same-package var before init: "+p)
		prefixes = append(prefixes, p)
	}
	sort.Strings(prefixes)
	ck(strings.Join(prefixes, ",") == "left,mid,right",
		"mid/left/right each initialized exactly once")
}

func checkValues() {
	ck(base.Base == 10 && base.BaseReady, "base values")
	ck(mid.Mid == 102 && mid.MidReady, "mid depends on initialized base")
	ck(left.Left == 11 && left.LeftReady, "left depends on initialized base")
	ck(right.Right == 12 && right.RightReady, "right depends on initialized base")
	ck(top.Top == 1046 && top.TopObserved && top.TopReady,
		"top observes all dependencies and computes its value")
	ck(len(mainSnapshot) == 11, "main var initializer sees imported packages initialized")
	for i := 0; i < len(mainSnapshot); i++ {
		ck(mainSnapshot[i] == sink.Marks()[i], "main snapshot matches imported trace")
	}
}

func main() {
	seedVal = readSeed()
	seedStr = strconv.FormatInt(seedVal, 10)

	marks := sink.Marks()
	checkMiddleBlocks(marks)
	checkValues()

	// Concurrent read-only access to package-level init state, with GC in the
	// middle. Nothing may append to the trace after initialization.
	const workers = 8
	startBase := int(seedVal % 3)
	var wg sync.WaitGroup
	var bad int32
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < 200; i++ {
				if top.Top != 1046 || mid.Mid != 102 {
					atomic.AddInt32(&bad, 1)
					return
				}
				if len(sink.Marks()) != 12 {
					atomic.AddInt32(&bad, 1)
					return
				}
				_ = left.Left + right.Right + base.Base + (startBase+w+i)%3
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
	ck(atomic.LoadInt32(&bad) == 0, "concurrent reads under GC")
	gcSink = gcSink[:0]

	gcStress()
	ck(len(sink.Marks()) == 12, "init trace is not replayed after GC")
	checkMiddleBlocks(sink.Marks())
	checkValues()

	finish("l12_module_init_order")
}
