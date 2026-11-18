// L06: reflect.MakeFunc 回调：谓词/排序/Map/多返回值/变参/有状态闭包，回调存储跨 GC。
package main

import (
	"fmt"
	"math/rand"
	"os"
	"reflect"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync/atomic"
)

type Item struct {
	V   int64
	Tag *string
}

type cbState struct {
	n   int64
	sum int64
}

var (
	failed  int32
	gcSink  [][]byte
	ptrSink []*Item
	fnSink  []any
	uafSeed int64 = 1
)

func ck(ok bool, msg string) {
	if !ok {
		atomic.AddInt32(&failed, 1)
		fmt.Println("FAIL:", msg)
	}
}

func mustPanic(name string, fn func()) {
	defer func() {
		if recover() == nil {
			ck(false, name+" did not panic")
		}
	}()
	fn()
}

func gcStress() {
	for i := 0; i < 16; i++ {
		b := make([]byte, 4096)
		b[0] = byte(i)
		gcSink = append(gcSink[:0], b)
	}
	gcSink = gcSink[:0]
	runtime.GC()
	runtime.GC()
}

func finish(name string) {
	if n := atomic.LoadInt32(&failed); n != 0 {
		fmt.Printf("FAILED %s seed=%d (%d failed checks)\n", name, uafSeed, n)
		os.Exit(1)
	}
	fmt.Println("ALL PASS " + name)
}

func envSeed() int64 {
	s := os.Getenv("UAF_SEED")
	if s == "" {
		return 1
	}
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil || n <= 0 {
		return 1
	}
	return n
}

func envInt(name string, def int) int {
	s := os.Getenv(name)
	if s == "" {
		return def
	}
	n, err := strconv.Atoi(s)
	if err != nil || n <= 0 {
		return def
	}
	return n
}

func filter(xs []*Item, keep func(*Item) bool) []*Item {
	var out []*Item
	for _, x := range xs {
		if keep(x) {
			out = append(out, x)
		}
	}
	return out
}

