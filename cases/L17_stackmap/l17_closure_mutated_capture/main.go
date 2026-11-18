// L17: 闭包的可变捕获。闭包把捕获的指针变量重绑定到新对象，并在同一调用内
// 把捕获值复制到局部变量后跨 GC 使用；闭包本身装箱在 any 中经接口断言调用。
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
	next *item
}

type bag struct {
	head *item
	n    int64
	tag  string
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

//go:noinline
func gcStress() {
	var sink [][]byte
	for i := 0; i < 8; i++ {
		b := make([]byte, 2048)
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

func finish(name string) {
	if n := atomic.LoadInt32(&failed); n != 0 {
		fmt.Printf("FAILED %s (%d failed checks) seed=%d\n", name, n, uafSeed)
		os.Exit(1)
	}
	fmt.Println("ALL PASS " + name)
}

//go:noinline
func sinkPair(a, b *item, delta int64, where string) int64 {
	gcStress()
	if a == nil || b == nil || b.next == nil {
		ck(false, fmt.Sprintf("%s: closure capture nil after GC seed=%d", where, uafSeed))
		return 0
	}
	if a.next == nil || a.next.v != a.v-delta {
		ck(false, fmt.Sprintf("%s: mutated capture chain corrupted seed=%d", where, uafSeed))
	}
	if b.next.v != b.v+1 {
		ck(false, fmt.Sprintf("%s: stable capture corrupted seed=%d", where, uafSeed))
	}
	gcStress()
	return a.v
}

//go:noinline
func callClosure(f any, delta int64, where string) bag {
	// 接口 data word 在 GC 后仍须指向闭包对象。
	gcStress()
	fn, ok := f.(func(int64) bag)
	if !ok {
		ck(false, fmt.Sprintf("%s: closure type assertion failed seed=%d", where, uafSeed))
		return bag{}
	}
	return fn(delta)
}

func makeCounter(base int64) func(delta int64) bag {
	cur := &item{v: base}
	keep := &item{v: base + 7}
	keep.next = &item{v: base + 8}
	hits := int64(0)
	return func(delta int64) bag {
		hits++
		// 重绑定捕获变量：旧链通过 next 指针仍被引用。
		cur = &item{v: cur.v + delta, next: cur}
		local := cur
		keepLocal := keep
		gcStress()
		if local.next == nil || local.next.v != local.v-delta {
			ck(false, fmt.Sprintf("closure: reassigned capture corrupted seed=%d", uafSeed))
		}
		if keepLocal.next == nil || keepLocal.next.v != keepLocal.v+1 {
			ck(false, fmt.Sprintf("closure: keep capture corrupted seed=%d", uafSeed))
		}
		got := sinkPair(local, keepLocal, delta, "closure")
		gcStress()
		if got != local.v {
			ck(false, fmt.Sprintf("closure: value changed after sink GC seed=%d", uafSeed))
		}
		return bag{head: local, n: hits, tag: "cap"}
	}
}

func verifyBag(b bag, wantN int64, where string) {
	if b.head == nil || b.tag != "cap" || b.n != wantN {
		ck(false, fmt.Sprintf("%s: bag fields corrupted seed=%d", where, uafSeed))
		return
	}
	// 初始捕获节点 + 每次调用 prepend 一个节点。
	wantLen := wantN + 1
	length := int64(0)
	for p := b.head; p != nil; p = p.next {
		length++
		if length > wantLen {
			break
		}
	}
	if length != wantLen {
		ck(false, fmt.Sprintf("%s: chain length=%d want=%d seed=%d", where, length, wantLen, uafSeed))
	}
}

func main() {
	uafSeed = seedFromEnv()
	step := (uafSeed%5+5)%5 + 1

	for r := 0; r < 8; r++ {
		base := int64(1000*r) + uafSeed%23
		f0 := makeCounter(base)
		f1 := makeCounter(base + 500)
		boxed := []any{f0, f1}
		gcStress()

		// 轮换调用两个闭包，跟踪各自命中次数。
		hits := [2]int64{}
		for k := 0; k < 5; k++ {
			which := k % 2
			b := callClosure(boxed[which], int64(k+1)*step, fmt.Sprintf("round %d call %d", r, k))
			hits[which]++
			gcStress()
			verifyBag(b, hits[which], fmt.Sprintf("round %d call %d", r, k))
		}
	}

	// 每轮新建的闭包各自捕获独立对象；全部构造完后统一 GC 再调用。
	var fns []any
	var bases []int64
	for i := 0; i < 6; i++ {
		base := int64(9000 + i*37)
		fns = append(fns, makeCounter(base))
		bases = append(bases, base)
	}
	gcStress()
	for i, f := range fns {
		b := callClosure(f, step, fmt.Sprintf("batch %d", i))
		gcStress()
		verifyBag(b, 1, fmt.Sprintf("batch %d", i))
		if b.head != nil && b.head.v != bases[i]+step {
			ck(false, fmt.Sprintf("batch %d: base capture corrupted seed=%d", i, uafSeed))
		}
	}

	finish("l17_closure_mutated_capture")
}
