// L17: struct 按值返回后同时作为两个 defer 实参与一个方法值 defer 的参数。
// 注册后原地重绑局部 struct 的指针字段；defer 记录中的浅拷贝必须仍指向原对象。
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

type pair struct {
	p *item
	s string
}

type result struct {
	p    *item
	list []*item
	arr  [2]*item
	anyv any
	tag  string
}

type checker struct {
	keep *item
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

func mkItem(base int64) *item {
	return &item{v: base, tag: "if", next: &item{v: base + 5, tag: "if-next"}}
}

func checkItem(x *item, where string) {
	if x == nil {
		ck(false, fmt.Sprintf("%s: nil item seed=%d", where, uafSeed))
		return
	}
	if x.tag != "if" || x.next == nil || x.next.tag != "if-next" || x.next.v != x.v+5 {
		ck(false, fmt.Sprintf("%s: item chain corrupted seed=%d", where, uafSeed))
	}
}

func itemVal(x *item, where string) int64 {
	if x == nil {
		ck(false, fmt.Sprintf("%s: nil item seed=%d", where, uafSeed))
		return 0
	}
	checkItem(x, where)
	return x.v + x.next.v
}

func makeResult(base int64, mode int) result {
	r := result{
		p:    mkItem(base),
		arr:  [2]*item{mkItem(base + 1), mkItem(base + 2)},
		tag:  "res",
		anyv: mkItem(base + 3),
	}
	switch mode % 3 {
	case 0:
		r.list = nil
	case 1:
		r.list = []*item{mkItem(base + 10), mkItem(base + 11)}
		r.anyv = pair{p: mkItem(base + 12), s: "pair"}
	default:
		r.list = []*item{mkItem(base + 20)}
		r.anyv = "any-mode"
	}
	return r
}

func resultSum(r result, where string) int64 {
	sum := itemVal(r.p, where+".p")
	for i, p := range r.list {
		sum += itemVal(p, fmt.Sprintf("%s.list[%d]", where, i))
	}
	for i, p := range r.arr {
		sum += itemVal(p, fmt.Sprintf("%s.arr[%d]", where, i))
	}
	switch x := r.anyv.(type) {
	case nil:
	case *item:
		sum += itemVal(x, where+".anyv")
	case pair:
		sum += itemVal(x.p, where+".anyv.pair") + int64(len(x.s))
	case string:
		sum += int64(len(x))
	default:
		ck(false, fmt.Sprintf("%s: unexpected any type %T seed=%d", where, x, uafSeed))
	}
	return sum + int64(len(r.tag))
}

func (c checker) take(r result, out *int64, order *[]string) {
	gcStress()
	if c.keep == nil || c.tag != "chk" {
		ck(false, fmt.Sprintf("method-value defer: receiver corrupted seed=%d", uafSeed))
	}
	*out = resultSum(r, "defer-take") + itemVal(c.keep, "defer-take.keep") + int64(len(c.tag))
	*order = append(*order, "take")
}

func run(base int64, mode int) (out result, sums [3]int64, order []string, want int64, keepV int64) {
	orig := makeResult(base, mode)
	want = resultSum(orig, "defer-orig")

	h := checker{keep: mkItem(base + 70), tag: "chk"}
	keepV = itemVal(h.keep, "run-keep")

	defer func(x result) {
		gcStress()
		sums[0] = resultSum(x, "defer-A")
		order = append(order, "A")
	}(orig)
	defer h.take(orig, &sums[1], &order)
	defer func(x result) {
		gcStress()
		sums[2] = resultSum(x, "defer-B")
		order = append(order, "B")
	}(orig)

	// 注册后重绑局部字段：defer 实参的浅拷贝必须不受影响。
	orig.p = mkItem(base + 200)
	orig.list = append(orig.list, mkItem(base+201))
	orig.arr[0] = mkItem(base + 202)
	orig.anyv = "mutated"
	orig.tag = "mutated-tag"
	mutWant := resultSum(orig, "mutated-orig")

	gcStress()
	if sums != [3]int64{} {
		ck(false, fmt.Sprintf("deferred calls ran before return seed=%d", uafSeed))
	}
	if len(order) != 0 {
		ck(false, fmt.Sprintf("defer order not empty before return seed=%d", uafSeed))
	}

	// 让编译期知道 expect 值参与后续校验。
	if want == mutWant {
		ck(false, fmt.Sprintf("mutation did not change struct checksum seed=%d", uafSeed))
	}
	return orig, sums, order, want, keepV
}

func main() {
	uafSeed = seedFromEnv()
	for r := 0; r < 6; r++ {
		base := int64(r*1000) + uafSeed%37
		mode := (r + int(uafSeed)) % 3
		out, sums, order, want, keepV := run(base, mode)
		gcStress()

		if got := resultSum(out, "main-out"); got == 0 {
			ck(false, fmt.Sprintf("round %d: returned struct lost all fields seed=%d", r, uafSeed))
		}
		checkItem(out.p, fmt.Sprintf("round %d out.p", r))
		if out.p.v != base+200 {
			ck(false, fmt.Sprintf("round %d: returned p.v=%d want=%d seed=%d", r, out.p.v, base+200, uafSeed))
		}
		if out.tag != "mutated-tag" || out.anyv != "mutated" {
			ck(false, fmt.Sprintf("round %d: returned scalar fields lost seed=%d", r, uafSeed))
		}
		for i, p := range out.list {
			checkItem(p, fmt.Sprintf("round %d out.list[%d]", r, i))
		}
		for i, p := range out.arr {
			checkItem(p, fmt.Sprintf("round %d out.arr[%d]", r, i))
		}

		if sums[0] != want || sums[2] != want {
			ck(false, fmt.Sprintf("round %d: deferred closure sums=%v want=%d seed=%d", r, sums, want, uafSeed))
		}
		wantTake := want + keepV + int64(len("chk"))
		if sums[1] != wantTake {
			ck(false, fmt.Sprintf("round %d: deferred method sum=%d want=%d seed=%d", r, sums[1], wantTake, uafSeed))
		}
		if len(order) != 3 || order[0] != "B" || order[1] != "take" || order[2] != "A" {
			ck(false, fmt.Sprintf("round %d: defer LIFO order=%v want=[B take A] seed=%d", r, order, uafSeed))
		}
		if mutWant := resultSum(out, "main-mut"); mutWant == want {
			ck(false, fmt.Sprintf("round %d: returned struct equals pre-mutation seed=%d", r, uafSeed))
		}
	}

	finish("l17_struct_ret_defer_pair")
}
