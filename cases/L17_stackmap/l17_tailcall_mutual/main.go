// L17: 尾位置调用（mutual recursion / 接口方法递归 / 闭包递归）。每层栈帧的
// 指针参数在尾调用点仍可能被 GC 扫描，链式 next 指针必须被逐个重定位。
package main

import (
	"fmt"
	"os"
	"runtime"
	"strconv"
	"sync/atomic"
)

type item struct {
	v    int64
	name string
	next *item
}

type payload struct {
	it    *item
	depth int64
	sum   int64
}

type walker interface {
	walk(p payload, n int64) int64
}

type walkImpl struct {
	keep *item
	add  int64
	self walker
}

var (
	failed  int32
	uafSeed int64 = 1
)

func ck(ok bool, msg string) {
	if !ok {
		atomic.AddInt32(&failed, 1)
		fmt.Println("FAIL:", msg)
	}
}

func gcStress() {
	var sink [][]byte
	for i := 0; i < 3; i++ {
		b := make([]byte, 1024)
		b[0] = byte(i)
		sink = append(sink, b)
	}
	runtime.GC()
	runtime.GC()
	runtime.KeepAlive(sink)
}

func seedFromEnv() int64 {
	if s := os.Getenv("UAF_SEED"); s != "" {
		if v, err := strconv.ParseInt(s, 10, 64); err == nil && v != 0 {
			return v
		}
	}
	return 1
}

func tortureRounds(def, capRounds int) int {
	n := def
	if s := os.Getenv("TORTURE_ROUNDS"); s != "" {
		if v, err := strconv.Atoi(s); err == nil && v > 0 {
			n *= v
			if n > capRounds {
				n = capRounds
			}
		}
	}
	return n
}

func finish(name string) {
	if n := atomic.LoadInt32(&failed); n != 0 {
		fmt.Printf("FAILED %s (%d failed checks) seed=%d\n", name, n, uafSeed)
		os.Exit(1)
	}
	fmt.Println("ALL PASS " + name)
}

func chainOK(p *item, want int64) bool {
	n := int64(0)
	for p != nil {
		n++
		p = p.next
	}
	return n == want
}

//go:noinline
func checkChain(p *item, want int64, where string) {
	if !chainOK(p, want) {
		ck(false, fmt.Sprintf("%s: chain broken seed=%d", where, uafSeed))
	}
	if p != nil && p.name != "a" && p.name != "b" {
		ck(false, fmt.Sprintf("%s: chain name corrupted name=%q seed=%d", where, p.name, uafSeed))
	}
}

//go:noinline
func stepA(p payload, n int64) int64 {
	if n <= 0 {
		gcStress()
		checkChain(p.it, p.depth+1, "stepA base")
		return p.sum + p.it.v
	}
	it := &item{v: p.it.v + 1, name: "a", next: p.it}
	if n%9 == 0 {
		gcStress()
	}
	return stepB(payload{it: it, depth: p.depth + 1, sum: p.sum + it.v}, n-1)
}

//go:noinline
func stepB(p payload, n int64) int64 {
	if n <= 0 {
		gcStress()
		checkChain(p.it, p.depth+1, "stepB base")
		return p.sum + p.it.v
	}
	it := &item{v: p.it.v + 2, name: "b", next: p.it}
	if n%9 == 0 {
		gcStress()
	}
	return stepA(payload{it: it, depth: p.depth + 1, sum: p.sum + it.v}, n-1)
}

//go:noinline
func (w *walkImpl) walk(p payload, n int64) int64 {
	if n <= 0 {
		gcStress()
		if w == nil || w.keep == nil || w.keep.v != 700 || w.keep.name != "keep" {
			ck(false, fmt.Sprintf("walk base: receiver keep corrupted seed=%d", uafSeed))
		}
		checkChain(p.it, p.depth+1, "walk base")
		return p.sum + p.it.v
	}
	it := &item{v: p.it.v + w.add, name: "a", next: p.it}
	if n%5 == 0 {
		gcStress()
	}
	if w.self == nil {
		ck(false, fmt.Sprintf("walk: self interface nil seed=%d", uafSeed))
		return 0
	}
	return w.self.walk(payload{it: it, depth: p.depth + 1, sum: p.sum + it.v}, n-1)
}

func runClosure(base, n int64, where string) int64 {
	var f func(payload, int64) int64
	f = func(p payload, n int64) int64 {
		if n <= 0 {
			gcStress()
			checkChain(p.it, p.depth+1, where)
			return p.sum + p.it.v
		}
		it := &item{v: p.it.v + 4, name: "a", next: p.it}
		if n%7 == 0 {
			gcStress()
		}
		return f(payload{it: it, depth: p.depth + 1, sum: p.sum + it.v}, n-1)
	}
	return f(payload{it: &item{v: base, name: "root"}, depth: 0, sum: base}, n)
}

func expectAlternating(base, n int64) int64 {
	sum := base
	cur := base
	for i := int64(0); i < n; i++ {
		if i%2 == 0 {
			cur += 1
		} else {
			cur += 2
		}
		sum += cur
	}
	return sum + cur
}

func expectStep(base, n, add int64) int64 {
	sum := base
	cur := base
	for i := int64(0); i < n; i++ {
		cur += add
		sum += cur
	}
	return sum + cur
}

func main() {
	uafSeed = seedFromEnv()
	rounds := tortureRounds(4, 64)

	depth := int64(320)
	depthIface := int64(48)
	depthClosure := int64(64)

	for rnd := 0; rnd < rounds; rnd++ {
		base := int64(rnd*300) + uafSeed%37
		root := &item{v: base, name: "root"}

		got := stepA(payload{it: root, depth: 0, sum: base}, depth)
		want := expectAlternating(base, depth)
		ck(got == want, fmt.Sprintf("round %d: stepA/B got=%d want=%d seed=%d",
			rnd, got, want, uafSeed))
		gcStress()
		ck(root.v == base && root.name == "root",
			fmt.Sprintf("round %d: root corrupted after tail chain seed=%d", rnd, uafSeed))

		w := &walkImpl{keep: &item{v: 700, name: "keep"}, add: 3}
		w.self = w
		var iw walker = w
		gotI := iw.walk(payload{it: root, depth: 0, sum: base}, depthIface)
		wantI := expectStep(base, depthIface, 3)
		ck(gotI == wantI, fmt.Sprintf("round %d: iface walk got=%d want=%d seed=%d",
			rnd, gotI, wantI, uafSeed))
		gcStress()
		ck(w.keep != nil && w.keep.v == 700 && w.keep.name == "keep",
			fmt.Sprintf("round %d: walk receiver keep corrupted seed=%d", rnd, uafSeed))

		gotC := runClosure(base, depthClosure, "closure base")
		wantC := expectStep(base, depthClosure, 4)
		ck(gotC == wantC, fmt.Sprintf("round %d: closure tail got=%d want=%d seed=%d",
			rnd, gotC, wantC, uafSeed))

		gcStress()
		ck(root.v == base && root.name == "root",
			fmt.Sprintf("round %d: root corrupted after all tail paths seed=%d", rnd, uafSeed))
	}

	finish("l17_tailcall_mutual")
}
