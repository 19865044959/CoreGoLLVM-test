// L17: 小 struct 走寄存器返回（指针+标量、string+指针）与多返回值跨 GC。
// 返回槽/寄存器中的指针必须被栈映射标记，并与全局锚点保持同一对象。
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

type ptrPair struct {
	p *item
	n int64
}

type strPtr struct {
	s string
	p *item
}

type pairMaker interface {
	make(v int64) ptrPair
}

type makerRef struct{ keep *item }

type makerVal struct{ keep *item }

var (
	failed     int32
	uafSeed    int64 = 1
	pairAnchor *item
	strAnchor  *item
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

// makePair 在 callee 内两次 GC 后按值返回 (ptr,int)。
//
//go:noinline
func makePair(v int64) ptrPair {
	gcStress()
	p := &item{v: v, name: "pair", next: &item{v: v + 1, name: "pair-next"}}
	pairAnchor = p
	gcStress()
	return ptrPair{p: p, n: v}
}

// makeStrPtr 按值返回 (string,ptr)，string 头与指针都落在返回槽。
//
//go:noinline
func makeStrPtr(v int64) strPtr {
	s := "sv" + strconv.FormatInt(v, 10)
	q := &item{v: v + 7, name: s}
	strAnchor = q
	gcStress()
	return strPtr{s: s, p: q}
}

//go:noinline
func pickPair(flag int, v int64) ptrPair {
	if flag%2 == 0 {
		p := &item{v: v, name: "even"}
		gcStress()
		return ptrPair{p: p, n: v + 1}
	}
	p := &item{v: v, name: "odd"}
	gcStress()
	return ptrPair{p: p, n: v + 2}
}

//go:noinline
func splitRet(v int64) (*item, string, int64) {
	p := &item{v: v, name: "tuple", next: &item{v: v + 1, name: "tn"}}
	s := "tuple-string"
	gcStress()
	return p, s, v + 3
}

//go:noinline
func (m *makerRef) make(v int64) ptrPair {
	gcStress()
	return ptrPair{p: m.keep, n: v + m.keep.v}
}

//go:noinline
func (m makerVal) make(v int64) ptrPair {
	gcStress()
	return ptrPair{p: m.keep, n: v + m.keep.v}
}

func main() {
	uafSeed = seedFromEnv()

	for rnd := 0; rnd < 12; rnd++ {
		base := int64(rnd*100) + uafSeed%7

		pp := makePair(base)
		gcStress()
		ck(pp.n == base, fmt.Sprintf("round %d pair n=%d want=%d seed=%d", rnd, pp.n, base, uafSeed))
		ck(pp.p == pairAnchor && pp.p.v == base && pp.p.name == "pair" &&
			pp.p.next != nil && pp.p.next.v == base+1,
			fmt.Sprintf("round %d pair pointer corrupted seed=%d", rnd, uafSeed))

		// 通过返回指针改写字段，再 GC 后检查全局锚点：必须仍是同一对象。
		pp.p.v = base + 50
		gcStress()
		ck(pairAnchor.v == base+50 && pairAnchor.name == "pair",
			fmt.Sprintf("round %d anchor identity broken seed=%d", rnd, uafSeed))
		ck(pairAnchor.next != nil && pairAnchor.next.v == base+1,
			fmt.Sprintf("round %d anchor next corrupted seed=%d", rnd, uafSeed))

		sp := makeStrPtr(base)
		gcStress()
		ck(sp.s == "sv"+strconv.FormatInt(base, 10) && sp.p == strAnchor &&
			sp.p.name == sp.s && sp.p.v == base+7,
			fmt.Sprintf("round %d string/pointer return corrupted seed=%d", rnd, uafSeed))

		for _, flag := range []int{0, 1} {
			got := pickPair(flag, base+int64(flag))
			gcStress()
			wantN := base + int64(flag) + 1
			wantName := "even"
			if flag == 1 {
				wantN = base + int64(flag) + 2
				wantName = "odd"
			}
			ck(got.n == wantN && got.p != nil && got.p.v == base+int64(flag) &&
				got.p.name == wantName,
				fmt.Sprintf("round %d pickPair flag=%d corrupted seed=%d", rnd, flag, uafSeed))
		}

		tp, ts, tn := splitRet(base + 9)
		gcStress()
		ck(tp != nil && tp.v == base+9 && tp.name == "tuple" &&
			tp.next != nil && tp.next.v == base+10 && ts == "tuple-string" && tn == base+12,
			fmt.Sprintf("round %d tuple return corrupted seed=%d", rnd, uafSeed))
		gcStress()
		ck(tp.next.v == base+10,
			fmt.Sprintf("round %d tuple pointer after second GC seed=%d", rnd, uafSeed))

		mr := &makerRef{keep: &item{v: base + 900, name: "keep"}}
		var mk pairMaker = mr
		g1 := mk.make(base)
		gcStress()
		ck(g1.p == mr.keep && g1.p.v == base+900 && g1.n == base+base+900,
			fmt.Sprintf("round %d pointer-receiver maker corrupted seed=%d", rnd, uafSeed))

		mv := makerVal{keep: &item{v: base + 901, name: "vkeep"}}
		mk = mv
		g2 := mk.make(base)
		gcStress()
		ck(g2.p == mv.keep && g2.p.v == base+901 && g2.p.name == "vkeep",
			fmt.Sprintf("round %d value-receiver maker corrupted seed=%d", rnd, uafSeed))

		var boxed any = makePair(base + 4)
		gcStress()
		bp, ok := boxed.(ptrPair)
		ck(ok && bp.p == pairAnchor && bp.p != nil && bp.p.v == base+4,
			fmt.Sprintf("round %d boxed return corrupted seed=%d", rnd, uafSeed))
	}

	finish("l17_struct_return_regabi")
}
