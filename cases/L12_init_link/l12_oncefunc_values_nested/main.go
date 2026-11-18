// L12: OnceValue/OnceValues/OnceFunc boundary graph. Lazy values are wired as
// package-level variables: derive calls base inside its own Once body, error
// values are cached, and panicking OnceFunc/OnceValue bodies must re-panic on
// every call while running only once. Concurrency and explicit GC verify the
// happens-before edges and the cached pointer/error identities.
package main

import (
	"errors"
	"fmt"
	"os"
	"runtime"
	"strconv"
	"sync"
	"sync/atomic"
)

type node struct {
	id      int
	name    string
	payload []byte
}

var (
	failed  int32
	gcSink  [][]byte
	seedVal int64
	seedStr string

	baseCalls   int32
	deriveCalls int32
	pairCalls   int32
	errCalls    int32
	fnCalls     int32
	nilCalls    int32
	plainCalls  int32
	panicSeen   int32

	cachedErr = errors.New("cached once failure")

	base = sync.OnceValue(func() *node {
		atomic.AddInt32(&baseCalls, 1)
		return &node{id: 1, name: "base", payload: []byte("base-payload")}
	})

	derive = sync.OnceValue(func() *node {
		atomic.AddInt32(&deriveCalls, 1)
		b := base()
		return &node{id: b.id + 1, name: "derive", payload: append([]byte(nil), b.payload...)}
	})

	pair = sync.OnceValues(func() (string, error) {
		atomic.AddInt32(&pairCalls, 1)
		return "pair-7", nil
	})

	failing = sync.OnceValues(func() (int, error) {
		atomic.AddInt32(&errCalls, 1)
		return 0, cachedErr
	})

	failFn = sync.OnceFunc(func() {
		atomic.AddInt32(&fnCalls, 1)
		panic("once-fn-boom")
	})

	nilValue = sync.OnceValue(func() *node {
		atomic.AddInt32(&nilCalls, 1)
		return nil
	})

	plainOnce sync.Once
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

func catchPanic(f func()) (recovered any) {
	defer func() { recovered = recover() }()
	f()
	return nil
}

func main() {
	seedVal = readSeed()
	seedStr = strconv.FormatInt(seedVal, 10)
	rounds := readRounds()

	// Nested lazy values: derive's body depends on base's Once.
	d := derive()
	ck(d != nil && d.id == 2 && d.name == "derive", "derive value")
	ck(baseCalls == 1 && deriveCalls == 1, "nested Once body counts after first call")
	b := base()
	ck(b != nil && b.id == 1 && string(b.payload) == "base-payload", "base value")
	ck(baseCalls == 1, "base body re-ran")
	ck(d != b, "base and derive should be distinct nodes")
	ck(derive() == d, "derive identity stable")

	s, err := pair()
	ck(err == nil && s == "pair-7", "OnceValues pair")
	ck(pairCalls == 1, "pair body re-ran")
	s2, err2 := pair()
	ck(s2 == s && err2 == nil && pairCalls == 1, "OnceValues cached pair")

	v, ev := failing()
	ck(v == 0 && errors.Is(ev, cachedErr), "OnceValues cached error")
	ck(errCalls == 1, "error body re-ran")
	v2, ev2 := failing()
	ck(v2 == 0 && ev2 == cachedErr && errCalls == 1, "cached error identity")

	ck(nilValue() == nil && nilCalls == 1, "OnceValue nil result")

	// A panicking OnceFunc body runs once; every caller observes the panic.
	for i := 0; i < 3; i++ {
		r := catchPanic(failFn)
		ck(r == "once-fn-boom", fmt.Sprintf("OnceFunc panic value %v", r))
	}
	ck(fnCalls == 1, "OnceFunc body ran more than once")
	ck(atomic.LoadInt32(&panicSeen) == 0, "unexpected panic counter")

	plainOnce.Do(func() { atomic.AddInt32(&plainCalls, 1) })
	plainOnce.Do(func() { atomic.AddInt32(&plainCalls, 1) })
	ck(plainCalls == 1, "plain Once body ran more than once")

	// Concurrent first callers must all observe the same cached graph. The
	// intent is that no checker ever sees a partially initialized value.
	workers := 8 * rounds
	iters := 300 * rounds
	offset := int(seedVal % 17)
	var wg sync.WaitGroup
	var bad int32
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < iters; i++ {
				n := derive()
				if n != d || n.name != "derive" || n.payload[0] != 'b' {
					atomic.AddInt32(&bad, 1)
					return
				}
				if base() != b || string(base().payload) != "base-payload" {
					atomic.AddInt32(&bad, 1)
					return
				}
				gotS, gotErr := pair()
				if gotErr != nil || gotS != "pair-7" {
					atomic.AddInt32(&bad, 1)
					return
				}
				val, gotErr := failing()
				if val != 0 || gotErr != cachedErr {
					atomic.AddInt32(&bad, 1)
					return
				}
				if nilValue() != nil {
					atomic.AddInt32(&bad, 1)
					return
				}
				r := catchPanic(failFn)
				if r != "once-fn-boom" {
					atomic.AddInt32(&bad, 1)
					return
				}
				if (w+i+offset)%17 == 0 {
					buf := make([]byte, 512)
					buf[0] = byte(i)
					if buf[0] != byte(i) {
						atomic.AddInt32(&bad, 1)
						return
					}
				}
			}
		}(w)
	}
	for r := 0; r < 3; r++ {
		for i := 0; i < 8; i++ {
			buf := make([]byte, 2048)
			buf[0] = byte(i + r)
			gcSink = append(gcSink[:0], buf)
		}
		runtime.GC()
	}
	wg.Wait()
	ck(atomic.LoadInt32(&bad) == 0, "concurrent Once graph check failed")

	// Exactly one body execution for each lazy value despite the storm.
	ck(baseCalls == 1, "base body count != 1 under concurrency")
	ck(deriveCalls == 1, "derive body count != 1 under concurrency")
	ck(pairCalls == 1, "pair body count != 1 under concurrency")
	ck(errCalls == 1, "error body count != 1 under concurrency")
	ck(fnCalls == 1, "panic body count != 1 under concurrency")
	ck(nilCalls == 1, "nil body count != 1 under concurrency")

	gcStress(2)
	ck(derive() == d && d.payload[0] == 'b' && len(d.payload) == 12, "derive changed after GC")
	ck(base() == b && b.name == "base", "base changed after GC")
	gotS, gotErr := pair()
	ck(gotS == "pair-7" && gotErr == nil, "pair changed after GC")
	val, gotErr := failing()
	ck(val == 0 && gotErr == cachedErr, "error changed after GC")
	ck(nilValue() == nil && nilCalls == 1, "nil value body re-ran after GC")
	ck(fnCalls == 1 && pairCalls == 1, "body counts changed after GC")

	finish("l12_oncefunc_values_nested")
}
