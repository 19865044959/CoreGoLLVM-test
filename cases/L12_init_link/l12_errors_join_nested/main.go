// L12: errors.Join tree boundaries built from init-time error slots. The fixed
// slot order makes the aggregate deterministic even though cross-file init
// order is not. The case checks nested Join trees, multi-%w wrapping, custom
// Is matching, duplicate joins, nil elision, pointer payload identity and
// concurrent reads with explicit GC.
package main

import (
	"errors"
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

const wantJoinedText = "load: alpha: entry 2: bad value\n" +
	"beta: panic: panic sentinel\n" +
	"gamma: gamma\n" +
	"code 7\n" +
	"bad value\n" +
	"gamma\n" +
	"delta: alpha"

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

func main() {
	seedVal = readSeed()
	seedStr = strconv.FormatInt(seedVal, 10)

	ck(len(initOrder) == 5, fmt.Sprintf("init slots filled %d times", len(initOrder)))
	ordered := append([]string(nil), initOrder...)
	sort.Strings(ordered)
	ck(fmt.Sprintf("%v", ordered) == "[alpha beta delta epsilon gamma]", "init slot set")
	ck(slotA != nil && slotB != nil && slotC != nil && slotD != nil && slotE == nil, "init slot nilness")

	joined := errors.Join(slotA, nil, slotB, slotC, slotD)
	ck(joined != nil, "joined error is nil")
	ck(joined.Error() == wantJoinedText, fmt.Sprintf("joined text %q", joined.Error()))

	// Top-level tree shape: 4 children, nil slot elided.
	top, ok := joined.(interface{ Unwrap() []error })
	ck(ok, "joined does not implement Unwrap() []error")
	if ok {
		kids := top.Unwrap()
		ck(len(kids) == 4, fmt.Sprintf("top unwrap length %d", len(kids)))
		ck(kids[0] == slotA && kids[1] == slotB && kids[2] == slotC && kids[3] == slotD, "top child identity/order")
		if len(kids) == 4 {
			ck(len(kids[0].(interface{ Unwrap() error }).Unwrap().(interface{ Unwrap() []error }).Unwrap()) == 2, "multi-%w child shape")
			gkids := kids[2].(interface{ Unwrap() []error }).Unwrap()
			ck(len(gkids) == 2, "gamma join child count")
			dkids := kids[3].(interface{ Unwrap() []error }).Unwrap()
			ck(len(dkids) == 2, "delta join child count")
			ck(len(dkids[0].(interface{ Unwrap() []error }).Unwrap()) == 2, "nested join child count")
			ck(dkids[1].(interface{ Unwrap() error }).Unwrap() == errAlpha, "delta wrap unwrap")
		}
	}

	// errors.Is traverses nested joins, multi-%w and custom Is.
	for _, target := range []error{errAlpha, errBadValue, errPanicSentinel, errGamma, errCode} {
		ck(errors.Is(joined, target), "errors.Is failed for "+target.Error())
	}
	ck(!errors.Is(joined, errUnrelated), "errors.Is matched unrelated sentinel")

	// errors.As finds the first taggedError and the codeError payloads.
	var te *taggedError
	ck(errors.As(joined, &te), "errors.As taggedError")
	if te != nil {
		ck(te.Detail == alphaDetail, "taggedError detail pointer identity")
		ck(te.Detail != nil && te.Detail.tag == "alpha-payload" && string(te.Detail.payload) == "payload-a", "taggedError payload")
	}
	var ce *codeError
	ck(errors.As(joined, &ce), "errors.As codeError")
	ck(ce != nil && ce.code == 7 && ce.Detail != nil && ce.Detail.tag == "code-payload", "codeError fields")

	// Join edge cases: empty, single, duplicate and all-nil.
	ck(errors.Join() == nil, "empty Join")
	ck(errors.Join(nil, nil) == nil, "all-nil Join")
	single := errors.Join(slotA)
	ck(single.Error() == slotA.Error(), "single Join text")
	dup := errors.Join(slotA, slotA)
	ck(dup.Error() == slotA.Error()+"\n"+slotA.Error(), "duplicate Join text")
	ck(len(dup.(interface{ Unwrap() []error }).Unwrap()) == 2, "duplicate Join preserves both children")
	ck(errors.Is(dup, errBadValue), "duplicate Join Is")

	// Concurrent read of the immutable error tree with explicit GC.
	workers := 8
	iters := 300
	start := int(seedVal % 4)
	var wg sync.WaitGroup
	var bad int32
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < iters; i++ {
				if joined.Error() != wantJoinedText {
					atomic.AddInt32(&bad, 1)
					return
				}
				target := []error{errBadValue, errAlpha, errPanicSentinel, errGamma, errCode}[(start+w+i)%5]
				if !errors.Is(joined, target) {
					atomic.AddInt32(&bad, 1)
					return
				}
				var got *taggedError
				if !errors.As(joined, &got) || got.Detail != alphaDetail || string(got.Detail.payload) != "payload-a" {
					atomic.AddInt32(&bad, 1)
					return
				}
			}
		}(w)
	}
	gcStress(3)
	wg.Wait()
	ck(atomic.LoadInt32(&bad) == 0, "concurrent error-tree read failed under GC")

	gcStress(2)
	ck(joined.Error() == wantJoinedText, "joined text changed after GC")
	ck(errors.Is(joined, errBadValue) && errors.Is(joined, errCode), "sentinel matching changed after GC")
	var te2 *taggedError
	ck(errors.As(joined, &te2) && te2.Detail == alphaDetail && string(te2.Detail.payload) == "payload-a", "detail payload changed after GC")
	var ce2 *codeError
	ck(errors.As(joined, &ce2) && ce2.code == 7 && ce2.Detail.tag == "code-payload", "codeError changed after GC")

	finish("l12_errors_join_nested")
}
