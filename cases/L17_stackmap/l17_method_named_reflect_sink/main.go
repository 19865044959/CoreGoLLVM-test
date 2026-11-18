// L17: 方法命名返回作为 deferred sink。defer 中先经 interface 方法、再经
// reflect 写回 named return 的指针/切片/interface 字段；另有值拷贝快照 defer。
package main

import (
	"fmt"
	"os"
	"reflect"
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

type boxed struct {
	p   *item
	tag string
}

type record struct {
	V    int64
	P    *item
	List []*item
	Any  any
	Tag  string
}

type filler interface {
	fill(r *record, seen *[]string)
}

type fillImpl struct {
	keep *item
	tag  string
}

type recorder struct {
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

func itemVal(x *item, where string) int64 {
	if x == nil {
		ck(false, fmt.Sprintf("%s: nil item seed=%d", where, uafSeed))
		return 0
	}
	if x.tag != "if" || x.next == nil || x.next.tag != "if-next" || x.next.v != x.v+5 {
		ck(false, fmt.Sprintf("%s: item chain corrupted seed=%d", where, uafSeed))
	}
	return x.v + x.next.v
}

func recordSum(r record, where string) int64 {
	sum := r.V + itemVal(r.P, where+".P")
	for i, p := range r.List {
		sum += itemVal(p, fmt.Sprintf("%s.List[%d]", where, i))
	}
	switch x := r.Any.(type) {
	case nil:
	case *item:
		sum += itemVal(x, where+".Any")
	case pair:
		sum += itemVal(x.p, where+".Any.pair") + int64(len(x.s))
	case boxed:
		sum += itemVal(x.p, where+".Any.boxed") + int64(len(x.tag))
	case string:
		sum += int64(len(x))
	default:
		ck(false, fmt.Sprintf("%s: unexpected any type %T seed=%d", where, x, uafSeed))
	}
	return sum + int64(len(r.Tag))
}

func (f *fillImpl) fill(r *record, seen *[]string) {
	gcStress()
	if f.keep == nil || f.tag != "fill" {
		ck(false, fmt.Sprintf("fill: receiver corrupted seed=%d", uafSeed))
	}
	r.Tag = f.tag
	r.P = f.keep
	r.List = append(r.List, f.keep)
	r.Any = boxed{p: f.keep, tag: "fill-box"}
	*seen = append(*seen, "fill")
}

func (rec *recorder) run(base int64, mode int) (out record, snap int64, seen []string) {
	out = record{
		V:    base,
		P:    mkItem(base + 1),
		List: []*item{mkItem(base + 2)},
		Any:  pair{p: mkItem(base + 3), s: "seed-pair"},
		Tag:  "seed",
	}
	if mode%2 == 0 {
		out.Any = "seed-string"
	}
	seedWant := recordSum(out, "seed")

	f := filler(&fillImpl{keep: mkItem(base + 10), tag: "fill"})
	defer f.fill(&out, &seen)
	defer func() {
		gcStress()
		rv := reflect.ValueOf(&out).Elem()
		rv.FieldByName("V").SetInt(base + 20)
		rv.FieldByName("P").Set(reflect.ValueOf(mkItem(base + 21)))
		rv.FieldByName("List").Set(reflect.ValueOf([]*item{mkItem(base + 22), mkItem(base + 23)}))
		rv.FieldByName("Any").Set(reflect.ValueOf(boxed{p: mkItem(base + 24), tag: "reflect-box"}))
		rv.FieldByName("Tag").SetString("reflect")
		seen = append(seen, "reflect")
	}()
	defer func(x record) {
		gcStress()
		snap = recordSum(x, "snapshot")
		seen = append(seen, "snap")
	}(out)

	// 注册后重绑定接口变量：defer 记录必须保留旧 receiver。
	f = filler(&fillImpl{keep: mkItem(base + 99), tag: "rebound"})
	gcStress()
	if snap != 0 {
		ck(false, fmt.Sprintf("snapshot defer ran before return seed=%d", uafSeed))
	}
	if seedWant == 0 {
		ck(false, fmt.Sprintf("seed record empty seed=%d", uafSeed))
	}
	return out, 0, seen
}

func main() {
	uafSeed = seedFromEnv()
	for r := 0; r < 6; r++ {
		base := int64(r*1000) + uafSeed%41
		mode := (r + int(uafSeed)) % 2
		out, snap, seen := (&recorder{keep: mkItem(base + 50), tag: "rec"}).run(base, mode)
		gcStress()

		wantSnap := recordSum(record{
			V:    base,
			P:    mkItem(base + 1),
			List: []*item{mkItem(base + 2)},
			Any:  pair{p: mkItem(base + 3), s: "seed-pair"},
			Tag:  "seed",
		}, "want-snap")
		if mode%2 == 0 {
			wantSnap = recordSum(record{
				V:    base,
				P:    mkItem(base + 1),
				List: []*item{mkItem(base + 2)},
				Any:  "seed-string",
				Tag:  "seed",
			}, "want-snap-str")
		}
		if snap != wantSnap {
			ck(false, fmt.Sprintf("round %d: snapshot sum=%d want=%d seed=%d", r, snap, wantSnap, uafSeed))
		}
		if len(seen) != 3 || seen[0] != "snap" || seen[1] != "reflect" || seen[2] != "fill" {
			ck(false, fmt.Sprintf("round %d: defer order=%v want=[snap reflect fill] seed=%d", r, seen, uafSeed))
		}

		if out.V != base+20 || out.Tag != "fill" {
			ck(false, fmt.Sprintf("round %d: named return scalar V=%d tag=%q seed=%d", r, out.V, out.Tag, uafSeed))
		}
		if out.P == nil || out.P.v != base+10 || out.P.tag != "if" {
			ck(false, fmt.Sprintf("round %d: named return P not filled by old receiver seed=%d", r, uafSeed))
		}
		if len(out.List) != 3 || out.List[2] != out.P {
			ck(false, fmt.Sprintf("round %d: named return List len=%d seed=%d", r, len(out.List), uafSeed))
		}
		for i, p := range out.List {
			itemVal(p, fmt.Sprintf("round %d out.List[%d]", r, i))
		}
		b, ok := out.Any.(boxed)
		if !ok || b.p != out.P || b.tag != "fill-box" {
			ck(false, fmt.Sprintf("round %d: named return Any not filled seed=%d", r, uafSeed))
		}
		if got := recordSum(out, "main-out"); got <= 0 {
			ck(false, fmt.Sprintf("round %d: returned record checksum=%d seed=%d", r, got, uafSeed))
		}
	}

	finish("l17_method_named_reflect_sink")
}
