// L16: reflect 方法调用：方法集枚举、提升方法、方法表达式、接口动态调用、变参与 GC 后方法值。
package main

import (
	"errors"
	"fmt"
	"math/rand"
	"os"
	"reflect"
	"runtime"
	"strconv"
	"strings"
	"sync/atomic"
)

type counter struct {
	N   int
	log []string
}

func (c *counter) Add(d int) int {
	c.N += d
	c.log = append(c.log, "add:"+strconv.Itoa(d))
	return c.N
}

func (c counter) Value() int { return c.N }

func (c *counter) Variadic(prefix string, vals ...int) string {
	sum := 0
	for _, v := range vals {
		sum += v
	}
	return prefix + ":" + strconv.Itoa(sum)
}

func (c *counter) Try(ok bool) (int, error) {
	if !ok {
		return 0, errors.New("failed")
	}
	return c.N, nil
}

type wrapper struct {
	*counter
	Tag string
}

func (w wrapper) Describe() string { return "w:" + w.Tag }

func (w *wrapper) SetTag(s string) { w.Tag = s }

var (
	failed  int32
	gcSink  [][]byte
	live    []any
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
		_ = &counter{N: i, log: []string{"trash"}}
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

func mustPanic(fn func()) (panicked bool) {
	defer func() {
		if recover() != nil {
			panicked = true
		}
	}()
	fn()
	return false
}

func methodNames(t reflect.Type) []string {
	names := make([]string, 0, t.NumMethod())
	for i := 0; i < t.NumMethod(); i++ {
		names = append(names, t.Method(i).Name)
	}
	return names
}

func main() {
	uafSeed = envSeed()

	ct := reflect.TypeOf(counter{})
	pt := reflect.TypeOf(&counter{})
	wt := reflect.TypeOf(wrapper{})
	wpt := reflect.TypeOf(&wrapper{})

	ck(strings.Join(methodNames(ct), ",") == "Value", fmt.Sprintf("counter methods=%v", methodNames(ct)))
	ck(strings.Join(methodNames(pt), ",") == "Add,Try,Value,Variadic", fmt.Sprintf("*counter methods=%v", methodNames(pt)))
	ck(strings.Join(methodNames(wt), ",") == "Add,Describe,Try,Value,Variadic", fmt.Sprintf("wrapper methods=%v", methodNames(wt)))
	ck(strings.Join(methodNames(wpt), ",") == "Add,Describe,SetTag,Try,Value,Variadic",
		fmt.Sprintf("*wrapper methods=%v", methodNames(wpt)))
	if _, ok := wt.MethodByName("SetTag"); ok {
		ck(false, "value wrapper type exposes pointer-receiver SetTag")
	}
	it := reflect.TypeOf((*interface{ Value() int })(nil)).Elem()
	ck(it.Kind() == reflect.Interface && it.NumMethod() == 1, "interface type method count wrong")

	rounds := 6 + int(uafSeed%4)
	rng := rand.New(rand.NewSource(uafSeed))
	for r := 0; r < rounds; r++ {
		c := &counter{N: 10 + r, log: []string{"init"}}
		w := wrapper{counter: c, Tag: "t0"}
		live = append(live[:0], c, &w)

		// 指针接收者方法：值方法集里通过嵌入指针提升。
		cv := reflect.ValueOf(c).Elem()
		add := cv.Addr().MethodByName("Add")
		ck(add.IsValid(), fmt.Sprintf("round %d Add method value invalid", r))
		got := add.Call([]reflect.Value{reflect.ValueOf(5)})[0].Int()
		ck(got == int64(15+r), fmt.Sprintf("round %d Add returned %d want %d", r, got, 15+r))
		ck(c.N == 15+r, fmt.Sprintf("round %d receiver N=%d want %d", r, c.N, 15+r))

		// 直接对 wrapper 值调用提升的 *counter 方法。
		wv := reflect.ValueOf(w)
		ck(wv.NumMethod() == 5, fmt.Sprintf("round %d wrapper value NumMethod=%d want 5", r, wv.NumMethod()))
		val := wv.MethodByName("Value").Call(nil)[0].Int()
		ck(val == int64(15+r), fmt.Sprintf("round %d promoted Value=%d want %d", r, val, 15+r))
		desc := wv.MethodByName("Describe").Call(nil)[0].String()
		ck(desc == "w:t0", fmt.Sprintf("round %d Describe=%q", r, desc))
		wadd := wv.MethodByName("Add")
		ck(wadd.IsValid(), fmt.Sprintf("round %d wrapper value cannot call promoted Add", r))
		if wadd.IsValid() {
			got = wadd.Call([]reflect.Value{reflect.ValueOf(2)})[0].Int()
			ck(got == int64(17+r), fmt.Sprintf("round %d promoted Add=%d want %d", r, got, 17+r))
		}
		ck(!wv.MethodByName("SetTag").IsValid(), fmt.Sprintf("round %d wrapper value SetTag valid", r))

		// 方法表达式：Type.MethodByName(...).Func 首参为接收者。
		me, meok := wpt.MethodByName("SetTag")
		ck(meok && me.Func.IsValid() && me.Func.Type().NumIn() == 2, fmt.Sprintf("round %d SetTag method expr=%v", r, me.Func.Type()))
		me.Func.Call([]reflect.Value{reflect.ValueOf(&w), reflect.ValueOf("t1")})
		ck(w.Tag == "t1", fmt.Sprintf("round %d SetTag expression left Tag=%q", r, w.Tag))
		ve, veok := wt.MethodByName("Value")
		ck(veok && ve.Func.IsValid(), fmt.Sprintf("round %d promoted Value method expr invalid", r))
		if ve.Func.IsValid() {
			ck(ve.Func.Type().NumIn() == 1, fmt.Sprintf("round %d Value expr type=%v", r, ve.Func.Type()))
			got = ve.Func.Call([]reflect.Value{reflect.ValueOf(w)})[0].Int()
			ck(got == int64(17+r), fmt.Sprintf("round %d Value expr=%d want %d", r, got, 17+r))
		}

		// 接口动态调用。
		var iface interface {
			Add(int) int
			Value() int
		} = c
		iv := reflect.ValueOf(iface)
		// ValueOf 解包接口后看到的是动态类型 *counter 的完整方法集。
		ck(iv.NumMethod() == 4, fmt.Sprintf("round %d dynamic NumMethod=%d want 4", r, iv.NumMethod()))
		itf := reflect.TypeOf(&iface).Elem()
		ck(itf.Kind() == reflect.Interface && itf.NumMethod() == 2,
			fmt.Sprintf("round %d static interface type=%v methods=%d", r, itf, itf.NumMethod()))
		got = iv.MethodByName("Add").Call([]reflect.Value{reflect.ValueOf(3)})[0].Int()
		ck(got == int64(20+r), fmt.Sprintf("round %d interface Add=%d want %d", r, got, 20+r))
		ck(iv.Kind() == reflect.Ptr && iv.Elem().Kind() == reflect.Struct,
			fmt.Sprintf("round %d dynamic value kind=%v elem=%v", r, iv.Kind(), iv.Elem().Kind()))

		// 变参方法与 CallSlice。
		vm := reflect.ValueOf(c).MethodByName("Variadic")
		vs := vm.Call([]reflect.Value{reflect.ValueOf("p"), reflect.ValueOf(1), reflect.ValueOf(2)})[0].String()
		ck(vs == "p:3", fmt.Sprintf("round %d variadic Call=%q", r, vs))
		vss := vm.CallSlice([]reflect.Value{reflect.ValueOf("q"), reflect.ValueOf([]int{3, 4, 5})})[0].String()
		ck(vss == "q:12", fmt.Sprintf("round %d variadic CallSlice=%q", r, vss))

		// 返回 (int, error)。
		tm := reflect.ValueOf(c).MethodByName("Try")
		oks := tm.Call([]reflect.Value{reflect.ValueOf(true)})
		ck(oks[0].Int() == int64(20+r) && oks[1].IsNil(), fmt.Sprintf("round %d Try(true)", r))
		bads := tm.Call([]reflect.Value{reflect.ValueOf(false)})
		ck(!bads[1].IsNil() && bads[1].Interface().(error).Error() == "failed", fmt.Sprintf("round %d Try(false)", r))

		// 非法调用必须 panic，而不是破坏接收者。
		ck(mustPanic(func() {
			add.Call([]reflect.Value{reflect.ValueOf("x")})
		}), fmt.Sprintf("round %d wrong arg type did not panic", r))
		ck(mustPanic(func() {
			add.Call(nil)
		}), fmt.Sprintf("round %d missing arg did not panic", r))
		ck(c.N == 20+r, fmt.Sprintf("round %d receiver corrupted by failed calls: %d", r, c.N))

		// 方法值绑定接收者，GC 后调用仍作用于同一对象。
		bound := reflect.ValueOf(c).MethodByName("Add")
		live = append(live, bound, add)
		gcStress()
		ck(c.N == 20+r, fmt.Sprintf("round %d receiver lost after GC", r))
		got = bound.Call([]reflect.Value{reflect.ValueOf(1)})[0].Int()
		ck(got == int64(21+r), fmt.Sprintf("round %d bound method after GC=%d want %d", r, got, 21+r))
		ck(w.counter == c && w.Tag == "t1", fmt.Sprintf("round %d wrapper lost after GC", r))
		ck(len(c.log) == 5, fmt.Sprintf("round %d log len=%d want 5", r, len(c.log)))
		wantLog := []string{"init", "add:5", "add:2", "add:3", "add:1"}
		for i := range wantLog {
			ck(c.log[i] == wantLog[i], fmt.Sprintf("round %d log[%d]=%q want %q", r, i, c.log[i], wantLog[i]))
		}
		ck(wt.NumMethod() == 5 && wpt.NumMethod() == 6, fmt.Sprintf("round %d Type method count changed after GC", r))

		// 打乱顺序重新推导方法值，确认类型信息稳定。
		idx := rng.Perm(len(methodNames(wpt)))
		names := methodNames(wpt)
		for _, i := range idx {
			ck(wpt.Method(i).Name == names[i], fmt.Sprintf("round %d Method(%d) unstable", r, i))
		}
	}

	finish("l16_reflect_method_call")
}
