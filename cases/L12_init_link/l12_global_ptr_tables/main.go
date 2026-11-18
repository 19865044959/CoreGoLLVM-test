// L12: package-level map/slice pointer tables and their cross references.
// The global tables are built by a function call during package variable
// initialization; insertion into the tables happens after main starts and
// before concurrent read-only checks + GC.
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

func main() {
	seedVal = readSeed()
	seedStr = strconv.FormatInt(seedVal, 10)

	root := tables.ordered[0]

	// State built entirely during package variable initialization.
	validate(tables, 0, seedVal)
	ck(tables.tail != nil && tables.tail.id == builtinCount-1, "initial tail")

	// Append new entries to every table: map, ordered slice, groups,
	// pointer-keyed cross table, payload table and linked list.
	k := 1 + int(seedVal%3)
	baseID := 1000 + int(seedVal%97)
	for j := 0; j < k; j++ {
		tables.insert(newEntry(baseID + j))
	}
	validate(tables, 0, seedVal)
	ck(len(tables.ordered) == builtinCount+k, "inserted entry count")
	ck(tables.tail.id == baseID+k-1, "inserted tail")

	// Mutate scalars reachable through the global tables, then run GC. The
	// payload pointers themselves must stay stable.
	for _, e := range tables.ordered {
		e.score += 100
	}
	validate(tables, 100, seedVal)

	const workers = 8
	start := int(seedVal % int64(len(tables.ordered)))
	var wg sync.WaitGroup
	var bad int32
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < 300; i++ {
				idx := (start + w + i) % len(tables.ordered)
				e := tables.ordered[idx]
				if tables.byID[e.id] != e {
					atomic.AddInt32(&bad, 1)
					return
				}
				if tables.cross[e][0] == nil || tables.payloads[idx][e.name] != e {
					atomic.AddInt32(&bad, 1)
					return
				}
				if e.payload == nil || e.payload[0] != byte(e.id*7) {
					atomic.AddInt32(&bad, 1)
					return
				}
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
	ck(atomic.LoadInt32(&bad) == 0, "concurrent table reads under GC")
	gcSink = gcSink[:0]

	gcStress()
	validate(tables, 100, seedVal)
	ck(tables.ordered[0] == root, "root pointer identity after GC")
	ck(tables.byID[baseID] != nil && tables.cross[tables.byID[baseID]][0] != nil,
		"inserted pointers survive GC")

	finish("l12_global_ptr_tables")
}
