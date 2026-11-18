// L12: 跨包注册表重复。两个独立插件包在 init 中注册同一个名字；它们的
// 相对 init 顺序未定义，因此用例不断言谁先谁后，只断言“恰好一个成功、
// 另一个得到 ErrDuplicate”这一与顺序无关的确定结果。
package main

import (
	"errors"
	"fmt"
	"os"
	"runtime"
	"strconv"
	"sync"
	"sync/atomic"

	"l12_dup_register_crosspkg/plugin1"
	"l12_dup_register_crosspkg/plugin2"
	"l12_dup_register_crosspkg/reg"
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

func main() {
	seedVal = readSeed()
	seedStr = strconv.FormatInt(seedVal, 10)

	// Init-time duplicate: order-independent invariant.
	e1, e2 := plugin1.Outcome(), plugin2.Outcome()
	ck((e1 == nil) != (e2 == nil), "exactly one init registration wins")
	winVal := 0
	winOwner := ""
	winTag := ""
	if e1 == nil {
		ck(errors.Is(e2, reg.ErrDuplicate), "loser gets ErrDuplicate")
		winVal, winOwner, winTag = plugin1.Value, "plugin1", "p1"
	} else {
		ck(errors.Is(e1, reg.ErrDuplicate), "loser gets ErrDuplicate")
		winVal, winOwner, winTag = plugin2.Value, "plugin2", "p2"
	}

	ck(reg.Default.Len() == 1, "registry has exactly one dup entry")
	ck(sameStrings(reg.Default.Names(), []string{"dup"}), "sorted names")
	entry, ok := reg.Default.Get("dup")
	ck(ok, "dup entry exists")
	ck(entry.Value == winVal, "dup entry value belongs to the winner")
	ck(entry.Owner == winOwner, "dup entry owner belongs to the winner")
	ck(entry.Data != nil && entry.Data.Owner == winOwner && entry.Data.Tag == winTag,
		"registration payload intact")

	// Post-init duplicate is rejected without overwriting.
	err := reg.Default.Register("dup", 303, "main", &reg.Payload{Owner: "main", Tag: "m"})
	ck(errors.Is(err, reg.ErrDuplicate), "post-init duplicate rejected")
	after, ok := reg.Default.Get("dup")
	ck(ok && after.Value == winVal && after.Owner == winOwner, "duplicate did not overwrite")
	ck(after.Data != nil && after.Data.Tag == winTag, "duplicate did not replace payload")

	// Concurrent duplicate registration: exactly one goroutine wins.
	const racers = 16
	var (
		successSlot int32 = -1
		successes   int32
		bad         int32
		wg          sync.WaitGroup
	)
	for i := 0; i < racers; i++ {
		slot := (int(seedVal) + i) % racers
		wg.Add(1)
		go func(slot int) {
			defer wg.Done()
			err := reg.Default.Register("race", 1000+slot, "racer",
				&reg.Payload{Owner: "racer", Tag: "r"})
			switch {
			case err == nil:
				atomic.CompareAndSwapInt32(&successSlot, -1, int32(slot))
				atomic.AddInt32(&successes, 1)
			case errors.Is(err, reg.ErrDuplicate):
				// expected for all losers
			default:
				atomic.AddInt32(&bad, 1)
			}
		}(slot)
	}
	wg.Wait()
	ck(atomic.LoadInt32(&bad) == 0, "all losers return exactly ErrDuplicate")
	ck(atomic.LoadInt32(&successes) == 1, "exactly one concurrent registration wins")
	slot := atomic.LoadInt32(&successSlot)
	ck(slot >= 0, "winner recorded")
	race, ok := reg.Default.Get("race")
	ck(ok && race.Value == 1000+int(slot), "race entry belongs to the winner")
	ck(race.Data != nil && race.Data.Owner == "racer", "race payload intact")
	ck(sameStrings(reg.Default.Names(), []string{"dup", "race"}), "sorted names after race")

	// Explicit GC: map entries and their pointer payloads must survive.
	gcStress()
	entry, ok = reg.Default.Get("dup")
	ck(ok && entry.Value == winVal && entry.Data != nil && entry.Data.Tag == winTag,
		"dup entry survives GC")
	race, ok = reg.Default.Get("race")
	ck(ok && race.Data != nil && race.Data.Owner == "racer", "race entry survives GC")
	ck(reg.Default.Len() == 2, "registry size unchanged after GC")
	ck(errors.Is(reg.Default.Register("race", 999, "late", nil), reg.ErrDuplicate),
		"GC does not clear duplicate detection")

	finish("l12_dup_register_crosspkg")
}
