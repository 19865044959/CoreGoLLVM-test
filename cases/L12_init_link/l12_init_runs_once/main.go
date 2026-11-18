// L12: 多文件 init 各执行一次；同文件内 init 顺序有定义，跨文件顺序不断言。
package main

import (
	"fmt"
	"os"
	"runtime"
	"sync/atomic"
)

var (
	failed int32
	gcSink [][]byte
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
		fmt.Printf("FAILED %s (%d failed checks)\n", name, n)
		os.Exit(1)
	}
	fmt.Println("ALL PASS " + name)
}

func traceIndex(mark string) int {
	for i, m := range initTrace {
		if m == mark {
			return i
		}
	}
	return -1
}

func countTrace(mark string) int {
	n := 0
	for _, m := range initTrace {
		if m == mark {
			n++
		}
	}
	return n
}

func main() {
	// 每个文件的 init 恰好运行一次。
	ck(atomic.LoadInt32(&aRuns) == 1, "init_a.go init runs exactly once")
	ck(atomic.LoadInt32(&bRuns) == 1, "init_b.go init runs exactly once")
	ck(atomic.LoadInt32(&cRuns) == 1, "init_c.go init runs exactly once")
	ck(atomic.LoadInt32(&initBad) == 0, "all init funcs saw fully initialized package vars")

	// 共 4 个 init：a 文件 2 个，b/c 文件各 1 个。
	ck(len(initTrace) == 4, "exactly 4 init marks")
	for _, mark := range []string{"a#1", "a#2", "b#1", "c#1"} {
		ck(countTrace(mark) == 1, "mark "+mark+" appears exactly once")
	}

	// 同文件内两个 init 的顺序有定义；跨文件顺序未定义，不做断言。
	ck(traceIndex("a#1") >= 0 && traceIndex("a#2") >= 0 && traceIndex("a#1") < traceIndex("a#2"),
		"two init funcs in init_a.go run in source order")
	ck(atomic.LoadInt32(&afterFirstA) == 1, "second init in same file observes first init side effect")

	// 所有 init 都在包级变量初始化之后。
	ck(initEpoch == 2021 && initReady, "package vars initialized before init")

	// GC 不得影响 init 期间写入的全局 trace / 计数。
	before := append([]string(nil), initTrace...)
	gcStress()
	ck(len(initTrace) == len(before), "trace length stable across GC")
	same := true
	for i := range before {
		if initTrace[i] != before[i] {
			same = false
		}
	}
	ck(same, "trace content stable across GC")
	ck(atomic.LoadInt32(&aRuns) == 1 && atomic.LoadInt32(&bRuns) == 1 && atomic.LoadInt32(&cRuns) == 1,
		"init counters unchanged after GC")

	gcStress()
	ck(initEpoch == 2021 && initReady, "package vars survive GC")
	ck(atomic.LoadInt32(&initBad) == 0, "still no init observation violations")

	finish("l12_init_runs_once")
}
