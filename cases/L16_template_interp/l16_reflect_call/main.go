// L16: reflect.Value.Call 动态调用含指针参数的函数（含变参/MakeFunc/方法值）；GC 后指针仍有效。
package main

import (
	"fmt"
	"math/rand"
	"os"
	"reflect"
	"runtime"
	"strconv"
	"sync/atomic"
)

type node struct {
	Tag  string
	Val  int64
	Next *node
}

func (n *node) Combine(o *node) int64 {
	return n.Val + o.Val
}

func mutate(n *node, delta int64) *node {
	n.Val += delta
	return &node{Tag: n.Tag + "+", Val: n.Val * 2, Next: n}
}

func replace(pp **node, tag string) {
	*pp = &node{Tag: tag, Val: 100}
}

func joinAll(prefix string, ns ...*node) string {
	total := int64(0)
	tags := prefix
	for _, n := range ns {
		total += n.Val
		tags += "|" + n.Tag
	}
	return fmt.Sprintf("%s:%d", tags, total)
}

func bumpVal(n node) node {
	n.Val++
	return n
}

func maybeErr(ok bool) (*node, error) {
	if ok {
		return &node{Tag: "ok", Val: 7}, nil
	}
	return nil, fmt.Errorf("no node")
}

func mustPanic(fn func()) (panicked bool) {
	defer func() {
		if recover() != nil {
			panicked = true
		}
	}()
	fn()
	return false
}

var (
	failed  int32
	gcSink  [][]byte
	live    []*node
	uafSeed int64 = 1
)

func ck(ok bool, msg string) {
	if !ok {
		atomic.AddInt32(&failed, 1)
		fmt.Println("FAIL:", msg)
	}
}

