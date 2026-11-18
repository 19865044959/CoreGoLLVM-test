// L11: 闭包与 map 逃逸语义——闭包捕获变量/循环变量、map 逃逸到堆、
// map 中的闭包与指针值跨 GC 仍可用；无论是否发生逃逸分析/内联，结果一致。
package main

import (
	"fmt"
	"os"
	"runtime"
	"strconv"
	"sync/atomic"
)

type emNode struct {
	id   int64
	tag  string
	next *emNode
}

type emValue struct {
	a, b int64
}

var (
	failed int32
	gcSink [][]byte
	seed   uint64 = 1
)

func ck(ok bool, msg string) {
	if !ok {
		atomic.AddInt32(&failed, 1)
		fmt.Printf("FAIL: %s (UAF_SEED=%d)\n", msg, seed)
	}
}

func gcStress() {
	for i := 0; i < 16; i++ {
		b := make([]byte, 4096)
		b[0] = byte(i)
		b[len(b)-1] = byte(255 - i)
		gcSink = append(gcSink[:0], b)
	}
	gcSink = gcSink[:0]
	runtime.GC()
	runtime.GC()
}

func loadSeed() uint64 {
	s := os.Getenv("UAF_SEED")
	if s == "" {
		return 1
	}
	n, err := strconv.ParseUint(s, 10, 64)
	if err != nil {
		return 1
	}
	return n
}

func finish(name string) {
	if n := atomic.LoadInt32(&failed); n != 0 {
		fmt.Printf("FAILED %s (%d failed checks, seed=%d)\n", name, n, seed)
		os.Exit(1)
	}
	fmt.Println("ALL PASS " + name)
}

// makeStore 返回逃逸到堆上的 map 与一个捕获该 map 的闭包。
func makeStore(n int) (map[int64]*emNode, func(int64) *emNode) {
	m := make(map[int64]*emNode, n)
	put := func(i int64) *emNode {
		nd := &emNode{id: i, tag: "node-" + strconv.FormatInt(i, 10)}
		m[i] = nd
		return nd
	}
	return m, put
}

// mapOfClosures 的值是闭包，map 本身必须逃逸。
func mapOfClosures() map[string]func(int64) int64 {
	return map[string]func(int64) int64{
		"double": func(x int64) int64 { return x * 2 },
		"triple": func(x int64) int64 { return x * 3 },
		"inc":    func(x int64) int64 { return x + 1 },
	}
}

// immediateClosureSum 的闭包只在函数内立即调用，是“不逃逸”路径。
func immediateClosureSum(vals []int64) int64 {
	sum := func() int64 {
		var t int64
		for _, v := range vals {
			t += v
		}
		return t
	}()
	return sum
}

func main() {
	seed = loadSeed()
	if seed == 0 {
		seed = 1
	}
	n := 24 + int(seed%17) // 24..40

	m, put := makeStore(n)
	for i := 0; i < n; i++ {
		put(int64(i))
	}
	for i := 0; i < n-1; i++ {
		m[int64(i)].next = m[int64(i+1)]
	}

	// 闭包捕获的是变量本身：p 重新赋值后闭包看到新值。
	p := m[0]
	readID := func() int64 { return p.id }
	p = m[1]
	ck(readID() == 1, "closure captures variable, not value")

	// 每次迭代的循环变量各自被捕获（Go 1.22 语义）。
	fns := make([]func() int64, n)
	for i := 0; i < n; i++ {
		fns[i] = func() int64 { return int64(i) }
	}

	// 捕获 map 的闭包。
	calls := 0
	touch := func(k int64) bool {
		calls++
		_, ok := m[k]
		return ok
	}
	ck(touch(0) && touch(int64(n-1)) && !touch(int64(n+5)), "closure over map lookup")
	ck(calls == 3, "closure call count")

	ops := mapOfClosures()
	mk := func(i int64) *emNode { return &emNode{id: i, tag: "escaped"} }
	it := mk(9)

	gcStress()

	ck(ops["double"](21) == 42 && ops["triple"](7) == 21 && ops["inc"](41) == 42,
		"map of closures after GC")
	ck(it.id == 9 && it.tag == "escaped", "escaped closure result after GC")
	ck(len(m) == n, "map length after GC")

	tagOK := true
	for i := 0; i < n; i++ {
		nd := m[int64(i)]
		if nd == nil || nd.id != int64(i) || nd.tag != "node-"+strconv.FormatInt(int64(i), 10) {
			tagOK = false
			break
		}
	}
	ck(tagOK, "map entries after GC")

	loopOK := true
	for i := 0; i < n; i++ {
		if fns[i]() != int64(i) {
			loopOK = false
			break
		}
	}
	ck(loopOK, "per-iteration loop closures after GC")

	cur := m[0]
	count := 0
	var chainSum int64
	for cur != nil {
		count++
		chainSum += cur.id
		cur = cur.next
	}
	ck(count == n && chainSum == int64(n*(n-1)/2), "map value chain after GC")

	// map 存结构体值 + 闭包 updater。
	sm := make(map[int64]emValue, n)
	update := func(k int64) { sm[k] = emValue{a: k, b: k * k} }
	for i := 0; i < n; i++ {
		update(int64(i))
	}
	gcStress()
	var ssum int64
	structOK := true
	for i := 0; i < n; i++ {
		v := sm[int64(i)]
		if v.b != v.a*v.a {
			structOK = false
		}
		ssum += v.b
	}
	ck(structOK, "map struct values after GC")
	ck(ssum == int64((n-1)*n*(2*n-1)/6), "map struct value sum")

	vals := make([]int64, n)
	for i := range vals {
		vals[i] = int64(i * i)
	}
	ck(immediateClosureSum(vals) == int64((n-1)*n*(2*n-1)/6), "immediate closure sum")

	// clear 之后链仍持有节点，GC 不得回收。
	head := m[0]
	clear(m)
	gcStress()
	ck(len(m) == 0, "map cleared")
	count = 0
	chainSum = 0
	for cur := head; cur != nil; cur = cur.next {
		count++
		chainSum += cur.id
	}
	ck(count == n && chainSum == int64(n*(n-1)/2), "chain survives map clear + GC")

	finish("l11_closure_map_escape")
}
