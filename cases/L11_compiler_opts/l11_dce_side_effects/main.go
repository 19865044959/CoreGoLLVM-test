// L11: 死代码消除不得越过可观察副作用——返回值被丢弃的调用、常量分支、
// 分配与 panic/recover 的副作用都必须按语义发生；GC 后计数与对象内容不变。
package main

import (
	"fmt"
	"os"
	"runtime"
	"sync/atomic"
)

var (
	failed     int32
	gcSink     [][]byte
	sideHits   int64
	sideAllocs int64
	sidePanics int64
	sink       int64
	kept       []*blob
)

type blob struct {
	id   int
	data [8]byte
}

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
		b[len(b)-1] = byte(255 - i)
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

func bump() int {
	sideHits++
	return 7
}

func addHits(n int) int64 {
	sideHits += int64(n)
	return sideHits
}

// pureCalc 无副作用，结果未使用时允许被删除。
func pureCalc(x int) int {
	return x*3 + 1
}

// allocBlob 的结果被丢弃时，全局计数与对象分配仍是可观察副作用。
func allocBlob(id int) *blob {
	sideAllocs++
	b := &blob{id: id}
	for i := range b.data {
		b.data[i] = byte(id*7 + i*3)
	}
	kept = append(kept, b)
	return b
}

// panicRecover 先递增计数再 panic，recover 后必须回到调用点。
func panicRecover(id int) (out string) {
	sidePanics++
	defer func() {
		if r := recover(); r != nil {
			out = "recovered:" + fmt.Sprint(r)
		}
	}()
	if id >= 0 {
		panic("boom")
	}
	return "nopanic"
}

func main() {
	// 纯函数结果丢弃：无副作用，执行与否不影响可观察状态。
	_ = pureCalc(5)
	_ = pureCalc(6)
	ck(sideHits == 0, "pure call must not touch side counters")

	// 返回值被丢弃的有副作用调用必须执行。
	bump()
	ck(sideHits == 1, "ignored side-effect call was eliminated")

	for i := 0; i < 100; i++ {
		addHits(i)
	}
	ck(sideHits == 1+4950, "side effect in value-ignored loop was eliminated")

	if false {
		bump()
		sideHits += 100000
	}
	ck(sideHits == 4951, "dead constant-false branch executed")

	if true {
		addHits(2)
	}
	ck(sideHits == 4953, "constant-true branch was eliminated")

	if 2*3 == 7 {
		sideHits += 999
	}
	ck(sideHits == 4953, "dead folded comparison executed")

	for i := 0; i < 50; i++ {
		sink += int64(pureCalc(i))
	}
	ck(sink == 3725, "used pure result mismatch")

	for i := 0; i < 100; i++ {
		allocBlob(i)
	}
	ck(sideAllocs == 100, "allocation side effect lost")
	ck(len(kept) == 100, "allocated objects missing")

	gcStress()
	for i, b := range kept {
		ck(b != nil, "allocated object became nil")
		if b == nil {
			continue
		}
		ck(b.id == i, "allocated object id corrupted after GC")
		for j := range b.data {
			ck(b.data[j] == byte(i*7+j*3), "allocated object data corrupted after GC")
		}
	}
	ck(sideAllocs == 100, "side counter changed after GC")

	ck(panicRecover(1) == "recovered:boom", "panic/recover result mismatch")
	ck(sidePanics == 1, "panic side effect lost")
	ck(panicRecover(-1) == "nopanic", "non-panic path result mismatch")
	ck(sidePanics == 2, "non-panic side effect lost")
	panicRecover(-2)
	ck(sidePanics == 3, "ignored panic-path call was eliminated")

	gcStress()
	ck(sidePanics == 3 && sideAllocs == 100 && len(kept) == 100, "side effects lost across GC")

	finish("l11_dce_side_effects")
}