func gcStress() {
	for i := 0; i < 16; i++ {
		b := make([]byte, 4096)
		b[0] = byte(i)
		gcSink = append(gcSink[:0], b)
	}
	for i := 0; i < 64; i++ {
		_ = &node{Tag: "trash", Val: int64(i), Next: &node{Tag: "n"}}
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

func main() {
	uafSeed = envSeed()
	mutateFn := reflect.ValueOf(mutate)
	bumpFn := reflect.ValueOf(bumpVal)
	replaceFn := reflect.ValueOf(replace)
	joinFn := reflect.ValueOf(joinAll)
	maybeFn := reflect.ValueOf(maybeErr)

	wrapperType := reflect.TypeOf(func(*node) *node { return nil })
	wrapper := reflect.MakeFunc(wrapperType, func(args []reflect.Value) []reflect.Value {
		res := mutateFn.Call([]reflect.Value{args[0], reflect.ValueOf(int64(3))})
		return []reflect.Value{res[0]}
	})

	rounds := 8 + int(uafSeed%5)
	for r := 0; r < rounds; r++ {
		base := &node{Tag: "base", Val: int64(10 + r)}
		live = append(live[:0], base)

		mres := mutateFn.Call([]reflect.Value{reflect.ValueOf(base), reflect.ValueOf(int64(5))})
		mutant, ok := mres[0].Interface().(*node)
		ck(ok && mutant != nil, fmt.Sprintf("round %d mutate result is not *node", r))
		ck(base.Val == int64(15+r), fmt.Sprintf("round %d base.Val=%d want %d", r, base.Val, 15+r))
		ck(mutant != nil && mutant.Next == base && mutant.Tag == "base+" && mutant.Val == base.Val*2,
			fmt.Sprintf("round %d mutate returned pointer fields wrong", r))

		combine := reflect.ValueOf(base).MethodByName("Combine")
		ck(combine.IsValid(), fmt.Sprintf("round %d method Combine not found", r))
		got := combine.Call([]reflect.Value{reflect.ValueOf(mutant)})[0].Int()
		ck(got == base.Val+mutant.Val, fmt.Sprintf("round %d method call=%d want %d", r, got, base.Val+mutant.Val))

		var p *node = base
		replaceFn.Call([]reflect.Value{reflect.ValueOf(&p), reflect.ValueOf("repl")})
		ck(p.Tag == "repl" && p.Val == 100, fmt.Sprintf("round %d double pointer replace: %+v", r, p))
		ck(base.Tag == "base", fmt.Sprintf("round %d double pointer replace clobbered base", r))

		pair := []*node{base, mutant}
		rng := rand.New(rand.NewSource(uafSeed*100 + int64(r)))
		perm := rng.Perm(len(pair))
		ns := []*node{pair[perm[0]], pair[perm[1]]}
		wantJoin := "v|" + ns[0].Tag + "|" + ns[1].Tag + ":" + strconv.FormatInt(ns[0].Val+ns[1].Val, 10)
		jres := joinFn.Call([]reflect.Value{reflect.ValueOf("v"), reflect.ValueOf(ns[0]), reflect.ValueOf(ns[1])})
		ck(jres[0].String() == wantJoin, fmt.Sprintf("round %d variadic Call=%q want %q", r, jres[0].String(), wantJoin))
		jsres := joinFn.CallSlice([]reflect.Value{reflect.ValueOf("v"), reflect.ValueOf(ns)})
		ck(jsres[0].String() == wantJoin, fmt.Sprintf("round %d variadic CallSlice=%q want %q", r, jsres[0].String(), wantJoin))

		valBefore := base.Val
		cres := bumpFn.Call([]reflect.Value{reflect.ValueOf(*base)})
		copied := cres[0].Interface().(node)
		ck(copied.Val == valBefore+1, fmt.Sprintf("round %d value-copy result=%d want %d", r, copied.Val, valBefore+1))
		ck(base.Val == valBefore, fmt.Sprintf("round %d value-copy mutated original: %d", r, base.Val))

		okRes := maybeFn.Call([]reflect.Value{reflect.ValueOf(true)})
		ck(!okRes[0].IsNil() && okRes[0].Interface().(*node).Val == 7, fmt.Sprintf("round %d maybeErr(true) result", r))
		ck(okRes[1].IsNil(), fmt.Sprintf("round %d maybeErr(true) error not nil", r))
		badRes := maybeFn.Call([]reflect.Value{reflect.ValueOf(false)})
		ck(badRes[0].IsNil(), fmt.Sprintf("round %d maybeErr(false) node not nil", r))
		ck(!badRes[1].IsNil() && badRes[1].Interface().(error).Error() == "no node",
			fmt.Sprintf("round %d maybeErr(false) error wrong", r))

		ck(mustPanic(func() {
			mutateFn.Call([]reflect.Value{reflect.ValueOf(base)})
		}), fmt.Sprintf("round %d too few args did not panic", r))
		ck(mustPanic(func() {
			mutateFn.Call([]reflect.Value{reflect.ValueOf(base), reflect.ValueOf("x")})
		}), fmt.Sprintf("round %d wrong arg type did not panic", r))
		ck(mustPanic(func() {
			mutateFn.Call([]reflect.Value{reflect.ValueOf(node{}), reflect.ValueOf(int64(1))})
		}), fmt.Sprintf("round %d value receiver did not panic", r))

		// MakeFunc 包装：内部再次通过 reflect.Value.Call 调 mutate。
		wres := wrapper.Call([]reflect.Value{reflect.ValueOf(mutant)})
		wrapped, ok := wres[0].Interface().(*node)
		ck(ok && wrapped != nil, fmt.Sprintf("round %d MakeFunc result type", r))
		ck(wrapped.Next == mutant && wrapped.Val == mutant.Val*2 && mutant.Val == base.Val*2+3,
			fmt.Sprintf("round %d MakeFunc chain wrong", r))
		live = append(live, mutant, p, wrapped)

		gcStress()

		ck(base.Val == int64(15+r) && base.Tag == "base", fmt.Sprintf("round %d base corrupted after GC", r))
		ck(mutant.Next == base && mutant.Val == base.Val*2+3, fmt.Sprintf("round %d mutant chain after GC", r))
		ck(wrapped.Next == mutant && wrapped.Val == mutant.Val*2, fmt.Sprintf("round %d wrapped chain after GC", r))
		ck(p.Tag == "repl" && p.Val == 100, fmt.Sprintf("round %d replaced node after GC", r))
		got2 := combine.Call([]reflect.Value{reflect.ValueOf(mutant)})[0].Int()
		ck(got2 == base.Val+mutant.Val, fmt.Sprintf("round %d method value after GC=%d want %d", r, got2, base.Val+mutant.Val))
		mres2 := mutateFn.Call([]reflect.Value{reflect.ValueOf(base), reflect.ValueOf(int64(1))})
		mutant2 := mres2[0].Interface().(*node)
		ck(mutant2.Next == base && base.Val == int64(16+r), fmt.Sprintf("round %d reflect call after GC", r))
		live = append(live, mutant2)
	}

	finish("l16_reflect_call")
}
