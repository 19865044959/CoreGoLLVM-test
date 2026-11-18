// L12: typed constants and method sets. Named constant types gain methods;
// value-receiver methods are in the method set of both T and *T, while
// pointer-receiver methods are only in *T. Constants themselves are not
// addressable, so pointer-receiver methods cannot be called on them directly.
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

	// Typed constant values and their methods.
	ck(levelDebug == 0 && levelInfo == 1 && levelWarn == 2 && levelError == 3,
		"iota typed constants")
	const promoted = levelWarn + 1
	ck(promoted == levelError, "constant arithmetic keeps the named type")
	ck(levelWarn.String() == "warn", "value-receiver String on a constant")
	ck(levelError.Weight() == 30, "value-receiver Weight on a constant")
	ck(labelAPI.Upper() == "API" && labelDB.Upper() == "DB", "label methods on constants")
	ck(labelAPI.Kind() == "label", "label Kind")
	ck(flagAll.Has(flagWrite) && flagAll.Has(flagRead|flagExec), "flag combinations")
	ck(flagAll.Count() == 3 && !flagRead.Has(flagWrite), "flag counting and disjointness")

	// fmt uses the Stringer method set.
	ck(fmt.Sprint(levelWarn) == "warn", "fmt.Sprint uses level.String")
	ck(fmt.Sprint(labelDB) == "db", "fmt.Sprint uses label.String")
	ck(fmt.Sprint(flagAll) == "flags(7)", "fmt.Sprint uses flags.String")

	// Interface embedding: weighter embeds stringer.
	ck(globalWeighter.Weight() == 10 && globalWeighter.String() == "info",
		"interface embedding dispatches both methods")

	// Method expressions and method values.
	weight := level.Weight
	ck(weight(levelInfo) == 10, "method expression value receiver")
	upperFn := label.Upper
	ck(upperFn(labelDB) == "DB", "method expression on label")
	mv := levelError.String
	ck(mv() == "error", "method value on a constant")

	// Pointer receiver: present on *level only.
	v := levelInfo
	p := &v
	ck(p.Promote() == levelWarn && v == levelWarn, "pointer receiver mutates the variable")
	iface, ok := any(v).(promoter)
	ck(!ok && iface == nil, "level value is not a promoter")
	iface, ok = any(p).(promoter)
	ck(ok && iface != nil && iface.Promote() == levelError && v == levelError,
		"*level implements promoter")

	// Global interface slice initialized from typed constants.
	want := []string{"debug", "info", "warn", "error", "api", "db", "flags(1)"}
	if len(globalStringers) != len(want) {
		ck(false, "global Stringer slice length")
	} else {
		start := int(seedVal % int64(len(want)))
		for i := 0; i < len(want); i++ {
			j := (start + i) % len(want)
			ck(globalStringers[j].String() == want[j], "global Stringer dispatch")
		}
	}

	// Interface equality compares dynamic type and value.
	ck(weighter(levelInfo) == globalWeighter, "interface equality for same type and value")
	ck(weighter(levelInfo) != weighter(levelWarn), "interface inequality for different value")

	// Type switch over constant-derived interface values.
	counts := map[string]int{}
	for _, x := range []any{levelDebug, labelAPI, flagAll, levelWarn} {
		switch x.(type) {
		case level:
			counts["level"]++
		case label:
			counts["label"]++
		case flags:
			counts["flags"]++
		default:
			counts["other"]++
		}
	}
	ck(counts["level"] == 2 && counts["label"] == 1 && counts["flags"] == 1 &&
		counts["other"] == 0, "type switch on named constant types")

	// Concurrent reads of interface globals while GC runs.
	const workers = 8
	var wg sync.WaitGroup
	var bad int32
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < 300; i++ {
				if globalStringers[(w+i)%len(globalStringers)].String() == "" {
					atomic.AddInt32(&bad, 1)
					return
				}
				if globalWeighter.Weight() != 10 {
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
	ck(atomic.LoadInt32(&bad) == 0, "interface dispatch under GC")
	gcSink = gcSink[:0]

	gcStress()
	ck(globalWeighter.Weight() == 10 && globalStringers[4].String() == "api",
		"interface globals survive GC")
	ck(levelError.String() == "error" && flagAll.Count() == 3,
		"constant methods unchanged after GC")

	finish("l12_const_methodset")
}
