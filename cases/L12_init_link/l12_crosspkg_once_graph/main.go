// L12: 跨包 sync.Once 依赖图（module）。base 是叶子，left/right 各自在
// Once body 中等待 base，root 再等待两个分支。imports 形成的 init 偏序为
// base -> left/right -> root -> main；left 与 right 的相对顺序未定义，不断言。
package main

import (
	"fmt"
	"os"
	"runtime"
	"strconv"
	"sync"
	"sync/atomic"

	"l12_crosspkg_once_graph/base"
	"l12_crosspkg_once_graph/left"
	"l12_crosspkg_once_graph/right"
	"l12_crosspkg_once_graph/root"
	"l12_crosspkg_once_graph/sink"
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
	if v > 8 {
		return 8
	}
	return v
}

func markIndex(marks []string, want string) int {
	for i, s := range marks {
		if s == want {
			return i
		}
	}
	return -1
}

func markCount(marks []string, want string) int {
	n := 0
	for _, s := range marks {
		if s == want {
			n++
		}
	}
	return n
}

func init() {
	sink.Add("main.init")
}

func main() {
	seedVal = readSeed()
	seedStr = strconv.FormatInt(seedVal, 10)
	rounds := tortureRounds()

	// Import-driven init order is partially ordered by the import graph.
	marks := sink.Marks()
	ck(len(marks) == 5, fmt.Sprintf("init mark count %d, want 5", len(marks)))
	for _, m := range []string{"base.init", "left.init", "right.init", "root.init", "main.init"} {
		ck(markCount(marks, m) == 1, "missing or duplicate init mark "+m)
	}
	bi := markIndex(marks, "base.init")
	li := markIndex(marks, "left.init")
	ri := markIndex(marks, "right.init")
	roi := markIndex(marks, "root.init")
	mi := markIndex(marks, "main.init")
	ck(bi >= 0 && li >= 0 && bi < li, "base init must precede left init")
	ck(bi >= 0 && ri >= 0 && bi < ri, "base init must precede right init")
	ck(li >= 0 && roi >= 0 && li < roi, "left init must precede root init")
	ck(ri >= 0 && roi >= 0 && ri < roi, "right init must precede root init")
	ck(roi >= 0 && mi >= 0 && roi < mi, "root init must precede main init")

	// No lazy builder may have run during package initialization.
	ck(base.Builds() == 0 && left.Builds() == 0 && right.Builds() == 0 && root.Builds() == 0,
		"sync.Once builder ran during init")
	ck(base.InitBuilds == 0 && left.InitBuilds == 0 && right.InitBuilds == 0 && root.InitBuilds == 0,
		"init observed a pre-built node")

	workers := 32 * rounds
	if workers > 128 {
		workers = 128
	}
	iters := 64 * rounds
	if iters > 2048 {
		iters = 2048
	}
	var wg sync.WaitGroup
	var bad int32
	start := int(seedVal % 8)
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < iters; i++ {
				r := root.Ensure()
				l := left.Ensure()
				rr := right.Ensure()
				b := base.Ensure()
				if r == nil || r.ID != 100 || r.Label != "root" ||
					l == nil || l.ID != 21 || l.Label != "left" ||
					rr == nil || rr.ID != 22 || rr.Label != "right" ||
					b == nil || b.ID != 11 || b.Label != "base" {
					atomic.AddInt32(&bad, 1)
					return
				}
				if r.Left != l || r.Right != rr || l.Base != b || rr.Base != b {
					atomic.AddInt32(&bad, 1)
					return
				}
				_ = (start + w + i) % 8
			}
		}(w)
	}
	for i := 0; i < 4; i++ {
		for j := 0; j < 16; j++ {
			buf := make([]byte, 8192)
			buf[j%len(buf)] = byte(i)
			gcSink = append(gcSink[:0], buf)
		}
		runtime.GC()
	}
	wg.Wait()
	gcSink = gcSink[:0]
	ck(atomic.LoadInt32(&bad) == 0, "concurrent cross-package Once graph verification failed")

	ck(base.Builds() == 1 && left.Builds() == 1 && right.Builds() == 1 && root.Builds() == 1,
		"a Once body did not run exactly once")
	marks = sink.Marks()
	ck(len(marks) == 9, fmt.Sprintf("mark count after builds %d, want 9", len(marks)))
	for _, m := range []string{"base.build", "left.build", "right.build", "root.build"} {
		ck(markCount(marks, m) == 1, "missing or duplicate build mark "+m)
	}
	bb := markIndex(marks, "base.build")
	lb := markIndex(marks, "left.build")
	rb := markIndex(marks, "right.build")
	rob := markIndex(marks, "root.build")
	ck(bb >= 0 && lb >= 0 && bb < lb, "base build must complete before left build")
	ck(bb >= 0 && rb >= 0 && bb < rb, "base build must complete before right build")
	ck(lb >= 0 && rob >= 0 && lb < rob, "left build must complete before root build")
	ck(rb >= 0 && rob >= 0 && rb < rob, "right build must complete before root build")

	// Second calls return the same graph and do not rebuild.
	r1 := root.Ensure()
	l1 := left.Ensure()
	rr1 := right.Ensure()
	b1 := base.Ensure()
	ck(r1 == root.Ensure() && l1 == left.Ensure() && rr1 == right.Ensure() && b1 == base.Ensure(),
		"second Ensure returned a different pointer")
	ck(base.Builds() == 1 && left.Builds() == 1 && right.Builds() == 1 && root.Builds() == 1,
		"second Ensure re-ran a Once body")

	gcStress()
	ck(r1.Left == l1 && r1.Right == rr1 && l1.Base == b1 && rr1.Base == b1,
		"cross-package pointer graph damaged by GC")
	ck(len(sink.Marks()) == 9, "build/init marks changed after GC")

	finish("l12_crosspkg_once_graph")
}
