// L12: sync.Once 依赖 DAG。四个 Once 组成 base -> {left,right} -> root 的
// 有向无环依赖；Once body 内部调用依赖的 Once.Do。32 个 goroutine 并发首次
// 调用 root，主 goroutine 同时制造垃圾并显式 GC，验证每个节点只构建一次、
// 依赖先于依赖者、所有 goroutine 看到同一张指针图。TORTURE_ROUNDS 放大
// 并发规模。
package main

import (
	"fmt"
	"os"
	"runtime"
	"strconv"
	"sync"
	"sync/atomic"
)

type payload struct {
	owner string
	blob  []byte
}

type node struct {
	name        string
	value       int
	p           *payload
	base        *node
	left, right *node
}

var (
	failed int32
	gcSink [][]byte

	onceBase, onceLeft, onceRight, onceRoot sync.Once
	nodeBase, nodeLeft, nodeRight, nodeRoot *node

	buildCount [4]int32
	traceMu    sync.Mutex
	dagTrace   []string

	seedVal int64
	seedStr string
)

func record(s string) {
	traceMu.Lock()
	dagTrace = append(dagTrace, s)
	traceMu.Unlock()
}

func traceIndex(s string) int {
	traceMu.Lock()
	defer traceMu.Unlock()
	for i, v := range dagTrace {
		if v == s {
			return i
		}
	}
	return -1
}

func traceCount(s string) int {
	traceMu.Lock()
	defer traceMu.Unlock()
	n := 0
	for _, v := range dagTrace {
		if v == s {
			n++
		}
	}
	return n
}

func ensureBase() *node {
	onceBase.Do(func() {
		n := &node{
			name:  "base",
			value: 11,
			p:     &payload{owner: "base", blob: make([]byte, 32768)},
		}
		n.p.blob[0] = 1
		runtime.GC() // GC inside an init-once body; no locks are held here.
		nodeBase = n
		atomic.AddInt32(&buildCount[0], 1)
		record("base")
	})
	return nodeBase
}

func ensureLeft() *node {
	onceLeft.Do(func() {
		b := ensureBase()
		n := &node{
			name:  "left",
			value: b.value + 1,
			p:     &payload{owner: "left", blob: make([]byte, 4096)},
			base:  b,
		}
		nodeLeft = n
		atomic.AddInt32(&buildCount[1], 1)
		record("left")
	})
	return nodeLeft
}

func ensureRight() *node {
	onceRight.Do(func() {
		b := ensureBase()
		n := &node{
			name:  "right",
			value: b.value + 2,
			p:     &payload{owner: "right", blob: make([]byte, 4096)},
			base:  b,
		}
		nodeRight = n
		atomic.AddInt32(&buildCount[2], 1)
		record("right")
	})
	return nodeRight
}

func ensureRoot() *node {
	onceRoot.Do(func() {
		l := ensureLeft()
		r := ensureRight()
		n := &node{
			name:  "root",
			value: l.value*10 + r.value,
			p:     &payload{owner: "root", blob: make([]byte, 8192)},
			left:  l,
			right: r,
		}
		nodeRoot = n
		atomic.AddInt32(&buildCount[3], 1)
		record("root")
	})
	return nodeRoot
}

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
		v = 8
	}
	return v
}

func checkGraph(root *node) {
	ck(root != nil, "root non-nil")
	if root == nil {
		return
	}
	ck(root.name == "root" && root.value == 133, "root fields")
	ck(root.left == nodeLeft && root.right == nodeRight, "root points at both branches")
	ck(root.left != nil && root.left.base == nodeBase, "left points at base")
	ck(root.right != nil && root.right.base == nodeBase, "right points at base")
	ck(nodeBase.value == 11 && nodeLeft.value == 12 && nodeRight.value == 13, "node values")
	ck(nodeBase.p != nil && nodeBase.p.owner == "base", "base payload")
	ck(root.p != nil && root.p.owner == "root", "root payload")
}

func main() {
	seedVal = readSeed()
	seedStr = strconv.FormatInt(seedVal, 10)
	rounds := tortureRounds()
	workers := 32 * rounds
	iters := 32 * rounds

	// Concurrent first use of the DAG from many goroutines.
	var wg sync.WaitGroup
	var bad int32
	start := int(seedVal % 4)
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < iters; i++ {
				r := ensureRoot()
				if r != nodeRoot || r.left != nodeLeft || r.right != nodeRight ||
					r.left.base != nodeBase || r.value != 133 {
					atomic.AddInt32(&bad, 1)
					return
				}
				_ = (start + w + i) % 4
			}
		}(w)
	}
	// Allocate garbage and force GC while workers race on first use.
	for i := 0; i < 8; i++ {
		gcSink = append(gcSink, make([]byte, 32768))
		runtime.GC()
	}
	wg.Wait()
	ck(atomic.LoadInt32(&bad) == 0, "all goroutines observe one root graph")
	gcSink = gcSink[:0]

	// Exactly-once build counters and dependency order.
	for i, name := range []string{"base", "left", "right", "root"} {
		ck(atomic.LoadInt32(&buildCount[i]) == 1, name+" built exactly once")
		ck(traceCount(name) == 1, name+" traced exactly once")
	}
	ck(traceIndex("base") < traceIndex("left"), "base before left")
	ck(traceIndex("base") < traceIndex("right"), "base before right")
	ck(traceIndex("left") < traceIndex("root"), "left before root")
	ck(traceIndex("right") < traceIndex("root"), "right before root")
	checkGraph(nodeRoot)

	// Second use must reuse the same object.
	before := atomic.LoadInt32(&buildCount[3])
	ck(ensureRoot() == nodeRoot, "second ensureRoot returns same pointer")
	ck(atomic.LoadInt32(&buildCount[3]) == before, "second use does not rebuild")

	// Explicit GC with the whole DAG reachable only through globals.
	gcStress()
	checkGraph(nodeRoot)
	ck(traceCount("root") == 1, "root init not replayed after GC")
	ck(atomic.LoadInt32(&buildCount[0]) == 1, "base count stable after GC")

	finish("l12_once_dependency_dag")
}
