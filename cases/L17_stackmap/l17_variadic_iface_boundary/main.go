// L17: 变参 interface 三层转发边界。0/N 实参、nil 与空切片展开、13 实参
// 溢出场景逐层经过接口方法转发；每层 GC 后重扫 []any backing array 与接口值。
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

func checkItem(x *item, where string, idx int) {
	if x == nil {
		ck(false, fmt.Sprintf("%s: nil item at %d seed=%d", where, idx, uafSeed))
		return
	}
	if x.tag != "if" || x.next == nil || x.next.tag != "if-next" || x.next.v != x.v+5 {
		ck(false, fmt.Sprintf("%s: item chain corrupted at %d seed=%d", where, idx, uafSeed))
	}
}

func scanVal(a any, where string, idx int) int64 {
	switch x := a.(type) {
	case nil:
		return 0
	case *item:
		if x == nil {
			return 0
		}
		checkItem(x, where, idx)
		return x.v + x.next.v
	case pair:
		if x.p == nil {
			ck(false, fmt.Sprintf("%s: nil pair.p at %d seed=%d", where, idx, uafSeed))
			return 0
		}
		checkItem(x.p, where, idx)
		return x.p.v + int64(len(x.s))
	case []*item:
		var sum int64
		for i, p := range x {
			if p == nil {
				ck(false, fmt.Sprintf("%s: nil elem %d/%d at %d seed=%d", where, i, len(x), idx, uafSeed))
				continue
			}
			checkItem(p, where, idx)
			sum += p.v + p.next.v
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
		sum += scanVal(v, where, i)
	}
	return sum
}

func tailOf(vals []any) *item {
	for i := len(vals) - 1; i >= 0; i-- {
		switch x := vals[i].(type) {
		case *item:
			if x != nil {
				return x
			}
		case pair:
			if x.p != nil {
				return x.p
			}
		case []*item:
			if len(x) > 0 && x[len(x)-1] != nil {
				return x[len(x)-1]
			}
		}
	}
	return nil
}

func nilMarker(vals []any) int64 {
	if vals == nil {
		return 1 << 40
	}
	return 0
}

type forwarder interface {
	forward(tag string, vals ...any) (int64, *item)
}

// 第三层：终结 sink，GC 前后各扫一次变参切片。
type tailSink struct {
	keep *item
	tag  string
}

func (s *tailSink) forward(tag string, vals ...any) (int64, *item) {
	pre := scanArgs(vals, "sink-pre")
	gcStress()
	post := scanArgs(vals, "sink-post")
	if pre != post {
		ck(false, fmt.Sprintf("sink: variadic slice changed across GC tag=%s pre=%d post=%d seed=%d", tag, pre, post, uafSeed))
	}
	if s.keep == nil || s.keep.tag != "if" {
		ck(false, fmt.Sprintf("sink: receiver corrupted tag=%s seed=%d", tag, uafSeed))
	}
	return post + int64(len(vals))*100 + nilMarker(vals) + 1, tailOf(vals)
}

// 第二层：接口转发 + nil/空/重切片边界校验。
type layerB struct {
	next forwarder
	tag  string
}

func (b *layerB) forward(tag string, vals ...any) (int64, *item) {
	gcStress()
	pre := scanArgs(vals, "b-pre")
	if vals != nil {
		head := vals[:0]
		mid := vals[:0]
		if len(vals) > 0 {
			mid = vals[1:]
			if len(mid) != len(vals)-1 {
				ck(false, fmt.Sprintf("b: reslice mid=%d len=%d seed=%d", len(mid), len(vals), uafSeed))
			}
		}
		if len(head) != 0 {
			ck(false, fmt.Sprintf("b: reslice head=%d seed=%d", len(head), uafSeed))
		}
		if len(vals) > 2 {
			full := scanArgs(vals, "b-full")
			first := scanVal(vals[0], "b-first", 0)
			if scanArgs(mid, "b-mid") != full-first {
				ck(false, fmt.Sprintf("b: reslice alias mismatch seed=%d", uafSeed))
			}
		}
	}
	out, tail := b.next.forward(tag+"-b", vals...)
	gcStress()
	post := scanArgs(vals, "b-post")
	if pre != post {
		ck(false, fmt.Sprintf("b: variadic slice changed across GC pre=%d post=%d seed=%d", pre, post, uafSeed))
	}
	if tail != nil {
		checkItem(tail, "b-tail", -1)
	}
	return out + 1, tail
}

// 第一层：接口转发入口。
type layerA struct {
	next forwarder
	tag  string
}

func (a *layerA) forward(tag string, vals ...any) (int64, *item) {
	gcStress()
	pre := scanArgs(vals, "a-pre")
	out, tail := a.next.forward(tag+"-a", vals...)
	gcStress()
	post := scanArgs(vals, "a-post")
	if pre != post {
		ck(false, fmt.Sprintf("a: variadic slice changed across GC pre=%d post=%d seed=%d", pre, post, uafSeed))
	}
	if tail != nil {
		checkItem(tail, "a-tail", -1)
	}
	return out + 1, tail
}

func buildArgs(base int64, n int) []any {
	vals := make([]any, 0, n)
	for i := 0; i < n; i++ {
		switch i % 5 {
		case 0:
			vals = append(vals, mkItem(base+int64(i)))
		case 1:
			vals = append(vals, pair{p: mkItem(base + int64(i)), s: "pair"})
		case 2:
			vals = append(vals, []*item{mkItem(base + int64(i)), mkItem(base + int64(i) + 100)})
		case 3:
			vals = append(vals, fmt.Sprintf("s%d", i))
		default:
			vals = append(vals, int64(i)+base)
		}
	}
	return vals
}

func expectedForward(vals []any) int64 {
	return scanArgs(vals, "expect") + int64(len(vals))*100 + nilMarker(vals) + 3
}

func main() {
	uafSeed = seedFromEnv()
	top := forwarder(&layerA{next: &layerB{next: &tailSink{keep: mkItem(uafSeed % 31), tag: "sink"}}, tag: "a"})

	for r := 0; r < 8; r++ {
		base := int64(r*1000) + uafSeed%97
		var args []any
		switch r % 5 {
		case 0:
			args = nil
		case 1:
			args = []any{}
		case 2:
			args = buildArgs(base, 1)
		case 3:
			args = buildArgs(base, 13)
		default:
			args = buildArgs(base, 0)
		}

		before := scanArgs(args, fmt.Sprintf("round %d caller-before", r))
		wantTail := tailOf(args)
		got, gotTail := top.forward(fmt.Sprintf("r%d", r), args...)
		gcStress()
		if got != expectedForward(args) {
			ck(false, fmt.Sprintf("round %d: forward sum=%d want=%d len=%d seed=%d", r, got, expectedForward(args), len(args), uafSeed))
		}
		if gotTail != wantTail {
			ck(false, fmt.Sprintf("round %d: tail pointer mismatch seed=%d", r, uafSeed))
		}
		if gotTail != nil {
			checkItem(gotTail, fmt.Sprintf("round %d caller-tail", r), -1)
		}
		after := scanArgs(args, fmt.Sprintf("round %d caller-after", r))
		if before != after {
			ck(false, fmt.Sprintf("round %d: caller args changed across call/GC before=%d after=%d seed=%d", r, before, after, uafSeed))
		}

		// 零实参（非展开）边界：内部 variadic 形参为 nil。
		zgot, ztail := top.forward("zero")
		gcStress()
		if zgot != int64(1<<40)+3 {
			ck(false, fmt.Sprintf("round %d: zero-arg sum=%d want=%d seed=%d", r, zgot, int64(1<<40)+3, uafSeed))
		}
		if ztail != nil {
			ck(false, fmt.Sprintf("round %d: zero-arg tail non-nil seed=%d", r, uafSeed))
		}
	}

	finish("l17_variadic_iface_boundary")
}
