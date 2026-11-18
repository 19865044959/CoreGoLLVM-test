// L17: 方法中的命名返回 + defer。defer 在 deferreturn 读取命名返回值与 receiver，GC 落在中间。
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

type tracker struct {
	base *item
	ops  []int64
}

type fixer struct {
	tag int64
}

type valTracker struct {
	root *item
	mul  int64
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

// stamp 作为方法值被 defer 捕获：receiver 与实参在 defer 语句处求值。
//
//go:noinline
func (t *tracker) stamp(code int64, p *item) {
	gcStress()
	if p == nil || p.name != "base" || p.next == nil || p.next.name != "base-tail" {
		ck(false, fmt.Sprintf("stamp code=%d: captured arg corrupted", code))
	}
	t.ops = append(t.ops, code)
}

// run 同时覆盖：命名返回值、defer 闭包读写命名返回、defer 方法值、defer 实参求值。
//
//go:noinline
func (t *tracker) run(sel int64, tag int64) (out *item, total int64, err error) {
	defer func() {
		gcStress()
		if out == nil || out.name != "final" {
			ck(false, "run: named return out corrupted in defer")
		} else if out.v != tag+7 && out.v != tag+107 {
			ck(false, fmt.Sprintf("run: named return out.v=%d seed=%d", out.v, uafSeed))
		}
		t.ops = append(t.ops, 1)
		total += 10
	}()
	defer t.stamp(2, t.base)
	defer func(p *item) {
		gcStress()
		if p == nil || p.v != tag+30 || p.name != "deferarg" {
			ck(false, "run: evaluated defer argument corrupted")
		}
		t.ops = append(t.ops, 3)
		total += p.v
	}(&item{v: tag + 30, name: "deferarg"})

	gcStress()
	if t.base == nil || t.base.v != tag+1 || t.base.next == nil || t.base.next.v != tag+2 {
		ck(false, "run: receiver state corrupted after GC")
	}
	out = &item{v: tag + 7, name: "body"}
	total = sel + tag
	if sel == 1 {
		gcStress()
		out = &item{v: tag + 107, name: "final"}
	} else {
		out = &item{v: tag + 7, name: "final"}
	}
	return
}

// retag 在 defer 中直接覆盖命名指针返回值。
//
//go:noinline
func (f *fixer) retag() (out *item) {
	defer func() {
		gcStress()
		out = &item{v: f.tag + 1, name: "defer-win"}
	}()
	out = &item{v: f.tag, name: "body"}
	gcStress()
	return
}

// calc 值接收者方法：receiver 副本与命名返回值都被 defer 闭包捕获。
//
//go:noinline
func (v valTracker) calc(sel int64) (out *item, n int64) {
	defer func() {
		gcStress()
		if out == nil || out.name != "val" {
			ck(false, "calc: named return out corrupted in defer")
		}
		n += out.v * v.mul
		if v.root == nil || v.root.v != 400 {
			ck(false, "calc: value receiver root corrupted")
		}
	}()
	gcStress()
	out = &item{v: 5 + sel, name: "val"}
	n = 3
	return
}

func main() {
	uafSeed = seedFromEnv()
	const rounds = 10

	for rnd := 0; rnd < rounds; rnd++ {
		tag := int64(rnd*100) + uafSeed%7
		sel := int64(rnd % 2)
		t := &tracker{base: &item{v: tag + 1, name: "base", next: &item{v: tag + 2, name: "base-tail"}}}

		out, total, err := t.run(sel, tag)
		ck(err == nil, fmt.Sprintf("round %d: run err", rnd))
		gcStress()
		wantOut := tag + 7
		if sel == 1 {
			wantOut = tag + 107
		}
		if out == nil || out.v != wantOut || out.name != "final" {
			ck(false, fmt.Sprintf("round %d: run out=%v want=%d seed=%d", rnd, out, wantOut, uafSeed))
		}
		wantTotal := sel + tag + (tag + 30) + 10
		ck(total == wantTotal, fmt.Sprintf("round %d: run total=%d want=%d seed=%d", rnd, total, wantTotal, uafSeed))

		okOps := len(t.ops) == 3 && t.ops[0] == 3 && t.ops[1] == 2 && t.ops[2] == 1
		ck(okOps, fmt.Sprintf("round %d: defer order=%v want=[3 2 1] seed=%d", rnd, t.ops, uafSeed))
		if t.base == nil || t.base.v != tag+1 || t.base.next == nil || t.base.next.v != tag+2 {
			ck(false, fmt.Sprintf("round %d: tracker base corrupted after GC seed=%d", rnd, uafSeed))
		}

		f := &fixer{tag: tag + 9}
		ov := f.retag()
		gcStress()
		if ov == nil || ov.v != tag+10 || ov.name != "defer-win" {
			ck(false, fmt.Sprintf("round %d: retag defer overwrite seed=%d", rnd, uafSeed))
		}

		vt := valTracker{root: &item{v: 400, name: "root"}, mul: sel + 2}
		vo, vn := vt.calc(sel)
		gcStress()
		wantVN := int64(3) + (5+sel)*(sel+2)
		if vo == nil || vo.v != 5+sel || vo.name != "val" {
			ck(false, fmt.Sprintf("round %d: calc value return seed=%d", rnd, uafSeed))
		}
		ck(vn == wantVN, fmt.Sprintf("round %d: calc n=%d want=%d seed=%d", rnd, vn, wantVN, uafSeed))
		ck(vt.root != nil && vt.root.v == 400, fmt.Sprintf("round %d: valTracker root corrupted seed=%d", rnd, uafSeed))
	}

	finish("l17_method_named_defer")
}
