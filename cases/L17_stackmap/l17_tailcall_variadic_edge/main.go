// L17: 尾位置变参累积的边界。交替 step/接口 fold 递归深度仅 0..5，覆盖 nil/空/
// 1/5 实参展开；每帧 defer 在回卷时 GC 并重扫本层变参切片。
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

type folder interface {
	fold(n int, acc ...any) (int64, []any)
}

type impl struct {
	keep *item
	tag  string
}

var (
	failed  int32
	uafSeed int64 = 1
	fn      folder
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
	for i := 0; i < 6; i++ {
		b := make([]byte, 1536)
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
	return &item{v: base, tag: "tail", next: &item{v: base + 8, tag: "tail-next"}}
}

func itemVal(x *item, where string) int64 {
	if x == nil {
		ck(false, fmt.Sprintf("%s: nil item seed=%d", where, uafSeed))
		return 0
	}
	if x.tag != "tail" || x.next == nil || x.next.tag != "tail-next" || x.next.v != x.v+8 {
		ck(false, fmt.Sprintf("%s: item chain corrupted seed=%d", where, uafSeed))
	}
	return x.v + x.next.v
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
		}
	}
	return nil
}

func scanAny(a any, where string, idx int) int64 {
	switch x := a.(type) {
	case nil:
		return 0
	case *item:
		if x == nil {
			return 0
		}
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

func checkFrame(where string, acc []any, l int, tail *item) {
	gcStress()
	if len(acc) != l {
		ck(false, fmt.Sprintf("%s: frame acc len=%d want=%d seed=%d", where, len(acc), l, uafSeed))
	}
	if tail != nil {
		itemVal(tail, where+".tail")
	}
	scanArgs(acc, where+".acc")
}

//go:noinline
func step(n int, acc ...any) (int64, []any) {
	l := len(acc)
	tail := tailOf(acc)
	defer func() {
		checkFrame("step-defer", acc, l, tail)
	}()
	if n <= 0 {
		return scanArgs(acc, "step-base"), acc
	}
	if n%2 == 1 {
		return fn.fold(n-1, append(acc, mkItem(int64(n)*10))...)
	}
	return step(n-1, append(acc, "s")...)
}

func (f *impl) fold(n int, acc ...any) (int64, []any) {
	l := len(acc)
	tail := tailOf(acc)
	defer func() {
		gcStress()
		if f.keep == nil || f.tag != "fold" {
			ck(false, fmt.Sprintf("fold-defer: receiver corrupted seed=%d", uafSeed))
		}
		if len(acc) != l {
			ck(false, fmt.Sprintf("fold-defer: frame acc len=%d want=%d seed=%d", len(acc), l, uafSeed))
		}
		if tail != nil {
			itemVal(tail, "fold-defer.tail")
		}
		scanArgs(acc, "fold-defer.acc")
	}()
	if n <= 0 {
		return scanArgs(acc, "fold-base"), acc
	}
	return step(n-1, append(acc, mkItem(int64(n)*10+100))...)
}

func main() {
	uafSeed = seedFromEnv()
	fn = folder(&impl{keep: mkItem(uafSeed % 23), tag: "fold"})

	for r := 0; r < 10; r++ {
		base := int64(r*1000) + uafSeed%67
		n := r % 6
		var in []any
		kind := r % 4
		switch kind {
		case 0:
			in = nil
		case 1:
			in = []any{}
		case 2:
			in = []any{mkItem(base)}
		default:
			in = []any{mkItem(base), "five", pair{p: mkItem(base + 1), s: "p"}, int64(3), mkItem(base + 2)}
		}
		if r == 9 {
			in = []any{(*item)(nil), "typed-nil", mkItem(base + 3)}
		}
		inLen := len(in)
		inSum := scanArgs(in, fmt.Sprintf("round %d input-before", r))
		inTail := tailOf(in)

		got, acc := step(n, in...)
		gcStress()
		if got != scanArgs(acc, fmt.Sprintf("round %d returned-acc", r)) {
			ck(false, fmt.Sprintf("round %d: returned sum=%d does not match acc after GC seed=%d", r, got, uafSeed))
		}
		wantLen := inLen + n
		if len(acc) != wantLen {
			ck(false, fmt.Sprintf("round %d: acc len=%d want=%d n=%d kind=%d seed=%d", r, len(acc), wantLen, n, kind, uafSeed))
		}
		if acc == nil && !(kind == 0 && n == 0) {
			ck(false, fmt.Sprintf("round %d: accumulated acc unexpectedly nil seed=%d", r, uafSeed))
		}
		gotTail := tailOf(acc)
		if gotTail == nil && got != 0 {
			ck(false, fmt.Sprintf("round %d: non-zero sum without tail seed=%d", r, uafSeed))
		}
		if gotTail != nil {
			itemVal(gotTail, fmt.Sprintf("round %d acc-tail", r))
		}
		if gotLen := scanArgs(acc, fmt.Sprintf("round %d acc-after-gc", r)); gotLen <= 0 && got > 0 {
			ck(false, fmt.Sprintf("round %d: acc lost contents got=%d seed=%d", r, gotLen, uafSeed))
		}

		// caller 输入切片在深层 append 后必须保持不变。
		if inLen != len(in) {
			ck(false, fmt.Sprintf("round %d: caller input len changed %d->%d seed=%d", r, inLen, len(in), uafSeed))
		}
		if after := scanArgs(in, fmt.Sprintf("round %d input-after", r)); after != inSum {
			ck(false, fmt.Sprintf("round %d: caller input changed across call %d->%d seed=%d", r, inSum, after, uafSeed))
		}
		if tailOf(in) != inTail {
			ck(false, fmt.Sprintf("round %d: caller input tail changed seed=%d", r, uafSeed))
		}

		// 深度 0 且 nil 展开：必须保留 nil（nil vs 空切片边界）。
		if kind == 0 && n == 0 {
			zSum, zAcc := step(0, in...)
			gcStress()
			if zSum != 0 || zAcc != nil {
				ck(false, fmt.Sprintf("round %d: nil-spread depth0 sum=%d nil=%v seed=%d", r, zSum, zAcc == nil, uafSeed))
			}
		}
	}

	finish("l17_tailcall_variadic_edge")
}
