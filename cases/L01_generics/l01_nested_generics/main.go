// L01: 嵌套泛型 Box[Box[*T]]、Pair[Box[*payload], int64] 跨 GC 的指针链完整性。
package main

import (
	"fmt"
	"os"
	"runtime"
	"strconv"
	"sync/atomic"
)

type payload struct {
	v    int64
	name string
}

type Box[T any] struct {
	v   *T
	tag string
}

func NewBox[T any](v *T, tag string) *Box[T] { return &Box[T]{v: v, tag: tag} }

func (b *Box[T]) Get() *T { return b.v }

type Pair[A, B any] struct {
	a *A
	b *B
}

type trio struct {
	outer *Box[Box[*payload]]
	inner *Box[*payload]
	leaf  *payload
}

var (
	failed     int32
	gcSink     [][]byte
	nestedSink []trio
	uafSeed    int64 = 1
)

func ck(ok bool, msg string) {
	if !ok {
		atomic.AddInt32(&failed, 1)
		fmt.Println("FAIL:", msg)
	}
}

func gcStress() {
	for i := 0; i < 8; i++ {
		b := make([]byte, 2048)
		b[0] = byte(i)
		gcSink = append(gcSink[:0], b)
	}
	gcSink = gcSink[:0]
	runtime.GC()
	runtime.GC()
}

func finish(name string) {
	if n := atomic.LoadInt32(&failed); n != 0 {
		fmt.Printf("FAILED %s seed=%d (%d failed checks)\n", name, uafSeed, n)
		os.Exit(1)
	}
	fmt.Println("ALL PASS " + name)
}

func envSeed() int64 {
	s := os.Getenv("UAF_SEED")
	if s == "" {
		return 1
	}
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil || n <= 0 {
		return 1
	}
	return n
}

func main() {
	uafSeed = envSeed()
	p := &payload{v: 7, name: "leaf"}
	inner := NewBox(&p, "inner")      // *Box[*payload]，v 为 **payload
	outer := NewBox(inner, "outer")   // *Box[Box[*payload]]
	triple := NewBox(outer, "triple") // *Box[Box[Box[*payload]]]

	ck(outer.Get() == inner, "outer lost inner box")
	ck(inner.Get() == &p, "inner lost payload address")
	ok := *inner.Get()
	ck(ok == p && ok.v == 7 && ok.name == "leaf", "nested payload corrupted before GC")

	gcStress()

	ck(outer.Get() == inner, "outer lost inner box after GC")
	ck(inner.Get() == &p, "inner lost payload address after GC")
	leaf := *inner.Get()
	ck(leaf == p && leaf.v == 7 && leaf.name == "leaf", "nested payload corrupted after GC")
	ck(triple.Get() == outer, "triple lost outer box after GC")
	ck(inner.tag == "inner" && outer.tag == "outer" && triple.Get() == outer, "nested tag/link corrupted")

	t1 := triple.Get() // *Box[Box[*payload]]
	t2 := t1.Get()     // *Box[*payload]
	t3 := t2.Get()     // **payload
	ck(t2 == inner, "third-level traversal mismatch")
	ck(*t3 == p, "third-level payload address mismatch")
	ck((*t3).v == 7 && (*t3).name == "leaf", "third-level payload value corrupted")

	// Pair 的 A/B 分别是嵌套泛型与标量指针。
	n := int64(99)
	pair := &Pair[Box[*payload], int64]{a: inner, b: &n}
	gcStress()
	ck(pair.a == inner, "Pair.a lost nested box")
	pl := *pair.a.Get()
	ck(pl == p && pl.v == 7, "Pair.a payload corrupted")
	ck(*pair.b == 99, "Pair.b value corrupted")
	*pair.b = 100
	ck(*pair.b == 100, "Pair.b write-through failed")

	// 经嵌套链写穿到最内层 payload，再 GC 观察。
	leaf.v = 8
	gcStress()
	ck(p.v == 8, "write-through to nested payload lost")
	ck((*t2.Get()).v == 8, "third-level view missed write-through")

	// 多种叶子节点的嵌套链批量存活。
	m := 32 + int(uafSeed%9)
	nestedSink = make([]trio, m)
	for i := 0; i < m; i++ {
		lp := &payload{v: int64(i), name: fmt.Sprintf("n%d", i)}
		in := NewBox(&lp, "in")
		out := NewBox(in, "out")
		nestedSink[i] = trio{outer: out, inner: in, leaf: lp}
	}
	gcStress()
	for i := 0; i < m; i++ {
		t := nestedSink[i]
		ck(t.outer.Get() == t.inner, "batch outer->inner link broken")
		ck(*t.inner.Get() == t.leaf, "batch inner->leaf link broken")
		ck(t.leaf.v == int64(i) && t.leaf.name == fmt.Sprintf("n%d", i), "batch leaf payload corrupted")
		ck((*t.inner.Get()).v == int64(i), "batch deref value corrupted")
	}

	// nil 泛型嵌套字段跨 GC 仍为 nil。
	nilOuter := NewBox[Box[*payload]](nil, "nil")
	ck(nilOuter.Get() == nil, "nil nested box not nil before GC")
	gcStress()
	ck(nilOuter.Get() == nil, "nil nested box became non-nil after GC")

	finish("l01_nested_generics")
}
