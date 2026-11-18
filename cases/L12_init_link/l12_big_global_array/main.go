// L12: large package-level array of structs containing strings and pointers.
// The array is built by a function call during variable initialization, then
// mutated after main starts. Every element is rescanned after explicit GCs.
// P2: TORTURE_ROUNDS scales mutation/GC rounds (the array size stays fixed).
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

func tortureRounds() int {
	s := os.Getenv("TORTURE_ROUNDS")
	if s == "" {
		return 1
	}
	v, err := strconv.Atoi(s)
	if err != nil || v < 1 {
		return 1
	}
	if v > 16 {
		v = 16
	}
	return v
}

func main() {
	seedVal = readSeed()
	seedStr = strconv.FormatInt(seedVal, 10)
	rounds := tortureRounds()
	start := int(seedVal % 137)

	ck(atomic.LoadInt32(&recordBuilds) == 1, "large global array built exactly once")
	ck(len(records) == recordLen, "global array length")
	validate(0, start)

	// Mutate a deterministic subset for several rounds. Each append also
	// stores a new pointer into a global slice, exercising the write barrier.
	for r := 0; r < rounds; r++ {
		for i := start; i < recordLen; i += 137 {
			records[i].score++
			records[i].tag[15] = byte(r + 1)
		}
		p := &payload{}
		for j := range p.data {
			p.data[j] = byte(200 + r + j)
		}
		mutated = append(mutated, p)
		runtime.GC()
	}
	validate(rounds, start)
	ck(int64(rounds)*int64(touchedCount(start)) > 0, "mutation touched at least one entry")

	// Concurrent full scans of the global array while the main goroutine
	// allocates garbage and triggers GC.
	const workers = 8
	const scans = 24
	wantSum := scoreSum(rounds, start)
	var wg sync.WaitGroup
	var bad int32
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for s := 0; s < scans; s++ {
				var sum int64
				for i := 0; i < recordLen; i++ {
					e := &records[i]
					sum += e.score
					if e.payload == nil {
						atomic.AddInt32(&bad, 1)
						return
					}
					if i%257 == 0 && e.payload.data[0] != byte(i*11) {
						atomic.AddInt32(&bad, 1)
						return
					}
				}
				if sum != wantSum {
					atomic.AddInt32(&bad, 1)
					return
				}
				p := records[0].payload
				n := 0
				for p != nil {
					n++
					p = p.next
				}
				if n != recordLen {
					atomic.AddInt32(&bad, 1)
					return
				}
			}
		}(w)
	}
	for i := 0; i < 6; i++ {
		for j := 0; j < 8; j++ {
			b := make([]byte, 8192)
			b[j] = byte(i)
			gcSink = append(gcSink[:0], b)
		}
		runtime.GC()
	}
	wg.Wait()
	ck(atomic.LoadInt32(&bad) == 0, "concurrent array scans under GC")
	gcSink = gcSink[:0]

	gcStress()
	validate(rounds, start)
	ck(atomic.LoadInt32(&recordBuilds) == 1, "array not rebuilt after GC")
	ck(len(mutated) == rounds && mutated[rounds-1] != nil, "mutated slice survives GC")

	finish("l12_big_global_array")
}
