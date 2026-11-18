// L17: spill interface pair。大量 any 局部/形参 + 含指针 struct 值在深度递归
// 触发 morestack 前存活，GC 后逐个类型断言；栈/寄存器槽位图必须覆盖 iface 两字。
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
	tag  string
	next *item
}

type box struct {
	p *item
	s string
	a [2]*item
}

type anonBox struct {
	P *item
	T string
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

func mkItem(base int64) *item {
	return &item{v: base, tag: "iface", next: &item{v: base + 1, tag: "iface"}}
}

// scanAny 校验一个可出现的接口值；任何指针失效都会在解引用/字段检查处暴露。
func scanAny(v any, idx int, where string) int64 {
	switch x := v.(type) {
	case nil:
		return 0
	case *item:
		if x == nil || x.tag != "iface" || x.next == nil || x.next.tag != "iface" {
			ck(false, fmt.Sprintf("%s: item[%d] corrupted seed=%d", where, idx, uafSeed))
			return 0
		}
		return x.v + x.next.v
	case box:
		if x.p == nil || x.p.tag != "iface" || x.s == "" || x.a[0] == nil || x.a[1] == nil {
			ck(false, fmt.Sprintf("%s: box[%d] corrupted seed=%d", where, idx, uafSeed))
			return 0
		}
		return x.p.v + int64(len(x.s)) + x.a[0].v + x.a[1].v
	case []*item:
		sum := int64(0)
		for j, p := range x {
			if p == nil || p.tag != "iface" {
				ck(false, fmt.Sprintf("%s: slice[%d][%d] corrupted seed=%d", where, idx, j, uafSeed))
				continue
			}
			sum += p.v
		}
		return sum
	case string:
		return int64(len(x))
	case anonBox:
		if x.P == nil || x.P.tag != "iface" || x.T == "" {
			ck(false, fmt.Sprintf("%s: anon[%d] corrupted seed=%d", where, idx, uafSeed))
			return 0
		}
		return x.P.v + int64(len(x.T))
	default:
		ck(false, fmt.Sprintf("%s: unexpected type %T at %d seed=%d", where, v, idx, uafSeed))
		return 0
	}
}

func scanList(vals []any, where string) int64 {
	sum := int64(0)
	for i, v := range vals {
		sum += scanAny(v, i, where)
	}
	return sum
}

// deep 每帧带 512B 局部数组，递归深度触发栈复制；p 在递归期间一直存活。
//
//go:noinline
func deep(n int, p *item) int64 {
	var buf [512]byte
	buf[0] = byte(n)
	if n == 0 {
		gcStress()
		if p == nil {
			ck(false, fmt.Sprintf("deep: nil pointer seed=%d", uafSeed))
			return 0
		}
		return p.v + int64(buf[0])
	}
	got := deep(n-1, p)
	gcStress()
	if p == nil {
		ck(false, fmt.Sprintf("deep: pointer lost after stack growth seed=%d", uafSeed))
	}
	return got + int64(buf[0]) + p.v
}

//go:noinline
func observe(u0, u1, u2, u3, u4, u5, u6, u7 any, b0, b1 box, p *item) int64 {
	vals := []any{u0, u1, u2, u3, u4, u5, u6, u7, b0, b1, p}
	gcStress()
	sum := scanList(vals, "observe")
	gcStress()
	sum2 := scanList(vals, "observe-after")
	ck(sum == sum2, fmt.Sprintf("observe: interface args changed across GC seed=%d", uafSeed))
	return sum2
}

//go:noinline
func pressure(base int64, p *item, b0, b1 box, u0, u1, u2, u3, u4, u5, u6, u7 any) int64 {
	l0 := any(mkItem(base + 10))
	l1 := any(box{p: mkItem(base + 11), s: "l1", a: [2]*item{{v: base + 12, tag: "iface"}, {v: base + 13, tag: "iface"}}})
	l2 := any([]*item{mkItem(base + 14), mkItem(base + 15)})
	l3 := any("spill")
	l4 := any(nil)
	l5 := any(mkItem(base + 16))
	l6 := any(anonBox{P: mkItem(base + 17), T: "anon"})
	l7 := any(box{p: mkItem(base + 18), s: "l7", a: [2]*item{{v: base + 19, tag: "iface"}, {v: base + 20, tag: "iface"}}})

	locals := []any{l0, l1, l2, l3, l4, l5, l6, l7}
	args := []any{u0, u1, u2, u3, u4, u5, u6, u7, b0, b1, p}
	gcStress()
	wantLocals := scanAny(l0, 0, "l0") + scanAny(l1, 1, "l1") + scanAny(l2, 2, "l2") +
		scanAny(l3, 3, "l3") + scanAny(l4, 4, "l4") + scanAny(l5, 5, "l5") +
		scanAny(l6, 6, "l6") + scanAny(l7, 7, "l7") +
		scanList(locals, "locals-slice")
	wantArgs := scanList(args, "args-before")
	want := wantLocals + wantArgs
	// 深度递归造成 morestack/栈复制，随后 GC：local iface 对与形参必须全部可扫。
	deepN := deep(30, p)
	gcStress()
	got := scanAny(l0, 0, "l0-after") + scanAny(l1, 1, "l1-after") + scanAny(l2, 2, "l2-after") +
		scanAny(l3, 3, "l3-after") + scanAny(l4, 4, "l4-after") + scanAny(l5, 5, "l5-after") +
		scanAny(l6, 6, "l6-after") + scanAny(l7, 7, "l7-after") +
		scanList(locals, "locals-slice-after") + scanList(args, "args-after")
	got2 := observe(u0, u1, u2, u3, u4, u5, u6, u7, b0, b1, p)
	ck(want == got, fmt.Sprintf("pressure: locals/args sum want=%d got=%d seed=%d", want, got, uafSeed))
	ck(wantArgs == got2, fmt.Sprintf("pressure: observe sum want=%d got=%d seed=%d", wantArgs, got2, uafSeed))
	return got2 + deepN%7
}

func main() {
	uafSeed = seedFromEnv()
	for r := 0; r < 6; r++ {
		base := int64(r*1000) + uafSeed%29
		p := mkItem(base)
		b0 := box{p: mkItem(base + 1), s: "b0", a: [2]*item{{v: base + 2, tag: "iface"}, {v: base + 3, tag: "iface"}}}
		b1 := box{p: mkItem(base + 4), s: "b1", a: [2]*item{{v: base + 5, tag: "iface"}, {v: base + 6, tag: "iface"}}}
		u0 := any(mkItem(base + 20))
		u1 := any("u1")
		u2 := any(nil)
		u3 := any([]*item{mkItem(base + 21), mkItem(base + 22)})
		u4 := any(box{p: mkItem(base + 23), s: "u4", a: [2]*item{{v: base + 26, tag: "iface"}, {v: base + 27, tag: "iface"}}})
		u5 := any(anonBox{P: mkItem(base + 24), T: "u5"})
		u6 := any(mkItem(base + 25))
		u7 := any("u7")

		args := []any{u0, u1, u2, u3, u4, u5, u6, u7, b0, b1, p}
		want := scanList(args, "main-args")
		got := pressure(base, p, b0, b1, u0, u1, u2, u3, u4, u5, u6, u7)
		gcStress()
		after := scanList(args, "main-after")
		ck(after == want, fmt.Sprintf("round %d: caller interfaces changed after call seed=%d", r, uafSeed))
		if got < want {
			ck(false, fmt.Sprintf("round %d: pressure return too small got=%d want>=%d seed=%d", r, got, want, uafSeed))
		}
		if p.tag != "iface" || p.next == nil || p.next.tag != "iface" {
			ck(false, fmt.Sprintf("round %d: caller pointer corrupted seed=%d", r, uafSeed))
		}
	}

	finish("l17_spill_iface_pair")
}
