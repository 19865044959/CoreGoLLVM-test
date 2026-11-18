// L12: blank import 的传递副作用。根包只 blank import plugin；plugin 再
// blank import inner。两个包的 init 都必须被链接器保留并按依赖顺序执行；
// 同时 other 正常 import inner，证明第二条导入路径不会重跑 inner init。
package main

import (
	"fmt"
	"os"
	"runtime"
	"strconv"
	"sync"
	"sync/atomic"

	"l12_blank_import_transitive/other"
	"l12_blank_import_transitive/sink"

	_ "l12_blank_import_transitive/plugin"
)

var (
	failed  int32
	gcSink  [][]byte
	seedVal int64
	seedStr string

	// mainSnapshot is evaluated after all imports, including the blank-imported
	// plugin and its transitive inner, have finished initializing.
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

func sameStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func checkSideEffects() {
	// The blank-imported plugin ran, and it observed inner's side effect.
	ck(len(mainSnapshot) == 2, "main var sees two blank-import marks")
	ck(sameStrings(mainSnapshot, []string{"inner.init", "plugin.init"}),
		"transitive blank imports run in dependency order")
	ck(sink.Count("inner") == 1, "inner init ran exactly once")
	seen, ok := sink.Get("plugin.sawInner")
	ck(ok && seen == 1, "plugin saw inner init before its own init")
	ready, ok := sink.Get("plugin.ready")
	ck(ok && ready == 1, "plugin side effect recorded")

	marks := sink.Marks()
	ck(len(marks) == 3, "mark count after main.init")
	ck(sameStrings(marks, []string{"inner.init", "plugin.init", "main.init"}),
		"blank import marks then main.init")

	// The normal second import path must not duplicate inner's init.
	ck(other.Value == 6 && other.Ready, "normal importer sees inner state")
	ck(sink.Count("inner") == 1, "second import path did not rerun inner init")
}

func main() {
	seedVal = readSeed()
	seedStr = strconv.FormatInt(seedVal, 10)

	checkSideEffects()

	// Concurrent reads of the blank-import side effects, with GC.
	var wg sync.WaitGroup
	var bad int32
	start := int(seedVal % 2)
	for w := 0; w < 8; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < 200; i++ {
				if sink.Count("inner") != 1 || len(sink.Marks()) != 3 {
					atomic.AddInt32(&bad, 1)
					return
				}
				if v, ok := sink.Get("plugin.ready"); !ok || v != 1 {
					atomic.AddInt32(&bad, 1)
					return
				}
				_ = (start + w + i) % 2
			}
		}(w)
	}
	for i := 0; i < 8; i++ {
		gcSink = append(gcSink, make([]byte, 16384))
		runtime.GC()
	}
	wg.Wait()
	ck(atomic.LoadInt32(&bad) == 0, "blank-import side effects stable under GC")
	gcSink = gcSink[:0]

	gcStress()
	checkSideEffects()
	ck(sameStrings(sink.Marks(), []string{"inner.init", "plugin.init", "main.init"}),
		"no init replay after GC")

	finish("l12_blank_import_transitive")
}
