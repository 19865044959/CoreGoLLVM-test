// L17: 深层嵌套聚合（struct 内嵌数组/struct/any/func/map）按值参数与按值返回。
// FCA 抽取/重组必须覆盖 each 嵌套偏移处的指针槽，GC 后才继续读字段。
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

type leaf struct {
	p *item
	s string
}

type mid struct {
	a [2]leaf
	q *item
	v any
}

type top struct {
	m    mid
	arr  [2][2]*item
	f    func() *item
	mm   map[string]*item
	tail string
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

func finish(name string) {
	if n := atomic.LoadInt32(&failed); n != 0 {
		fmt.Printf("FAILED %s (%d failed checks) seed=%d\n", name, n, uafSeed)
		os.Exit(1)
	}
	fmt.Println("ALL PASS " + name)
}

//go:noinline
func makeTop(base int64) top {
	t := top{tail: "tail"}
	t.m.a[0] = leaf{p: &item{v: base + 1, name: "leaf0", next: &item{v: base + 2, name: "n0"}}, s: "s0"}
	t.m.a[1] = leaf{p: &item{v: base + 3, name: "leaf1", next: &item{v: base + 4, name: "n1"}}, s: "s1"}
	t.m.q = &item{v: base + 5, name: "q", next: &item{v: base + 6, name: "qn"}}
	t.m.v = leaf{p: &item{v: base + 7, name: "vl"}, s: "inside"}
	t.arr[0][0] = &item{v: base + 10, name: "a00"}
	t.arr[0][1] = &item{v: base + 11, name: "a01"}
	t.arr[1][0] = &item{v: base + 12, name: "a10"}
	t.arr[1][1] = &item{v: base + 13, name: "a11"}
	t.mm = map[string]*item{
		"k0": &item{v: base + 8, name: "m0"},
		"k1": &item{v: base + 9, name: "m1"},
	}
	q := t.m.q
	t.f = func() *item { return q }
	gcStress()
	return t
}

func foldMid(m mid) int64 {
	sum := int64(0)
	for i := range m.a {
		if m.a[i].p == nil || m.a[i].p.next == nil {
			ck(false, fmt.Sprintf("foldMid: a[%d] nil seed=%d", i, uafSeed))
			continue
		}
		sum += m.a[i].p.v + m.a[i].p.next.v + int64(len(m.a[i].s))
	}
	if m.q == nil || m.q.next == nil {
		ck(false, fmt.Sprintf("foldMid: q nil seed=%d", uafSeed))
	} else {
		sum += m.q.v + m.q.next.v
	}
	if lf, ok := m.v.(leaf); ok {
		if lf.p == nil {
			ck(false, fmt.Sprintf("foldMid: interface leaf nil seed=%d", uafSeed))
		} else {
			sum += lf.p.v + int64(len(lf.s))
		}
	} else {
		ck(false, fmt.Sprintf("foldMid: interface type %T seed=%d", m.v, uafSeed))
	}
	return sum
}

func foldTop(t top) int64 {
	if t.m.q == nil || t.m.q.next == nil {
		ck(false, fmt.Sprintf("foldTop: q nil seed=%d", uafSeed))
		return 0
	}
	sum := foldMid(t.m)
	for i := range t.arr {
		for j := range t.arr[i] {
			if t.arr[i][j] == nil {
				ck(false, fmt.Sprintf("foldTop: arr[%d][%d] nil seed=%d", i, j, uafSeed))
				continue
			}
			sum += t.arr[i][j].v
		}
	}
	if t.f == nil {
		ck(false, fmt.Sprintf("foldTop: func nil seed=%d", uafSeed))
	} else if p := t.f(); p == nil {
		ck(false, fmt.Sprintf("foldTop: func returned nil seed=%d", uafSeed))
	} else {
		sum += p.v
	}
	if t.mm == nil || t.mm["k0"] == nil || t.mm["k1"] == nil {
		ck(false, fmt.Sprintf("foldTop: map corrupted seed=%d", uafSeed))
	} else {
		sum += t.mm["k0"].v + t.mm["k1"].v
	}
	sum += int64(len(t.tail))
	return sum
}

//go:noinline
func takeMid(m mid) int64 {
	gcStress()
	a := foldMid(m)
	gcStress()
	b := foldMid(m)
	ck(a == b, fmt.Sprintf("takeMid: changed across GC seed=%d", uafSeed))
	return a
}

//go:noinline
func takeLeaves(a [2]leaf) int64 {
	gcStress()
	sum := int64(0)
	for i := range a {
		if a[i].p == nil || a[i].p.next == nil {
			ck(false, fmt.Sprintf("takeLeaves: a[%d] nil seed=%d", i, uafSeed))
			continue
		}
		sum += a[i].p.v + a[i].p.next.v + int64(len(a[i].s))
	}
	gcStress()
	return sum
}

//go:noinline
func takeTop(t top) (int64, int64) {
	gcStress()
	a := foldTop(t)
	gcStress()
	b := foldTop(t)
	return a, b
}

// retop 只改写聚合副本中的数组/内层数组字段，返回后再 GC。
//
//go:noinline
func retop(t top, base int64) top {
	t.m.a[0].p = &item{v: base + 77, name: "ret-leaf", next: &item{v: base + 2, name: "n0"}}
	t.arr[1][1] = &item{v: base + 99, name: "ret-arr"}
	t.tail = "ret"
	gcStress()
	return t
}

func verifyTop(t top, base int64, mutated bool, where string, rnd int) {
	a0 := base + 1
	arr11 := base + 13
	tail := "tail"
	if mutated {
		a0 = base + 77
		arr11 = base + 99
		tail = "ret"
	}
	bad := t.m.a[0].p == nil || t.m.a[0].p.v != a0 || t.m.a[0].p.next == nil ||
		t.m.a[0].p.next.v != base+2 || t.m.a[0].s != "s0" ||
		t.m.a[1].p == nil || t.m.a[1].p.v != base+3 || t.m.a[1].p.next == nil ||
		t.m.a[1].p.next.v != base+4 || t.m.a[1].s != "s1" ||
		t.m.q == nil || t.m.q.v != base+5 || t.m.q.next == nil || t.m.q.next.v != base+6
	if !bad {
		lf, ok := t.m.v.(leaf)
		bad = !ok || lf.p == nil || lf.p.v != base+7 || lf.s != "inside"
	}
	if !bad {
		bad = t.arr[0][0] == nil || t.arr[0][0].v != base+10 ||
			t.arr[0][1] == nil || t.arr[0][1].v != base+11 ||
			t.arr[1][0] == nil || t.arr[1][0].v != base+12 ||
			t.arr[1][1] == nil || t.arr[1][1].v != arr11
	}
	if !bad {
		bad = t.f == nil || t.f() == nil || t.f().v != base+5
	}
	if !bad {
		bad = t.mm == nil || t.mm["k0"] == nil || t.mm["k0"].v != base+8 ||
			t.mm["k1"] == nil || t.mm["k1"].v != base+9
	}
	if !bad {
		bad = t.tail != tail
	}
	ck(!bad, fmt.Sprintf("round %d %s: nested aggregate corrupted after GC seed=%d", rnd, where, uafSeed))
}

func main() {
	uafSeed = seedFromEnv()

	for rnd := 0; rnd < 6; rnd++ {
		base := int64(rnd*2000) + uafSeed%31

		t := makeTop(base)
		gcStress()
		verifyTop(t, base, false, "makeTop", rnd)

		a, b := takeTop(t)
		wantTop := 14*base + 110
		ck(a == wantTop && b == wantTop,
			fmt.Sprintf("round %d: takeTop got=%d/%d want=%d seed=%d", rnd, a, b, wantTop, uafSeed))
		gcStress()
		verifyTop(t, base, false, "takeTop", rnd)

		m := takeMid(t.m)
		wantMid := 7*base + 38
		ck(m == wantMid, fmt.Sprintf("round %d: takeMid got=%d want=%d seed=%d",
			rnd, m, wantMid, uafSeed))

		lv := takeLeaves(t.m.a)
		wantLeaves := 4*base + 14
		ck(lv == wantLeaves, fmt.Sprintf("round %d: takeLeaves got=%d want=%d seed=%d",
			rnd, lv, wantLeaves, uafSeed))

		rt := retop(t, base)
		gcStress()
		verifyTop(rt, base, true, "retop", rnd)
		verifyTop(t, base, false, "retop-original", rnd)

		foldRet := foldTop(rt)
		ck(foldRet == wantTop+76+86-1,
			fmt.Sprintf("round %d: foldTop(rt) got=%d want=%d seed=%d",
				rnd, foldRet, wantTop+161, uafSeed))

		gcStress()
		verifyTop(rt, base, true, "retop-after-gc", rnd)
	}

	finish("l17_fca_nested_mixed")
}
