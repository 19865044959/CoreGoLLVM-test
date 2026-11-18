// L17: interface defer 变参边界。同一接口的 nil/空/1/13 实参展开、nil 切片、
// 注册后原地修改 backing array 与重绑定接口变量；defer 记录中的旧 receiver 生效。
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

type pusher interface {
	Push(tag string, vals ...any) int64
}

type appender interface {
	Append(vals ...*item) int64
}

type impl struct {
	keep  *item
	multi int64
	tag   string
}

var (
	failed   int32
	uafSeed  int64 = 1
	deferred []string
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
	return &item{v: base, tag: "defer", next: &item{v: base + 7, tag: "defer-next"}}
}

func itemVal(x *item, where string) int64 {
	if x == nil {
		ck(false, fmt.Sprintf("%s: nil item seed=%d", where, uafSeed))
		return 0
	}
	if x.tag != "defer" || x.next == nil || x.next.tag != "defer-next" || x.next.v != x.v+7 {
		ck(false, fmt.Sprintf("%s: item chain corrupted seed=%d", where, uafSeed))
	}
	return x.v + x.next.v
}

func scanAny(a any, where string, idx int) int64 {
	switch x := a.(type) {
	case nil:
		return 0
	case *item:
		return itemVal(x, fmt.Sprintf("%s[%d]", where, idx))
	case pair:
		return itemVal(x.p, fmt.Sprintf("%s[%d].p", where, idx)) + int64(len(x.s))
	case []*item:
		var sum int64
		for i, p := range x {
			sum += itemVal(p, fmt.Sprintf("%s[%d].list[%d]", where, idx, i))
		}
		return sum
	case string:
		return int64(len(x))
	case int64:
		return x
	default:
		ck(false, fmt.Sprintf("%s: unexpected type %T at %d seed=%d", where, a, idx, uafSeed))
		return 0
	}
}

func scanArgs(vals []any, where string) int64 {
	var sum int64
	for i, v := range vals {
		sum += scanAny(v, where, i)
	}
	return sum
}

func scanPtrs(vals []*item, where string) int64 {
	var sum int64
	for i, p := range vals {
		sum += itemVal(p, fmt.Sprintf("%s[%d]", where, i))
	}
	return sum
}

func (i *impl) Push(tag string, vals ...any) int64 {
	pre := scanArgs(vals, "push-pre")
	gcStress()
	if i.keep == nil || i.tag != "orig" {
		ck(false, fmt.Sprintf("push %s: defer receiver corrupted tag=%q seed=%d", tag, i.tag, uafSeed))
	}
	post := scanArgs(vals, "push-post")
	if pre != post {
		ck(false, fmt.Sprintf("push %s: variadic slice changed across GC pre=%d post=%d seed=%d", tag, pre, post, uafSeed))
	}
	sum := post + int64(len(tag))*1000 + i.multi
	if vals == nil {
		sum += 1 << 40
	}
	deferred = append(deferred, fmt.Sprintf("push:%s:%s:%d", i.tag, tag, sum))
	return sum
}

func (i *impl) Append(vals ...*item) int64 {
	pre := scanPtrs(vals, "append-pre")
	gcStress()
	if i.keep == nil || i.tag != "orig" {
		ck(false, fmt.Sprintf("append: defer receiver corrupted tag=%q seed=%d", i.tag, uafSeed))
	}
	post := scanPtrs(vals, "append-post")
	if pre != post {
		ck(false, fmt.Sprintf("append: variadic slice changed across GC pre=%d post=%d seed=%d", pre, post, uafSeed))
	}
	sum := post + int64(len(vals))*100 + i.multi
	if vals == nil {
		sum += 1 << 41
	}
	deferred = append(deferred, fmt.Sprintf("append:%s:%d:%d", i.tag, len(vals), sum))
	return sum
}

