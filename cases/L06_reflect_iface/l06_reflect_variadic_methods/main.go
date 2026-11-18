// L06: reflect 可变参数方法：MethodByName/Call/CallSlice、方法表达式、提升方法与接口方法集，跨 GC。
package main

import (
	"fmt"
	"math/rand"
	"os"
	"reflect"
	"runtime"
	"strconv"
	"strings"
	"sync/atomic"
)

type Item struct {
	V   int64
	Tag *string
}

type Calc struct {
	Base int64
	Tag  *string
}

func (c *Calc) Sum(prefix int64, xs ...*Item) int64 {
	s := prefix + c.Base
	for _, x := range xs {
		if x != nil {
			s += x.V
		}
	}
	return s
}

func (c *Calc) Add(delta int64, xs ...int64) int64 {
	c.Base += delta
	for _, x := range xs {
		c.Base += x
	}
	return c.Base
}

func (c Calc) Join(sep string, parts ...string) string {
	return strings.Join(parts, sep) + ":" + strconv.FormatInt(c.Base, 10)
}

type Embed struct{ *Calc }

type summer interface {
	Sum(int64, ...*Item) int64
	Add(int64, ...int64) int64
}

type joiner interface {
	Join(string, ...string) string
}

var (
	failed      int32
	gcSink      [][]byte
	reflectSink []reflect.Value
	uafSeed     int64 = 1
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

func itemVals(items []*Item) []reflect.Value {
	out := make([]reflect.Value, len(items))
	for i, it := range items {
		out[i] = reflect.ValueOf(it)
	}
	return out
}

func main() {
	uafSeed = envSeed()
	itemPtr := reflect.TypeOf((*Item)(nil))
	sliceT := reflect.SliceOf(itemPtr)
	summerT := reflect.TypeOf((*summer)(nil)).Elem()
	joinerT := reflect.TypeOf((*joiner)(nil)).Elem()
	calcT := reflect.TypeOf(&Calc{})
	calcValT := reflect.TypeOf(Calc{})

	// 1) 方法元数据：Type.Method 含 receiver，Value.Method 不含。
	m, ok := calcT.MethodByName("Sum")
	ck(ok && m.Type.IsVariadic(), "Type.Method Sum is variadic")
	ck(m.Type.NumIn() == 3 && m.Type.In(0) == calcT, "Type.Method includes receiver")
	ck(m.Type.In(1).Kind() == reflect.Int64 && m.Type.In(2) == sliceT, "variadic parameter shape")
	ck(m.Type.NumOut() == 1 && m.Type.Out(0).Kind() == reflect.Int64, "variadic method result shape")

	// 2) 方法集与接口实现关系。
	ck(calcT.Implements(summerT), "*Calc implements summer")
	ck(!calcValT.Implements(summerT), "Calc value does not implement summer")
	ck(calcT.Implements(joinerT) && calcValT.Implements(joinerT), "value receiver Join implemented by both")
	ck(reflect.TypeOf(&Embed{}).Implements(summerT), "*Embed implements summer via embedding")
	ck(reflect.TypeOf(Embed{}).Implements(summerT), "Embed value implements summer via embedded *Calc")

	rounds := 24
	for r := 0; r < rounds; r++ {
		rnd := rand.New(rand.NewSource(uafSeed + int64(r)*131))
		base := uafSeed%5 + 1 + int64(r)
		tagText := fmt.Sprintf("calc-%d", r)
		c := &Calc{Base: base, Tag: &tagText}
		n := rnd.Intn(6)
		items := make([]*Item, n)
		var sumItems int64
		for i := range items {
			t := fmt.Sprintf("i%d-%d", r, i)
			items[i] = &Item{V: base + int64(i*3) + 1, Tag: &t}
			sumItems += items[i].V
		}

		vm := reflect.ValueOf(c).MethodByName("Sum")
		ck(vm.IsValid() && vm.Type().IsVariadic(), "Value.Method Sum valid")
		ck(vm.Type().NumIn() == 2 && vm.Type().In(1) == sliceT, "Value.Method excludes receiver")
		ck(vm.Type().In(0).Kind() == reflect.Int64, "first Value.Method parameter is prefix")

		// 3) Call 按单个变参传入。
		args := append([]reflect.Value{reflect.ValueOf(base + 10)}, itemVals(items)...)
		res := vm.Call(args)
		ck(len(res) == 1 && res[0].Int() == 2*base+10+sumItems, "MethodByName Call variadic")

		// 4) CallSlice 按切片传入；nil 变参切片等价于零个参数。
		sv := reflect.MakeSlice(sliceT, n, n)
		for i, it := range items {
			sv.Index(i).Set(reflect.ValueOf(it))
		}
		res = vm.CallSlice([]reflect.Value{reflect.ValueOf(base + 20), sv})
		ck(res[0].Int() == 2*base+20+sumItems, "CallSlice variadic")
		res = vm.Call([]reflect.Value{reflect.ValueOf(base + 30)})
		ck(res[0].Int() == 2*base+30, "Call with zero variadic args")
		res = vm.CallSlice([]reflect.Value{reflect.ValueOf(base + 40), reflect.Zero(sliceT)})
		ck(res[0].Int() == 2*base+40, "CallSlice with nil variadic slice")

		// 5) 方法表达式：首参为 receiver。
		recvArgs := append([]reflect.Value{reflect.ValueOf(c), reflect.ValueOf(base + 50)}, itemVals(items)...)
		res = m.Func.Call(recvArgs)
		ck(res[0].Int() == 2*base+50+sumItems, "method expression Call")
		res = m.Func.CallSlice([]reflect.Value{reflect.ValueOf(c), reflect.ValueOf(base + 60), sv})
		ck(res[0].Int() == 2*base+60+sumItems, "method expression CallSlice")
		ck(m.Func.Type().NumIn() == 3 && m.Func.Type().In(0) == calcT, "method expression receiver type")

		// 6) 嵌入提升的可变参数方法。
		e := &Embed{Calc: c}
		em, ok := reflect.TypeOf(e).MethodByName("Sum")
		ck(ok && em.Type.In(0) == reflect.TypeOf(e) && em.Type.IsVariadic(), "promoted variadic method type")
		evm := reflect.ValueOf(e).MethodByName("Sum")
		res = evm.Call([]reflect.Value{reflect.ValueOf(base + 70)})
		ck(res[0].Int() == 2*base+70, "promoted variadic method call")

		// 7) 接口动态派发：Sum 与 Add 均按变参调用。
		var sm summer = c
		ck(sm.Sum(base+80) == 2*base+80, "interface variadic Sum")
		before := c.Base
		got := sm.Add(5, 7, 9)
		ck(got == before+21 && c.Base == before+21, "interface variadic Add mutates receiver")
		var jv joiner = Calc{Base: base}
		ck(jv.Join("-", "a", "b") == "a-b:"+strconv.FormatInt(base, 10), "value receiver variadic Join")

		// 8) 参数错误路径。
		dummy := &Item{V: base + 1}
		mustPanic("Call with too few args", func() { vm.Call(nil) })
		mustPanic("Call with slice as variadic element", func() {
			vm.Call([]reflect.Value{reflect.ValueOf(int64(1)), reflect.ValueOf(items)})
		})
		mustPanic("CallSlice with non-slice", func() {
			vm.CallSlice([]reflect.Value{reflect.ValueOf(int64(1)), reflect.ValueOf(dummy)})
		})
		mustPanic("CallSlice missing variadic arg", func() { vm.CallSlice([]reflect.Value{reflect.ValueOf(int64(1))}) })

		// 9) 方法值与变参切片跨 GC：方法值持有 receiver，reflect.Value 切片持有元素指针。
		reflectSink = append(reflectSink[:0], vm, sv)
		c.Tag = &tagText
		gcStress()
		res = reflectSink[0].CallSlice([]reflect.Value{reflect.ValueOf(int64(base + 90)), reflectSink[1]})
		ck(res[0].Int() == c.Base+base+90+sumItems, "method value and variadic slice after GC")
		ck(c.Tag != nil && *c.Tag == tagText, "receiver Tag after GC")
		if n > 0 {
			ck(items[0] != nil && items[0].Tag != nil && *items[0].Tag == fmt.Sprintf("i%d-0", r), "variadic item pointer after GC")
		}
		if r%8 == 0 {
			gcStress()
		}
	}

	finish("l06_reflect_variadic_methods")
}