func main() {
	uafSeed = envSeed()
	rounds := envInt("TORTURE_ROUNDS", 16)
	base := uafSeed%5 + 1

	for r := 0; r < rounds; r++ {
		rnd := rand.New(rand.NewSource(uafSeed + int64(r)*43))
		n := 16 + rnd.Intn(17)
		items := make([]*Item, n)
		even := 0
		var sum int64
		for i := range items {
			t := fmt.Sprintf("t%d-%d", r, i)
			items[i] = &Item{V: int64(i*3+1) + base, Tag: &t}
			sum += items[i].V
			if items[i].V%2 == 0 {
				even++
			}
		}

		// 1) 谓词回调：MakeFunc 作为普通 func 传入高阶函数。
		predT := reflect.TypeOf((func(*Item) bool)(nil))
		predV := reflect.MakeFunc(predT, func(args []reflect.Value) []reflect.Value {
			it, _ := args[0].Interface().(*Item)
			return []reflect.Value{reflect.ValueOf(it != nil && it.V%2 == 0)}
		})
		ck(predV.Type() == predT, "predicate MakeFunc type")
		pred := predV.Interface().(func(*Item) bool)
		kept := filter(items, pred)
		ck(len(kept) == even, "predicate callback filters items")
		ck(!pred(nil), "predicate handles nil argument")
		res := predV.Call([]reflect.Value{reflect.ValueOf(items[0])})
		ck(len(res) == 1 && res[0].Kind() == reflect.Bool, "predicate via reflect.Call")

		// 2) 多返回值回调：值 + error。
		divT := reflect.TypeOf(func(int64) (int64, error) { return 0, nil })
		errT := reflect.TypeOf((*error)(nil)).Elem()
		divV := reflect.MakeFunc(divT, func(args []reflect.Value) []reflect.Value {
			x := args[0].Int()
			if x < 0 {
				return []reflect.Value{reflect.ValueOf(int64(0)), reflect.ValueOf(fmt.Errorf("neg:%d", -x))}
			}
			return []reflect.Value{reflect.ValueOf(x * 2), reflect.Zero(errT)}
		})
		div := divV.Interface().(func(int64) (int64, error))
		q, err := div(base + 1)
		ck(err == nil && q == (base+1)*2, "multi-return callback value")
		q, err = div(-9)
		ck(q == 0 && err != nil && err.Error() == "neg:9", "multi-return callback error")

		// 3) 指针返回回调：返回值只由全局槽保活。
		mkT := reflect.TypeOf(func(*Item) *Item { return nil })
		mkV := reflect.MakeFunc(mkT, func(args []reflect.Value) []reflect.Value {
			it, _ := args[0].Interface().(*Item)
			if it == nil {
				return []reflect.Value{reflect.Zero(mkT.Out(0))}
			}
			return []reflect.Value{reflect.ValueOf(&Item{V: it.V + 1000, Tag: it.Tag})}
		})
		mk := mkV.Interface().(func(*Item) *Item)
		cp := mk(items[0])
		ck(cp != nil && cp.V == items[0].V+1000 && cp.Tag == items[0].Tag, "pointer-return callback")
		ptrSink = append(ptrSink[:0], cp)

		// 4) 排序回调：闭包持有被排序切片，排序结果稳定。
		cpItems := append([]*Item(nil), items...)
		lessT := reflect.TypeOf(func(i, j int) bool { return false })
		lessV := reflect.MakeFunc(lessT, func(args []reflect.Value) []reflect.Value {
			i, j := args[0].Int(), args[1].Int()
			return []reflect.Value{reflect.ValueOf(cpItems[i].V < cpItems[j].V)}
		})
		less := lessV.Interface().(func(i, j int) bool)
		sort.SliceStable(cpItems, less)
		sorted := true
		for i := 1; i < len(cpItems); i++ {
			if cpItems[i-1].V > cpItems[i].V {
				sorted = false
				break
			}
		}
		ck(sorted && len(cpItems) == n, "MakeFunc less callback sorts")

		// 5) strings.Map 回调。
		rotT := reflect.TypeOf(func(r rune) rune { return 0 })
		rotV := reflect.MakeFunc(rotT, func(args []reflect.Value) []reflect.Value {
			return []reflect.Value{reflect.ValueOf(rune(args[0].Int() + 1))}
		})
		mapped := strings.Map(rotV.Interface().(func(rune) rune), "abc")
		ck(mapped == "bcd", "MakeFunc rune callback")

		// 6) 变参回调：直接调用、Call、CallSlice 三条路径。
		varT := reflect.TypeOf(func(string, ...int64) string { return "" })
		varV := reflect.MakeFunc(varT, func(args []reflect.Value) []reflect.Value {
			parts := args[1]
			total := int64(0)
			for i := 0; i < parts.Len(); i++ {
				total += parts.Index(i).Int()
			}
			return []reflect.Value{reflect.ValueOf(fmt.Sprintf("%s=%d", args[0].String(), total))}
		})
		vf := varV.Interface().(func(string, ...int64) string)
		ck(vf("a", 1, 2, 3) == "a=6", "variadic direct call")
		ck(vf("b") == "b=0", "variadic direct call without args")
		ck(varV.Call([]reflect.Value{reflect.ValueOf("c"), reflect.ValueOf(int64(4)), reflect.ValueOf(int64(5))})[0].String() == "c=9", "variadic Call")
		ck(varV.CallSlice([]reflect.Value{reflect.ValueOf("d"), reflect.ValueOf([]int64{6, 7})})[0].String() == "d=13", "variadic CallSlice")

		// 7) 有状态回调：闭包累加跨多次调用。
		state := &cbState{}
		mutT := reflect.TypeOf(func(int64) {})
		mutV := reflect.MakeFunc(mutT, func(args []reflect.Value) []reflect.Value {
			state.n++
			state.sum += args[0].Int()
			return nil
		})
		mut := mutV.Interface().(func(int64))
		for k := 0; k < 10; k++ {
			mut(int64(k) + base)
		}
		ck(state.n == 10 && state.sum == int64(45)+10*base, "stateful callback accumulation")

		// 8) 回调返回函数。
		makerT := reflect.TypeOf(func(int64) func(int64) int64 { return nil })
		makerV := reflect.MakeFunc(makerT, func(args []reflect.Value) []reflect.Value {
			off := args[0].Int()
			return []reflect.Value{reflect.ValueOf(func(x int64) int64 { return x + off })}
		})
		maker := makerV.Interface().(func(int64) func(int64) int64)
		ck(maker(base)(7) == base+7, "callback returning func")

		// 9) 回调内部 panic 原样传播；MakeFunc 返回数量错误必须 panic。
		boomT := reflect.TypeOf(func(int64) int64 { return 0 })
		boomV := reflect.MakeFunc(boomT, func(args []reflect.Value) []reflect.Value {
			panic("boom")
		})
		boom := boomV.Interface().(func(int64) int64)
		func() {
			defer func() {
				r := recover()
				ck(r != nil && fmt.Sprint(r) == "boom", "callback panic propagates")
			}()
			_ = boom(1)
		}()
		badV := reflect.MakeFunc(boomT, func(args []reflect.Value) []reflect.Value { return nil })
		bad := badV.Interface().(func(int64) int64)
		mustPanic("MakeFunc wrong return count", func() { _ = bad(1) })

		// 10) 回调与返回值跨 GC 继续可用。
		fnSink = append(fnSink[:0], pred, div, mk, less, mut, maker, vf)
		gcStress()
		pred2 := fnSink[0].(func(*Item) bool)
		ck(pred2(items[0]) == (items[0].V%2 == 0), "predicate after GC")
		div2 := fnSink[1].(func(int64) (int64, error))
		q2, err2 := div2(base + 2)
		ck(err2 == nil && q2 == (base+2)*2, "multi-return callback after GC")
		mk2 := fnSink[2].(func(*Item) *Item)
		cp2 := mk2(items[0])
		ck(cp2 != nil && cp2.V == items[0].V+1000, "pointer callback after GC")
		less2 := fnSink[3].(func(i, j int) bool)
		ck(less2(0, len(cpItems)-1) == (cpItems[0].V < cpItems[len(cpItems)-1].V), "sort callback after GC")
		mut2 := fnSink[4].(func(int64))
		mut2(5)
		ck(state.n == 11 && state.sum == int64(45)+10*base+5, "stateful callback after GC")
		made2 := fnSink[5].(func(int64) func(int64) int64)
		ck(made2(base)(3) == base+3, "nested callback after GC")
		vf2 := fnSink[6].(func(string, ...int64) string)
		ck(vf2("g", 8, 9) == "g=17", "variadic callback after GC")
		ck(ptrSink[0] == cp && cp.Tag == items[0].Tag && *cp.Tag == fmt.Sprintf("t%d-0", r), "returned pointer after GC")

		gcStress()
		ck(sum > 0 && len(kept) <= n, "round invariants after repeated GC")
	}

	finish("l06_reflect_makefunc_callback")
}
