// L12: build-selection matrix combining filename GOOS/GOARCH suffixes,
// compound //go:build constraints, the unix meta tag, the cgo/nocgo tag pair
// and excluded custom/ignore tags. Every selected file contributes a
// package-variable mark and an init mark; the case asserts the exact selected
// set for the current platform and that all variable initialization completes
// before the first init.
package main

import (
	"fmt"
	"os"
	"runtime"
	"sort"
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

func countMark(marks []string, want string) int {
	n := 0
	for _, m := range marks {
		if m == want {
			n++
		}
	}
	return n
}

func hasStem(stems []string, want string) bool {
	for _, s := range stems {
		if s == want {
			return true
		}
	}
	return false
}

func main() {
	seedVal = readSeed()
	seedStr = strconv.FormatInt(seedVal, 10)

	ck(atomic.LoadInt32(&phaseOK) == 1, "package variables not all initialized before init")
	stems := expectedStems()
	ck(len(varMarks) == len(stems)+1, fmt.Sprintf("var mark count %d want %d", len(varMarks), len(stems)+1))
	ck(len(initMarks) == len(stems)+1, fmt.Sprintf("init mark count %d want %d", len(initMarks), len(stems)+1))

	for _, s := range stems {
		ck(countMark(varMarks, s+".var") == 1, "missing/duplicate var mark "+s)
		ck(countMark(initMarks, s+".init") == 1, "missing/duplicate init mark "+s)
	}

	// Exactly one of the cgo/nocgo pair is selected; niceness is not assumed.
	cgoVars := countMark(varMarks, "cgo.var") + countMark(varMarks, "nocgo.var")
	cgoInits := countMark(initMarks, "cgo.init") + countMark(initMarks, "nocgo.init")
	ck(cgoVars == 1, fmt.Sprintf("cgo var pair selected %d", cgoVars))
	ck(cgoInits == 1, fmt.Sprintf("cgo init pair selected %d", cgoInits))
	ck(countMark(varMarks, "cgo.var") == countMark(initMarks, "cgo.init"), "cgo var/init mismatch")
	ck(countMark(varMarks, "nocgo.var") == countMark(initMarks, "nocgo.init"), "nocgo var/init mismatch")

	// Files excluded by filename suffix must be absent unless the host is that
	// platform; custom and ignore tags must never contribute.
	if runtime.GOOS != "windows" {
		ck(countMark(varMarks, "suffix_windows.var") == 0 && countMark(initMarks, "suffix_windows.init") == 0, "windows suffix selected")
	}
	if runtime.GOARCH != "386" {
		ck(countMark(varMarks, "suffix_386.var") == 0 && countMark(initMarks, "suffix_386.init") == 0, "386 suffix selected")
	}
	ck(!hasStem(stems, "suffix_windows") || runtime.GOOS == "windows", "windows stem expectation")
	ck(!hasStem(stems, "suffix_386") || runtime.GOARCH == "386", "386 stem expectation")
	ck(countMark(varMarks, "off.var") == 0 && countMark(initMarks, "off.init") == 0, "disabled custom tag file selected")
	ck(countMark(varMarks, "ignore.var") == 0 && countMark(initMarks, "ignore.init") == 0, "ignore file selected")

	// All marks are well formed and every platform contributes both phases.
	for _, m := range varMarks {
		if len(m) < 4 || m[len(m)-4:] != ".var" {
			ck(false, fmt.Sprintf("malformed var mark %q", m))
			break
		}
	}
	for _, m := range initMarks {
		if len(m) < 5 || m[len(m)-5:] != ".init" {
			ck(false, fmt.Sprintf("malformed init mark %q", m))
			break
		}
	}

	// Deterministic sorted views (cross-file init order is not asserted).
	vs := append([]string(nil), varMarks...)
	is := append([]string(nil), initMarks...)
	sort.Strings(vs)
	sort.Strings(is)
	ck(len(vs) == len(varMarks), "sorted var view")
	ck(len(is) == len(initMarks), "sorted init view")

	// Concurrent read-only checks with explicit GC.
	var wg sync.WaitGroup
	var bad int32
	offset := int(seedVal % 7)
	for w := 0; w < 8; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < 300; i++ {
				if len(varMarks) != len(stems)+1 || len(initMarks) != len(stems)+1 {
					atomic.AddInt32(&bad, 1)
					return
				}
				if countMark(varMarks, "off.var") != 0 || countMark(initMarks, "ignore.init") != 0 {
					atomic.AddInt32(&bad, 1)
					return
				}
				if len(stems) == 0 {
					continue
				}
				if countMark(varMarks, stems[(w+i+offset)%len(stems)]+".var") != 1 {
					atomic.AddInt32(&bad, 1)
					return
				}
				if countMark(initMarks, stems[(w+i+offset)%len(stems)]+".init") != 1 {
					atomic.AddInt32(&bad, 1)
					return
				}
			}
		}(w)
	}
	gcStress(2 + int(seedVal%3))
	wg.Wait()
	ck(atomic.LoadInt32(&bad) == 0, "concurrent build-selection read failed under GC")

	gcStress(2)
	ck(len(varMarks) == len(stems)+1 && len(initMarks) == len(stems)+1, "mark counts changed after GC")
	ck(atomic.LoadInt32(&phaseOK) == 1, "phase order changed after GC")
	ck(countMark(varMarks, "off.var") == 0 && countMark(initMarks, "ignore.init") == 0, "excluded marks changed after GC")

	finish("l12_tags_suffix_matrix")
}
