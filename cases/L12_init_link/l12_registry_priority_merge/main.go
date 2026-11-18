// L12: 跨包重复注册（module）。两个兄弟包的 init 注册同名 key，但合并策略
// 不是先到先得，而是确定性的“优先级高者胜，同优先级 owner 字典序小者胜”。
// 因此结果与未定义的跨包 init 顺序无关。main 的包级变量初始化阶段冻结注册表。
package main

import (
	"bytes"
	"fmt"
	"os"
	"runtime"
	"strconv"
	"sync"
	"sync/atomic"

	_ "l12_registry_priority_merge/plugin_high"
	_ "l12_registry_priority_merge/plugin_low"
	"l12_registry_priority_merge/reg"
)

var (
	failed  int32
	gcSink  [][]byte
	seedVal int64
	seedStr string

	// main's variable initializers run only after all imported packages
	// (including both blank-imported plugins) have finished init.
	frozenAtImport = freezeAfterImports()
)

func freezeAfterImports() bool {
	reg.Freeze()
	return reg.Frozen()
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

func sameKeys(got []string, want ...string) bool {
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

func checkWinner(key string, priority int, owner, data, tag string, score int) {
	w, ok := reg.Winner(key)
	ck(ok, "missing winner for "+key)
	if !ok {
		return
	}
	ck(w.Priority == priority && w.Owner == owner && bytes.Equal(w.Data, []byte(data)) &&
		w.Meta != nil && w.Meta.Tag == tag && w.Meta.Score == score,
		fmt.Sprintf("winner mismatch for %s: prio=%d owner=%s data=%q", key, w.Priority, w.Owner, w.Data))
}

func main() {
	seedVal = readSeed()
	seedStr = strconv.FormatInt(seedVal, 10)

	ck(frozenAtImport, "registry not frozen by main variable initializer")
	ck(reg.Frozen(), "registry reports not frozen")
	ck(reg.Total() == 6, fmt.Sprintf("contribution total %d, want 6", reg.Total()))
	ck(sameKeys(reg.Keys(), "codec", "high-only", "low-only", "tie"),
		fmt.Sprintf("keys mismatch: %v", reg.Keys()))

	// Winners must be independent of which sibling plugin init ran first.
	checkWinner("codec", 10, "alpha", "high-codec", "high", 10)
	checkWinner("tie", 7, "aaa", "low-tie", "low-tie", 7) // equal priority, smaller owner
	checkWinner("high-only", 3, "alpha", "only-high", "high-only", 3)
	checkWinner("low-only", 9, "beta", "only-low", "low-only", 9)

	// History is sorted by owner, not by the undefined init order.
	hc := reg.History("codec")
	ck(len(hc) == 2 && hc[0].Owner == "alpha" && hc[1].Owner == "beta", "codec history order")
	ht := reg.History("tie")
	ck(len(ht) == 2 && ht[0].Owner == "aaa" && ht[1].Owner == "zzz", "tie history order")

	// Frozen registry rejects late writers without mutating state.
	err := reg.Register(reg.Contribution{
		Key: "codec", Priority: 99, Owner: "late",
		Data: []byte("late-codec"), Meta: &reg.Meta{Tag: "late", Score: 99},
	})
	ck(err == reg.ErrFrozen, "late registration did not return ErrFrozen")
	ck(reg.Total() == 6, "late registration changed contribution total")
	checkWinner("codec", 10, "alpha", "high-codec", "high", 10)
	ck(sameKeys(reg.Keys(), "codec", "high-only", "low-only", "tie"), "late registration changed keys")

	// Read-only concurrent view plus explicit GC.
	var wg sync.WaitGroup
	var bad int32
	workers := 8
	start := int(seedVal % 4)
	keys := []string{"codec", "high-only", "low-only", "tie"}
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			seen := map[string]*reg.Meta{}
			for i := 0; i < 200; i++ {
				k := keys[(start+w+i)%len(keys)]
				win, ok := reg.Winner(k)
				if !ok || win.Meta == nil || reg.Total() != 6 {
					atomic.AddInt32(&bad, 1)
					return
				}
				if prev, ok := seen[k]; ok {
					if win.Meta != prev {
						atomic.AddInt32(&bad, 1)
						return
					}
				} else {
					seen[k] = win.Meta
				}
				wantHist := 1
				if k == "codec" || k == "tie" {
					wantHist = 2
				}
				if h := reg.History(k); len(h) != wantHist {
					atomic.AddInt32(&bad, 1)
					return
				}
			}
		}(w)
	}
	for r := 0; r < 4; r++ {
		for i := 0; i < 16; i++ {
			b := make([]byte, 8192)
			b[i%len(b)] = byte(r)
			gcSink = append(gcSink[:0], b)
		}
		runtime.GC()
	}
	wg.Wait()
	gcSink = gcSink[:0]
	ck(atomic.LoadInt32(&bad) == 0, "concurrent registry read failed under GC")

	gcStress()
	checkWinner("codec", 10, "alpha", "high-codec", "high", 10)
	checkWinner("tie", 7, "aaa", "low-tie", "low-tie", 7)
	ck(reg.Total() == 6 && reg.Frozen(), "registry state changed after GC")
	ck(len(reg.History("codec")) == 2, "history lost after GC")

	finish("l12_registry_priority_merge")
}
