// L17: defer 变参接口方法。defer 记录保存接口 receiver（itab+data）与变参切片；
// 注册后重绑定接口变量、原地修改变参 backing array，GC 后 deferreturn 再经 itab 调用。
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

type box struct {
	p   *item
	tag string
}

type flusher interface {
	Flush(prefix string, vals ...any) int64
}

type impl struct {
	keep *item
	tag  string
}

type valImpl struct {
	keep *item
	tag  string
}

var (
	failed       int32
	uafSeed      int64 = 1
	deferredSums []int64
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

func scanVal(v any, idx int, where string) int64 {
	switch x := v.(type) {
	case nil:
		return 0
	case *item:
		if x == nil || x.name != "flush" || x.next == nil || x.next.name != "tail" {
			ck(false, fmt.Sprintf("%s: item[%d] corrupted seed=%d", where, idx, uafSeed))
			return 0
		}
		return x.v + x.next.v
	case box:
		if x.p == nil || x.p.name != "flush" || x.tag == "" {
			ck(false, fmt.Sprintf("%s: box[%d] corrupted seed=%d", where, idx, uafSeed))
			return 0
		}
		return x.p.v + int64(len(x.tag))
	case []*item:
		sum := int64(0)
		for j, p := range x {
			if p == nil || p.name != "flush" {
				ck(false, fmt.Sprintf("%s: slice[%d][%d] corrupted seed=%d", where, idx, j, uafSeed))
				continue
			}
			sum += p.v
		}
		return sum
	case string:
		return int64(len(x))
	default:
		ck(false, fmt.Sprintf("%s: unexpected type %T at %d seed=%d", where, v, idx, uafSeed))
		return 0
	}
}

func scanVals(vals []any, where string) int64 {
	sum := int64(0)
	for i, v := range vals {
		sum += scanVal(v, i, where)
	}
	return sum
}

func mkFlushItem(v int64) *item {
	return &item{v: v, name: "flush", next: &item{v: v + 1, name: "tail"}}
}

//go:noinline
func (f *impl) Flush(prefix string, vals ...any) int64 {
	gcStress()
	if f.keep == nil || f.keep.name != "keep" {
		ck(false, fmt.Sprintf("impl.Flush: receiver corrupted tag=%q seed=%d", f.tag, uafSeed))
		return -1
	}
	if f.tag != "orig" {
		ck(false, fmt.Sprintf("impl.Flush: wrong receiver tag=%q seed=%d", f.tag, uafSeed))
	}
	sum := int64(len(prefix)) + f.keep.v + scanVals(vals, "impl.Flush")
	gcStress()
	sum2 := int64(len(prefix)) + f.keep.v + scanVals(vals, "impl.Flush-after")
	if sum != sum2 {
		ck(false, fmt.Sprintf("impl.Flush: variadic defer record changed across GC seed=%d", uafSeed))
	}
	deferredSums = append(deferredSums, sum2)
	return sum2
}

//go:noinline
func (v valImpl) Flush(prefix string, vals ...any) int64 {
	gcStress()
	if v.keep == nil || v.keep.name != "keep" {
		ck(false, fmt.Sprintf("valImpl.Flush: receiver corrupted tag=%q seed=%d", v.tag, uafSeed))
		return -1
	}
	if v.tag != "orig" {
		ck(false, fmt.Sprintf("valImpl.Flush: wrong receiver tag=%q seed=%d", v.tag, uafSeed))
	}
	sum := int64(len(prefix)) + v.keep.v + scanVals(vals, "valImpl.Flush")
	gcStress()
	sum2 := int64(len(prefix)) + v.keep.v + scanVals(vals, "valImpl.Flush-after")
	if sum != sum2 {
		ck(false, fmt.Sprintf("valImpl.Flush: value receiver copy changed across GC seed=%d", uafSeed))
	}
	deferredSums = append(deferredSums, sum2)
	return sum2
}

func expectedSum(prefix string, keep int64, vals []any) int64 {
	return int64(len(prefix)) + keep + scanVals(vals, "expected")
}

//go:noinline
func useDirect(f flusher, rebound flusher, keepOrig, keepRebound int64, rnd int) {
	args := []any{
		mkFlushItem(10 + int64(rnd)),
		box{p: mkFlushItem(20 + int64(rnd)), tag: "bx"},
		[]*item{mkFlushItem(30 + int64(rnd)), mkFlushItem(31 + int64(rnd))},
		"keep",
		nil,
		"last",
	}
	// defer 语句处对 f 和变参实参求值：接口值复制、args... 的切片头复制。
	defer f.Flush("direct", args...)

	// 原地修改 backing array 与接口变量，defer 记录应看到数组修改、看不到接口重绑定。
	args[3] = "mutated"
	args[len(args)-1] = "tail-mutated"
	f = rebound
	gcStress()
}

//go:noinline
func useExpr(f flusher, rebound flusher, keepOrig int64, rnd int) {
	args := []any{
		mkFlushItem(50 + int64(rnd)),
		box{p: mkFlushItem(60 + int64(rnd)), tag: "ex"},
		[]*item{mkFlushItem(70 + int64(rnd))},
		"expr",
		nil,
	}
	fn := flusher.Flush // 接口方法表达式：首参为 receiver。
	defer fn(f, "expr", args...)
	args[3] = "expr-mutated"
	f = rebound
	gcStress()
}

func main() {
	uafSeed = seedFromEnv()
	callIdx := 0

	checkCall := func(prefix string, keep int64, wantArgs []any, where string) {
		if callIdx >= len(deferredSums) {
			ck(false, fmt.Sprintf("%s: deferred method did not run seed=%d", where, uafSeed))
			return
		}
		want := expectedSum(prefix, keep, wantArgs)
		if deferredSums[callIdx] != want {
			ck(false, fmt.Sprintf("%s: deferred sum=%d want=%d seed=%d", where, deferredSums[callIdx], want, uafSeed))
		}
		callIdx++
	}

	for r := 0; r < 8; r++ {
		keepOrig := int64(100 + r*10 + int(uafSeed)%7)
		keepRebound := keepOrig + 500
		origPtr := &impl{keep: &item{v: keepOrig, name: "keep"}, tag: "orig"}
		origVal := valImpl{keep: &item{v: keepOrig, name: "keep"}, tag: "orig"}
		rbPtr := &impl{keep: &item{v: keepRebound, name: "keep"}, tag: "orig"}
		rbVal := valImpl{keep: &item{v: keepRebound, name: "keep"}, tag: "orig"}

		before := len(deferredSums)
		var args []any
		if (r+int(uafSeed))%2 == 0 {
			args = []any{
				mkFlushItem(10 + int64(r)),
				box{p: mkFlushItem(20 + int64(r)), tag: "bx"},
				[]*item{mkFlushItem(30 + int64(r)), mkFlushItem(31 + int64(r))},
				"mutated", nil, "tail-mutated",
			}
			useDirect(origPtr, rbPtr, keepOrig, keepRebound, r)
		} else {
			args = []any{
				mkFlushItem(10 + int64(r)),
				box{p: mkFlushItem(20 + int64(r)), tag: "bx"},
				[]*item{mkFlushItem(30 + int64(r)), mkFlushItem(31 + int64(r))},
				"mutated", nil, "tail-mutated",
			}
			useDirect(origVal, rbVal, keepOrig, keepRebound, r)
		}
		if len(deferredSums) != before+1 {
			ck(false, fmt.Sprintf("round %d direct: deferred call count=%d seed=%d", r, len(deferredSums)-before, uafSeed))
		}
		checkCall("direct", keepOrig, args, fmt.Sprintf("round %d direct", r))

		before = len(deferredSums)
		exprArgs := []any{
			mkFlushItem(50 + int64(r)),
			box{p: mkFlushItem(60 + int64(r)), tag: "ex"},
			[]*item{mkFlushItem(70 + int64(r))},
			"expr-mutated", nil,
		}
		if (r+int(uafSeed))%2 == 0 {
			useExpr(origVal, rbVal, keepOrig, r)
		} else {
			useExpr(origPtr, rbPtr, keepOrig, r)
		}
		if len(deferredSums) != before+1 {
			ck(false, fmt.Sprintf("round %d expr: deferred call count=%d seed=%d", r, len(deferredSums)-before, uafSeed))
		}
		checkCall("expr", keepOrig, exprArgs, fmt.Sprintf("round %d expr", r))
	}

	if len(deferredSums) != 16 {
		ck(false, fmt.Sprintf("deferred call total=%d want=16 seed=%d", len(deferredSums), uafSeed))
	}

	finish("l17_iface_defer_variadic")
}
