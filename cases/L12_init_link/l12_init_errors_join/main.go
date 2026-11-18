// L12: init 的错误路径聚合。三个 init 分别产生“包装错误”“recover 后的
// panic 错误”“无错误”，按固定 slot 顺序 errors.Join，因此错误树与文本不依赖
// 未定义的跨文件 init 顺序。错误详情携带指针 payload，GC 后校验身份与内容。
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

	sortedMarks := append([]string(nil), initMarks...)
	sort.Strings(sortedMarks)
	ck(len(sortedMarks) == 4, fmt.Sprintf("init mark count %d, want 4", len(sortedMarks)))
	if len(sortedMarks) == 4 {
		ck(sortedMarks[0] == "alpha" && sortedMarks[1] == "beta" &&
			sortedMarks[2] == "gamma" && sortedMarks[3] == "main",
			fmt.Sprintf("init marks %v", sortedMarks))
	}

	err := aggregate(slots[:])
	ck(err != nil, "aggregated init error is nil")
	wantText := "alpha: bad entry 2: invalid value: -5\nbeta: recovered panic: panic sentinel"
	if err != nil {
		ck(err.Error() == wantText, fmt.Sprintf("aggregate text %q, want %q", err.Error(), wantText))
	}
	ck(errors.Is(err, errBadValue), "aggregate does not unwrap errBadValue")
	ck(errors.Is(err, errPanicSentinel), "aggregate does not unwrap panic sentinel")
	ck(!errors.Is(err, errNegative), "aggregate unexpectedly matches unrelated sentinel")

	var te *taggedError
	ck(errors.As(err, &te), "errors.As failed on aggregate")
	ck(te != nil && te.Op == "alpha" && te.Detail == detailAlpha &&
		te.Detail != nil && te.Detail.Tag == "alpha" &&
		string(te.Detail.Payload) == "alpha-payload", "errors.As retrieved wrong first error/detail")

	ju, ok := err.(interface{ Unwrap() []error })
	ck(ok, "aggregate is not a joined multi-error")
	if ok {
		children := ju.Unwrap()
		ck(len(children) == 2, fmt.Sprintf("aggregate children %d, want 2", len(children)))
		if len(children) == 2 {
			beta, ok := children[1].(*taggedError)
			ck(ok && beta.Op == "beta" && errors.Is(beta, errPanicSentinel), "second aggregate child is not beta panic error")
		}
	}

	// Empty joins and pure validation errors.
	ck(aggregate([]error{nil, nil, nil}) == nil, "all-nil aggregate is not nil")
	ck(aggregateText(slots[:1]) == "alpha: bad entry 2: invalid value: -5", "single-slot aggregate text")
	ck(aggregateText(slots[:2]) == "alpha: bad entry 2: invalid value: -5\nbeta: recovered panic: panic sentinel", "two-slot aggregate text")
	ck(validateFixture(validFixture()) == nil, "valid fixture rejected")
	if e := validateFixture(badFixture()); !errors.Is(e, errNegative) || e.Error() != `entry 1 "beta": negative value` {
		ck(false, fmt.Sprintf("bad fixture error %v", e))
	}
	if e := validateFixture(dupFixture()); !errors.Is(e, errDupKey) || e.Error() != `entry 1 "alpha": duplicate key: dup` {
		ck(false, fmt.Sprintf("dup fixture error %v", e))
	}
	if e := validateFixture(emptyNameFixture()); !errors.Is(e, errEmptyKey) || e.Error() != "entry 0: empty key" {
		ck(false, fmt.Sprintf("empty fixture error %v", e))
	}

	// Concurrent reads of the immutable error tree while GC runs.
	var wg sync.WaitGroup
	var bad int32
	workers := 8
	start := int(seedVal % 6)
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < 200; i++ {
				if err.Error() != wantText || !errors.Is(err, errBadValue) || !errors.Is(err, errPanicSentinel) {
					atomic.AddInt32(&bad, 1)
					return
				}
				var got *taggedError
				if !errors.As(err, &got) || got.Detail != detailAlpha || string(got.Detail.Payload) != "alpha-payload" {
					atomic.AddInt32(&bad, 1)
					return
				}
				_ = (start + w + i) % 6
			}
		}(w)
	}
	for r := 0; r < 3; r++ {
		for i := 0; i < 16; i++ {
			b := make([]byte, 8192)
			b[i%len(b)] = byte(r)
			gcSink = append(gcSink[:0], b)
		}
		runtime.GC()
	}
	wg.Wait()
	gcSink = gcSink[:0]
	ck(atomic.LoadInt32(&bad) == 0, "concurrent error tree read failed under GC")

	gcStress()
	var after *taggedError
	ck(errors.As(err, &after) && after.Detail == detailAlpha, "error detail pointer changed after GC")
	ck(after != nil && string(after.Detail.Payload) == "alpha-payload", "error detail payload damaged after GC")
	ck(err.Error() == wantText, "aggregate text changed after GC")
	ck(len(initMarks) == 4, "init marks changed after GC")

	finish("l12_init_errors_join")
}
