// L17: 方法命名返回 + defer。方法把 named result 的地址交给 defer 方法值，
// 后者在 deferreturn 前 GC 并登记到 notes；闭包 defer 再改写命名返回值。
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
}

type result struct {
	id   int64
	head *item
	list []*item
	text string
}

type proc struct {
	base  int64
	notes []*result
	order []string
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

// note 作为 defer 方法值注册，&out 是命名返回槽的地址。
//
//go:noinline
func (p *proc) note(out *result, tag string) {
	p.order = append(p.order, tag)
	p.notes = append(p.notes, out)
	gcStress()
	if out.head == nil || out.head.v != out.id || out.head.name != "head" {
		ck(false, fmt.Sprintf("note: named return head corrupted seed=%d", uafSeed))
	}
	if len(out.list) != 2 || out.list[0] == nil || out.list[1] == nil {
		ck(false, fmt.Sprintf("note: named return list corrupted seed=%d", uafSeed))
	}
	if out.text != "body" {
		ck(false, fmt.Sprintf("note: named return text=%q seed=%d", out.text, uafSeed))
	}
	gcStress()
}

//go:noinline
func (p *proc) run(sel int64) (out result, head *item, err error) {
	defer func() {
		p.order = append(p.order, "closure")
		gcStress()
		out.text += "+defer"
		out.list = append(out.list, &item{v: out.id + 900, name: "tail"})
		head = out.head
		err = nil
		gcStress()
		if out.head == nil || out.head.v != out.id {
			ck(false, fmt.Sprintf("run closure: named return corrupted seed=%d", uafSeed))
		}
	}()
	defer p.note(&out, "note")

	out = result{
		id:   p.base + sel,
		head: &item{v: p.base + sel, name: "head"},
		list: []*item{{v: p.base + sel + 1, name: "l0"}, {v: p.base + sel + 2, name: "l1"}},
		text: "body",
	}
	head = out.head
	// 命名返回槽与 &out 在 GC 期间都必须有效。
	gcStress()
	return
}

func verifyResult(out result, head *item, err error, base, sel int64, where string) {
	wantID := base + sel
	if out.id != wantID {
		ck(false, fmt.Sprintf("%s: id=%d want=%d seed=%d", where, out.id, wantID, uafSeed))
	}
	if out.text != "body+defer" {
		ck(false, fmt.Sprintf("%s: text=%q seed=%d", where, out.text, uafSeed))
	}
	if len(out.list) != 3 || out.list[0] == nil || out.list[1] == nil || out.list[2] == nil {
		ck(false, fmt.Sprintf("%s: list corrupted len=%d seed=%d", where, len(out.list), uafSeed))
	} else {
		if out.list[0].v != wantID+1 || out.list[1].v != wantID+2 || out.list[2].v != wantID+900 {
			ck(false, fmt.Sprintf("%s: list values corrupted seed=%d", where, uafSeed))
		}
	}
	if out.head == nil || out.head.v != wantID || out.head.name != "head" {
		ck(false, fmt.Sprintf("%s: out.head corrupted seed=%d", where, uafSeed))
	}
	if head != out.head {
		ck(false, fmt.Sprintf("%s: pointer result != struct field seed=%d", where, uafSeed))
	}
	if err != nil {
		ck(false, fmt.Sprintf("%s: unexpected error %v seed=%d", where, err, uafSeed))
	}
}

func verifyNote(p *proc, idx int, base, sel int64, where string) {
	if idx < 0 || idx >= len(p.notes) || p.notes[idx] == nil {
		ck(false, fmt.Sprintf("%s: notes[%d] missing seed=%d", where, idx, uafSeed))
		return
	}
	n := p.notes[idx]
	if n.id != base+sel || n.text != "body+defer" {
		ck(false, fmt.Sprintf("%s: notes[%d] escaped named return corrupted seed=%d", where, idx, uafSeed))
	}
	if len(n.list) != 3 || n.list[0] == nil || n.list[1] == nil || n.list[2] == nil {
		ck(false, fmt.Sprintf("%s: notes[%d] list corrupted seed=%d", where, idx, uafSeed))
	} else if n.list[2].v != base+sel+900 {
		ck(false, fmt.Sprintf("%s: notes[%d] list values corrupted seed=%d", where, idx, uafSeed))
	}
}

func main() {
	uafSeed = seedFromEnv()
	p := &proc{base: 1000 + uafSeed%13}
	for r := 0; r < 10; r++ {
		sel := int64(r*3) + uafSeed%5
		out, head, err := p.run(sel)
		gcStress()
		verifyResult(out, head, err, p.base, sel, fmt.Sprintf("round %d direct", r))
		verifyNote(p, len(p.notes)-1, p.base, sel, fmt.Sprintf("round %d direct", r))
		if len(p.notes) > 0 {
			n := p.notes[len(p.notes)-1]
			if n.head != out.head {
				ck(false, fmt.Sprintf("round %d: notes head != returned head seed=%d", r, uafSeed))
			}
			if len(n.list) == 3 && len(out.list) == 3 && n.list[0] != out.list[0] {
				ck(false, fmt.Sprintf("round %d: notes list backing changed seed=%d", r, uafSeed))
			}
		}

		// 同名方法经方法值调用，命名返回/defer 逻辑不变。
		sel2 := sel + 500
		mv := p.run
		out2, head2, err2 := mv(sel2)
		gcStress()
		verifyResult(out2, head2, err2, p.base, sel2, fmt.Sprintf("round %d method-value", r))
		verifyNote(p, len(p.notes)-1, p.base, sel2, fmt.Sprintf("round %d method-value", r))
	}

	wantOrder := make([]string, 0, 4*10)
	for r := 0; r < 10; r++ {
		wantOrder = append(wantOrder, "note", "closure", "note", "closure")
	}
	if len(p.order) != len(wantOrder) {
		ck(false, fmt.Sprintf("defer order length=%d want=%d seed=%d", len(p.order), len(wantOrder), uafSeed))
	} else {
		for i := range wantOrder {
			if p.order[i] != wantOrder[i] {
				ck(false, fmt.Sprintf("defer order[%d]=%q want=%q seed=%d", i, p.order[i], wantOrder[i], uafSeed))
			}
		}
	}
	if len(p.notes) != 20 {
		ck(false, fmt.Sprintf("notes count=%d want=20 seed=%d", len(p.notes), uafSeed))
	}

	finish("l17_named_ret_method_sink")
}
