// L12: init 内错误处理（不 panic），失败后表状态仍可判读，后续 init 可兜底。
package main

import (
	"errors"
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

func seqCount(mark string) int {
	n := 0
	for _, m := range initSeq {
		if m == mark {
			n++
		}
	}
	return n
}

func snapshotEqual(got map[string]int, want map[string]int) bool {
	if len(got) != len(want) {
		return false
	}
	for k, v := range want {
		if got[k] != v {
			return false
		}
	}
	return true
}

func main() {
	// 两个跨文件 init 都执行了，且没有因为前一个返回错误而中断。
	ck(len(initSeq) == 2, "both cross-file init funcs ran")
	ck(seqCount("load") == 1 && seqCount("fallback") == 1, "init sequence contains load and fallback")
	// 跨文件 init 顺序未定义，不断言先后。

	// 加载错误被捕获而不是 panic。
	loadErr, ok := modInitErrs["load"]
	ck(ok && loadErr != nil, "load init recorded an error")
	if loadErr != nil {
		ck(errors.Is(loadErr, errBadValue), "load error wraps errBadValue")
		ck(loadErr.Error() == `entry 2 "beta": invalid value: -5`, "load error text deterministic")
	}
	optErr, ok := modInitErrs["optional"]
	ck(ok && optErr != nil, "optional init recorded recovered panic")
	if optErr != nil {
		ck(optErr.Error() == "recovered: optional module broken", "recovered panic text deterministic")
	}
	_, fallbackErr := modInitErrs["fallback"]
	ck(!fallbackErr, "fallback init had no error")

	// 失败后表状态可判读且确定：alpha 来自失败前的前缀，beta/gamma 由兜底补齐。
	want := map[string]int{"alpha": 1, "beta": 2, "gamma": 3}
	ck(globalMods.len() == 3, "mod table length after init error")
	ck(snapshotEqual(globalMods.snapshot(), want), "mod table contents after init error")
	for name, value := range want {
		v, ok := globalMods.get(name)
		ck(ok && v == value, "readable entry "+name)
	}

	// 错误分类仍可在失败状态上使用。
	ck(errors.Is(globalMods.put("alpha", 9), errDuplicateKey), "duplicate detection on recovered table")
	ck(errors.Is(globalMods.put("zeta", -1), errBadValue), "negative value still rejected")
	ck(errors.Is(globalMods.put("", 1), errEmptyKey), "empty name still rejected")
	ck(globalMods.len() == 3, "rejected puts leave table unchanged")

	// 修正后的 fixture 可以正常加载。
	fresh := newModTable()
	err := loadFixture(fresh, []fixtureEntry{{"delta", 4}, {"epsilon", 5}})
	ck(err == nil, "corrected fixture loads without error")
	ck(snapshotEqual(fresh.snapshot(), map[string]int{"delta": 4, "epsilon": 5}), "corrected fixture contents")

	gcStress()
	ck(snapshotEqual(globalMods.snapshot(), want), "mod table survives GC")
	ck(snapshotEqual(fresh.snapshot(), map[string]int{"delta": 4, "epsilon": 5}), "fresh table survives GC")
	ck(modInitErrs["load"].Error() == `entry 2 "beta": invalid value: -5`, "error value survives GC")
	e, ok := globalMods.entries["alpha"]
	ck(ok && e != nil && e.name == "alpha" && e.value == 1, "entry pointer intact after GC")

	// 再触发一轮 GC 并确认 init 结果没有被重复应用。
	before := globalMods.len()
	for i := 0; i < 32; i++ {
		gcSink = append(gcSink, make([]byte, 1024))
	}
	gcSink = nil
	runtime.GC()
	ck(globalMods.len() == before && before == 3, "no init replay after GC")

	finish("l12_init_error_path")
}
