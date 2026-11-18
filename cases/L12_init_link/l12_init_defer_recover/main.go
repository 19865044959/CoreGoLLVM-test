// L12: init 中的 defer/recover 语义。三个文件各自贡献一个 init，跨文件
// 顺序未定义；本用例只断言每个 init 块内部由规范保证的 defer LIFO 顺序、
// panic 恢复后 init 继续执行、以及所有 init 都早于 main。
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

// block finds want as a contiguous subsequence of trace and returns its start
// index, or -1. Each init function runs atomically, so its marks are adjacent
// even though the cross-file init order is unspecified.
func block(trace, want []string) int {
	for i := 0; i+len(want) <= len(trace); i++ {
		ok := true
		for j := range want {
			if trace[i+j] != want[j] {
				ok = false
				break
			}
		}
		if ok {
			return i
		}
	}
	return -1
}

func countFirst(trace []string, first string) int {
	n := 0
	for _, s := range trace {
		if s == first {
			n++
		}
	}
	return n
}

func verifyTrace(trace []string) {
	wantA := []string{"a:enter", "a:body", "a:defer3", "a:defer2", "a:defer1"}
	wantB := []string{"b:enter", "b:panic-pre", "b:recover", "b:after", "b:defer"}
	wantC := []string{"c:enter", "c:h1", "c:h2", "c:h3", "c:panic-pre", "c:recover", "c:defer"}

	ck(len(trace) == 17, fmt.Sprintf("trace length %d, want 17", len(trace)))
	ia := block(trace, wantA)
	ib := block(trace, wantB)
	ic := block(trace, wantC)
	ck(ia >= 0, "init A block is not in defer LIFO order")
	ck(ib >= 0, "init B block missing recover/continue/defer marks")
	ck(ic >= 0, "init C block missing nested defer LIFO or recover marks")
	ck(countFirst(trace, "a:enter") == 1, "init A ran more than once")
	ck(countFirst(trace, "b:enter") == 1, "init B ran more than once")
	ck(countFirst(trace, "c:enter") == 1, "init C ran more than once")
}

func main() {
	seedVal = readSeed()
	seedStr = strconv.FormatInt(seedVal, 10)

	// main's first reads must already see the complete init trace.
	snapshot := append([]string(nil), trace...)
	verifyTrace(snapshot)
	ck(probeRoot != nil && probeRoot.value == 30, "deferred init write to package pointer not visible")
	ck(bRecovered == 1, "init B recovered panic count is not 1")
	ck(cRecovered == 1, "init C recovered panic count is not 1")
	ck(cCompleted, "init C did not continue after recovered panic")

	// Concurrent read-only verification while GC runs. Inits are over, so the
	// slice and probe must stay stable.
	var wg sync.WaitGroup
	var bad int32
	workers := 8
	start := int(seedVal % 3)
	blocks := [][]string{
		{"a:enter", "a:body", "a:defer3", "a:defer2", "a:defer1"},
		{"b:enter", "b:panic-pre", "b:recover", "b:after", "b:defer"},
		{"c:enter", "c:h1", "c:h2", "c:h3", "c:panic-pre", "c:recover", "c:defer"},
	}
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < 200; i++ {
				t := trace
				if len(t) != 17 || probeRoot == nil || probeRoot.value != 30 {
					atomic.AddInt32(&bad, 1)
					return
				}
				idx := (start + w + i) % 3
				if block(t, blocks[idx]) < 0 {
					atomic.AddInt32(&bad, 1)
					return
				}
			}
		}(w)
	}
	gcRounds := 2 + int(seedVal%3)
	for r := 0; r < gcRounds; r++ {
		for i := 0; i < 16; i++ {
			b := make([]byte, 8192)
			b[i%len(b)] = byte(r)
			gcSink = append(gcSink[:0], b)
		}
		runtime.GC()
	}
	wg.Wait()
	gcSink = gcSink[:0]
	ck(atomic.LoadInt32(&bad) == 0, "concurrent read of init trace/probe failed under GC")

	gcStress()
	verifyTrace(trace)
	ck(probeRoot != nil && probeRoot.value == 30, "deferred init write lost after GC")
	ck(cCompleted && bRecovered == 1 && cRecovered == 1, "init recover state changed after GC")

	finish("l12_init_defer_recover")
}
