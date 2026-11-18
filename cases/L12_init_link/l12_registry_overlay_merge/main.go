// L12: registry priority overlay across blank-imported init packages (module).
// Three layers register the same keys with priorities, equal-priority owners
// and tombstones. Merging is field-level and order-independent; main freezes
// the registry from a package-level initializer (after all imported package
// inits) and verifies the deterministic overlay, histories, rejection path and
// pointer payloads under GC.
package main

import (
	"errors"
	"fmt"
	"os"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"

	"l12_registry_overlay_merge/reg"

	_ "l12_registry_overlay_merge/layerbase"
	_ "l12_registry_overlay_merge/layerops"
	_ "l12_registry_overlay_merge/layerteam"
)

var frozenAtImport = func() bool {
	reg.Freeze()
	return reg.Frozen()
}()

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

func readRounds() int {
	s := os.Getenv("TORTURE_ROUNDS")
	if s == "" {
		return 1
	}
	v, err := strconv.Atoi(s)
	if err != nil || v < 1 {
		return 1
	}
	if v > 8 {
		return 8
	}
	return v
}

func fingerprint(ms []reg.Merged) string {
	var b strings.Builder
	for _, m := range ms {
		fmt.Fprintf(&b, "%s|%s|%s|%d|%s|", m.Key, m.Name, m.Addr, m.Weight, strings.Join(m.Sources, ","))
		if m.Meta != nil {
			b.WriteString(m.Meta.Tag)
			b.WriteByte(':')
			b.Write(m.Meta.Payload)
		} else {
			b.WriteString("-")
		}
		b.WriteByte('\n')
	}
	return b.String()
}

func main() {
	seedVal = readSeed()
	seedStr = strconv.FormatInt(seedVal, 10)
	rounds := readRounds()

	ck(frozenAtImport, "main package-level initializer froze registry before plugin init")
	ck(reg.Frozen(), "registry not frozen")
	ck(reg.Total() == 12, fmt.Sprintf("contribution count %d", reg.Total()))

	snap := reg.Snapshot()
	ck(len(snap) == 3, fmt.Sprintf("snapshot key count %d", len(snap)))
	wantFingerprint := "db|ops-db|team:1|5|base,team,ops|ops-db:m-ops\n" +
		"onlyteam|only-team|team:9|9|team|-\n" +
		"queue|aaa-name|bbb:6|6|base,bbb,aaa|-\n"
	ck(fingerprint(snap) == wantFingerprint, fmt.Sprintf("overlay fingerprint %q", fingerprint(snap)))

	if len(snap) == 3 {
		ck(snap[0].Key == "db" && snap[1].Key == "onlyteam" && snap[2].Key == "queue", "snapshot key order")
		ck(snap[0].Name == "ops-db" && snap[0].Addr == "team:1" && snap[0].Weight == 5, "db field-level overlay")
		ck(fmt.Sprintf("%v", snap[0].Sources) == "[base team ops]", "db source history")
		ck(snap[0].Meta != nil && snap[0].Meta.Tag == "ops-db" && string(snap[0].Meta.Payload) == "m-ops", "db meta")
		ck(snap[1].Name == "only-team" && snap[1].Addr == "team:9" && snap[1].Weight == 9, "onlyteam")
		ck(fmt.Sprintf("%v", snap[1].Sources) == "[team]" && snap[1].Meta == nil, "onlyteam sources")
		ck(snap[2].Name == "aaa-name" && snap[2].Addr == "bbb:6" && snap[2].Weight == 6, "equal-priority tie-break")
		ck(fmt.Sprintf("%v", snap[2].Sources) == "[base bbb aaa]", "queue source order")
		ck(snap[2].Meta == nil, "queue meta")
	}

	// Tombstones hide lower-priority layers; history still records them.
	if m, ok := reg.Lookup("cache"); ok {
		ck(false, fmt.Sprintf("cache survived tombstone: %+v", m))
	}
	if m, ok := reg.Lookup("legacy"); ok {
		ck(false, fmt.Sprintf("legacy survived tombstone: %+v", m))
	}
	hCache := reg.History("cache")
	ck(len(hCache) == 3 && hCache[0].Owner == "base" && hCache[1].Owner == "team" && hCache[2].Tombstone, "cache history")
	hDB := reg.History("db")
	ck(len(hDB) == 3 && hDB[0].Owner == "base" && hDB[1].Owner == "team" && hDB[2].Owner == "ops", "db history")

	// Repeated snapshots must preserve the Meta pointer, not copy it.
	snap2 := reg.Snapshot()
	var dbMeta *reg.Meta
	if len(snap) == 3 && len(snap2) == 3 {
		ck(snap[0].Meta == snap2[0].Meta, "db meta pointer identity across snapshots")
		dbMeta = snap[0].Meta
	}

	// Freeze rejects later contributions without changing the view.
	before := fingerprint(reg.Snapshot())
	err := reg.Register(reg.Contribution{Key: "db", Priority: 99, Owner: "late", Name: "late"})
	ck(errors.Is(err, reg.ErrFrozen), "post-freeze register error")
	ck(reg.Total() == 12, "post-freeze contribution count changed")
	ck(fingerprint(reg.Snapshot()) == before, "post-freeze snapshot changed")

	// Concurrent snapshot/lookup/history reads with explicit GC.
	workers := 8
	iters := 300 * rounds
	start := int(seedVal % 3)
	var wg sync.WaitGroup
	var bad int32
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < iters; i++ {
				if fingerprint(reg.Snapshot()) != wantFingerprint {
					atomic.AddInt32(&bad, 1)
					return
				}
				m, ok := reg.Lookup("db")
				if !ok || m.Name != "ops-db" || m.Meta == nil || m.Meta != dbMeta || m.Meta.Tag != "ops-db" {
					atomic.AddInt32(&bad, 1)
					return
				}
				key := []string{"db", "queue", "onlyteam"}[(start+w+i)%3]
				if _, ok := reg.Lookup(key); !ok {
					atomic.AddInt32(&bad, 1)
					return
				}
				if _, ok := reg.Lookup("cache"); ok {
					atomic.AddInt32(&bad, 1)
					return
				}
			}
		}(w)
	}
	gcStress(3)
	wg.Wait()
	ck(atomic.LoadInt32(&bad) == 0, "concurrent registry overlay read failed")

	gcStress(2)
	ck(fingerprint(reg.Snapshot()) == wantFingerprint, "overlay changed after GC")
	m, ok := reg.Lookup("db")
	ck(ok && m.Meta != nil && m.Meta == dbMeta && m.Meta.Tag == "ops-db" && string(m.Meta.Payload) == "m-ops", "meta payload changed after GC")
	ck(reg.Frozen() && reg.Total() == 12, "frozen state changed after GC")

	finish("l12_registry_overlay_merge")
}
