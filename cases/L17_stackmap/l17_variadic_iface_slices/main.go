// L17: 变参 interface 的重切片/别名与接口方法转发。caller 的 []any、callee 的
// resliced 变参切片、defer 闭包捕获的原始切片在 GC 后都必须指向有效对象。
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
	head *item
	list []*item
	tag  string
}

func (p payload) String() string { return p.tag }

func (p payload) sum() int64 {
	s := p.head.v + p.head.next.v
	for _, it := range p.list {
		s += it.v
	}
	return s
}

type summer interface {
	sum() int64
	String() string
}

type boxed struct {
	p *item
	w int64
}

func (b boxed) sum() int64     { return b.p.v * b.w }
func (b boxed) String() string { return "boxed" }

type holder struct{ args []any }

type variadicInvoker interface {
	foldAll(prefix string, vals ...any) int64
}

type vimpl struct {
	base int64
	keep *item
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

func foldArgs(args []any) int64 {
	sum := int64(0)
	for i, a := range args {
		switch x := a.(type) {
		case *item:
			if x == nil || x.next == nil {
				ck(false, fmt.Sprintf("fold arg[%d]: nil item seed=%d", i, uafSeed))
				continue
			}
			if x.name != "it" || x.next.name != "next" {
				ck(false, fmt.Sprintf("fold arg[%d]: item names corrupted seed=%d", i, uafSeed))
			}
			sum += x.v + x.next.v
		case payload:
			if x.head == nil || x.head.next == nil || len(x.list) != 2 || x.tag != "pay" {
				ck(false, fmt.Sprintf("fold arg[%d]: payload corrupted seed=%d", i, uafSeed))
				continue
			}
			sum += x.sum()
		case []*item:
			for _, it := range x {
				if it == nil {
					ck(false, fmt.Sprintf("fold arg[%d]: nil slice item seed=%d", i, uafSeed))
					continue
				}
				sum += it.v
			}
		case summer:
			if x.String() != "boxed" {
				ck(false, fmt.Sprintf("fold arg[%d]: unexpected summer %q seed=%d", i, x.String(), uafSeed))
			}
			sum += x.sum()
		case string:
			sum += int64(len(x))
		default:
			ck(false, fmt.Sprintf("fold arg[%d]: unexpected type %T seed=%d", i, a, uafSeed))
		}
	}
	return sum
}

// consume 把变参切片存进逃逸的 holder，并在切片存活期间两次 GC。
//
//go:noinline
func consume(args ...any) (int64, int64) {
	h := &holder{args: args}
	gcStress()
	a := foldArgs(h.args)
	gcStress()
	b := foldArgs(args)
	return a, b
}

//go:noinline
func (v *vimpl) foldAll(prefix string, vals ...any) int64 {
	gcStress()
	a := int64(len(prefix)) + v.base + foldArgs(vals)
	gcStress()
	b := foldArgs(vals)
	ck(b == a-int64(len(prefix))-v.base,
		fmt.Sprintf("foldAll: variadic slice changed across GC seed=%d", uafSeed))
	ck(v.keep != nil && v.keep.v == 77 && v.keep.name == "keep",
		fmt.Sprintf("foldAll: receiver keep corrupted seed=%d", uafSeed))
	gcStress()
	return a
}

func round(rnd int, inv variadicInvoker, invBase int64) {
	base := int64(rnd*1000) + uafSeed%17

	itA := &item{v: base + 1, name: "it", next: &item{v: base + 2, name: "next"}}
	itB := &item{v: base + 41, name: "it", next: &item{v: base + 42, name: "next"}}
	pay := payload{
		head: &item{v: base + 10, name: "ph", next: &item{v: base + 11, name: "pt"}},
		list: []*item{{v: base + 12, name: "pl"}, {v: base + 13, name: "pl"}},
		tag:  "pay",
	}
	bx := boxed{p: &item{v: base + 7, name: "bx"}, w: 3}
	label := "varargs"
	slice := []*item{{v: base + 21, name: "s0"}, {v: base + 22, name: "s1"}}

	args := make([]any, 0, 6)
	args = append(args, itA, pay, label, bx, slice, itB)

	expTotal := (base + 1) + (base + 2) +
		(base + 10) + (base + 11) + (base + 12) + (base + 13) +
		int64(len(label)) +
		(base+7)*3 +
		(base + 21) + (base + 22) +
		(base + 41) + (base + 42)
	expInner := expTotal - (base + 1) - (base + 2) - (base + 41) - (base + 42)

	// 函数末尾的 defer 闭包捕获整个 []any，deferreturn 前再做一次 GC 并校验。
	defer func() {
		gcStress()
		ck(foldArgs(args) == expTotal,
			fmt.Sprintf("round %d deferred variadic corrupted seed=%d", rnd, uafSeed))
	}()

	a, b := consume(args[1 : len(args)-1]...)
	ck(a == expInner && b == expInner,
		fmt.Sprintf("round %d consume got=%d/%d want=%d seed=%d", rnd, a, b, expInner, uafSeed))

	got := inv.foldAll("pre", args...)
	ck(got == int64(len("pre"))+invBase+expTotal,
		fmt.Sprintf("round %d foldAll got=%d want=%d seed=%d",
			rnd, got, int64(len("pre"))+invBase+expTotal, uafSeed))

	gcStress()
	ck(itA.v == base+1 && itA.next != nil && itA.next.v == base+2,
		fmt.Sprintf("round %d caller itA corrupted seed=%d", rnd, uafSeed))
	ck(pay.head != nil && pay.head.v == base+10 && pay.list[1] != nil && pay.list[1].v == base+13,
		fmt.Sprintf("round %d caller payload corrupted seed=%d", rnd, uafSeed))
	ck(slice[1] != nil && slice[1].v == base+22,
		fmt.Sprintf("round %d caller slice corrupted seed=%d", rnd, uafSeed))
}

func main() {
	uafSeed = seedFromEnv()
	v := &vimpl{base: 5, keep: &item{v: 77, name: "keep"}}
	var inv variadicInvoker = v

	for rnd := 0; rnd < 8; rnd++ {
		round(rnd, inv, v.base)
	}

	gcStress()
	ck(v.keep != nil && v.keep.v == 77 && v.keep.name == "keep",
		fmt.Sprintf("main: vimpl keep corrupted seed=%d", uafSeed))
	finish("l17_variadic_iface_slices")
}