func run(base int64) {
	deferred = nil
	p := pusher(&impl{keep: mkItem(base + 50), multi: 1, tag: "orig"})
	a := appender(&impl{keep: mkItem(base + 51), multi: 2, tag: "orig"})

	var nilArgs []any
	emptyArgs := []any{}
	one := []any{mkItem(base + 1)}
	many := make([]any, 0, 13)
	for j := 0; j < 13; j++ {
		switch j % 4 {
		case 0:
			many = append(many, mkItem(base+int64(j)))
		case 1:
			many = append(many, pair{p: mkItem(base + int64(j)), s: "many"})
		case 2:
			many = append(many, []*item{mkItem(base + int64(j)), mkItem(base + int64(j) + 100)})
		default:
			many = append(many, fmt.Sprintf("m%d", j))
		}
	}
	mut := []any{mkItem(base + 2), pair{p: mkItem(base + 3), s: "mut"}, "tail"}
	mutWant := scanArgs(mut, "mut-before")

	defer p.Push("zero")
	defer p.Push("nil", nilArgs...)
	defer p.Push("empty", emptyArgs...)
	defer p.Push("one", one...)
	defer p.Push("many", many...)
	defer p.Push("mut", mut...)

	mut[0] = mkItem(base + 99)
	mut[2] = "tail-mut"
	mutAfter := scanArgs(mut, "mut-after")
	if mutWant == mutAfter {
		ck(false, fmt.Sprintf("mutation did not change mut slice seed=%d", uafSeed))
	}

	var nilPtrs []*item
	ptrs := []*item{mkItem(base + 4), mkItem(base + 5)}
	defer a.Append()
	defer a.Append(nilPtrs...)
	defer a.Append(ptrs...)

	// 重绑定接口变量：defer 记录中的旧 receiver 必须继续生效。
	p = pusher(&impl{keep: mkItem(base + 60), multi: 999, tag: "rebound"})
	a = appender(&impl{keep: mkItem(base + 61), multi: 999, tag: "rebound"})
	gcStress()
	if len(deferred) != 0 {
		ck(false, fmt.Sprintf("deferred calls ran before return seed=%d", uafSeed))
	}
}

func main() {
	uafSeed = seedFromEnv()
	for r := 0; r < 3; r++ {
		base := int64(r*1000) + uafSeed%43
		run(base)
		gcStress()

		pushWant := func(tag string, vals []any) string {
			sum := scanArgs(vals, "want-"+tag) + int64(len(tag))*1000 + 1
			if vals == nil {
				sum += 1 << 40
			}
			return fmt.Sprintf("push:orig:%s:%d", tag, sum)
		}
		appendWant := func(n int, vals []*item) string {
			sum := scanPtrs(vals, "want-append") + int64(n)*100 + 2
			if vals == nil {
				sum += 1 << 41
			}
			return fmt.Sprintf("append:orig:%d:%d", n, sum)
		}

		var nilArgs []any
		emptyArgs := []any{}
		one := []any{mkItem(base + 1)}
		many := make([]any, 0, 13)
		for j := 0; j < 13; j++ {
			switch j % 4 {
			case 0:
				many = append(many, mkItem(base+int64(j)))
			case 1:
				many = append(many, pair{p: mkItem(base + int64(j)), s: "many"})
			case 2:
				many = append(many, []*item{mkItem(base + int64(j)), mkItem(base + int64(j) + 100)})
			default:
				many = append(many, fmt.Sprintf("m%d", j))
			}
		}
		mut := []any{mkItem(base + 99), pair{p: mkItem(base + 3), s: "mut"}, "tail-mut"}
		var nilPtrs []*item
		ptrs := []*item{mkItem(base + 4), mkItem(base + 5)}

		wants := []string{
			pushWant("zero", nil),
			pushWant("nil", nilArgs),
			pushWant("empty", emptyArgs),
			pushWant("one", one),
			pushWant("many", many),
			pushWant("mut", mut),
			appendWant(0, nil),
			appendWant(0, nilPtrs),
			appendWant(2, ptrs),
		}
		if len(deferred) != len(wants) {
			ck(false, fmt.Sprintf("round %d: deferred calls=%d want=%d seed=%d", r, len(deferred), len(wants), uafSeed))
		}
		counts := make(map[string]int, len(wants))
		for _, w := range wants {
			counts[w]++
		}
		for _, got := range deferred {
			counts[got]--
		}
		for w, n := range counts {
			if n != 0 {
				ck(false, fmt.Sprintf("round %d: deferred record %q count=%d seed=%d", r, w, n, uafSeed))
			}
		}
	}

	finish("l17_iface_defer_variadic_edge")
}
