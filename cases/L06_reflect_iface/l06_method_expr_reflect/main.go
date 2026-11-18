// L06: 反射方法表达式：Type.Method(i) 枚举顺序与接收者签名、Method.Func 方法表达式调用、
// Value.Method 绑定方法、提升方法与导出规则，跨 GC 保持可调用。
package main

import (
	"fmt"
	"os"
	"reflect"
	"runtime"
	"strconv"
	"sync/atomic"
)

type node struct {
	V     int64
	Label string
}

type base struct {
	ID int64
}

func (b base) BaseVal(x int64) int64 { return b.ID*10 + x }
func (b *base) BasePtr(x int64) int64 {
	b.ID += x
	return b.ID * 10
}

type obj struct {
	base
	N int64
}

func (o obj) Val(x int64) int64 { return o.N*10 + x }
func (o *obj) Ptr(x int64) int64 {
	o.N += x
	return o.N * 10
}

// secret 未导出，不出现在 reflect 方法集中。
func (o obj) secret(x int64) int64 { return -x }

type valuer interface {
	Val(int64) int64
}

var (
	failed   int32
	gcSink   [][]byte
	methSink []reflect.Value
	anySink  []any
	uafSeed  int64 = 1
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

func wantResult(name string, r int, x int64) int64 {
	switch name {
	case "Val":
		return int64(100+r)*10 + x
	case "Ptr":
		return (int64(100+r) + x) * 10
	case "BaseVal":
		return int64(r)*10 + x
	case "BasePtr":
		return (int64(r) + x) * 10
	}
	return -1
}

func newObj(r int) *obj {
	return &obj{base: base{ID: int64(r)}, N: int64(100 + r)}
}

func main() {
	uafSeed = envSeed()
	const rounds = 30

	vt := reflect.TypeOf(obj{})
	pt := reflect.TypeOf(&obj{})
	ck(vt.NumMethod() == 2, "value type method count")
	ck(pt.NumMethod() == 4, "pointer type method count")
	ck(vt.Method(0).Name == "BaseVal" && vt.Method(1).Name == "Val", "value method order")
	ck(pt.Method(0).Name == "BasePtr" && pt.Method(1).Name == "BaseVal", "pointer method order prefix")
	ck(pt.Method(2).Name == "Ptr" && pt.Method(3).Name == "Val", "pointer method order suffix")
	for i := 0; i < vt.NumMethod(); i++ {
		m := vt.Method(i)
		ck(m.Index == i, "value method index")
		ck(m.Func.IsValid(), "value method Func valid")
		ck(m.Func.Type().NumIn() == 2, "value method expression arity")
		ck(m.Func.Type().In(0) == vt, "value method expression receiver type")
		ck(m.Type.In(0) == vt, "value method signature receiver")
	}
	for i := 0; i < pt.NumMethod(); i++ {
		m := pt.Method(i)
		ck(m.Index == i, "pointer method index")
		ck(m.Func.IsValid(), "pointer method Func valid")
		ck(m.Func.Type().In(0) == pt, "pointer method expression receiver type")
		ck(m.Type.In(0) == pt, "pointer method signature receiver")
	}
	_, hasPtr := vt.MethodByName("Ptr")
	ck(!hasPtr, "value type has no pointer method")
	_, hasSecret := pt.MethodByName("secret")
	ck(!hasSecret, "unexported method hidden from reflect")
	_, hasBaseVal := vt.MethodByName("BaseVal")
	ck(hasBaseVal, "promoted value method visible")
	_, hasBasePtr := pt.MethodByName("BasePtr")
	ck(hasBasePtr, "promoted pointer method visible")
	_, hasPtrPromoted := vt.MethodByName("Ptr")
	ck(!hasPtrPromoted, "promoted pointer method absent on value")

	it := reflect.TypeOf((*valuer)(nil)).Elem()
	ck(it.NumMethod() == 1 && it.Method(0).Name == "Val", "interface method shape")
	ck(!it.Method(0).Func.IsValid(), "interface Method.Func is zero")
	ck(it.Method(0).Type.NumIn() == 1 && it.Method(0).Type.NumOut() == 1, "interface method signature")
	ck(reflect.TypeOf(&obj{}).Implements(it), "pointer implements valuer")
	ck(reflect.TypeOf(obj{}).Implements(it), "value implements valuer")
	ck(!reflect.TypeOf(node{}).Implements(it), "unrelated type does not implement valuer")

	var nilObj *obj
	mustPanic("method expression with wrong receiver", func() {
		pt.Method(0).Func.Call([]reflect.Value{reflect.ValueOf(obj{}), reflect.ValueOf(int64(1))})
	})
	mustPanic("bound method on nil pointer", func() {
		reflect.ValueOf(nilObj).MethodByName("Ptr").Call([]reflect.Value{reflect.ValueOf(int64(1))})
	})

	for r := 0; r < rounds; r++ {
		const x = int64(3)
		o := newObj(r)

		// Method.Func 是方法表达式，首参为接收者。
		var exprFns []reflect.Value
		for i := 0; i < pt.NumMethod(); i++ {
			m := pt.Method(i)
			exprRecv := newObj(r)
			out := m.Func.Call([]reflect.Value{reflect.ValueOf(exprRecv), reflect.ValueOf(x)})
			ck(len(out) == 1 && out[0].Kind() == reflect.Int64, "method expression single result")
			ck(out[0].Int() == wantResult(m.Name, r, x), "method expression result "+m.Name)

			boundRecv := newObj(r)
			bound := reflect.ValueOf(boundRecv).MethodByName(m.Name)
			ck(bound.IsValid() && bound.Type().NumIn() == 1, "bound method shape "+m.Name)
			bout := bound.Call([]reflect.Value{reflect.ValueOf(x)})
			ck(len(bout) == 1 && bout[0].Int() == wantResult(m.Name, r, x), "bound method result "+m.Name)
			exprFns = append(exprFns, m.Func)
		}

		// 值类型方法：Method.Func 接收者为 obj，Value.Method 为绑定方法。
		for i := 0; i < vt.NumMethod(); i++ {
			m := vt.Method(i)
			fresh := *newObj(r)
			out := m.Func.Call([]reflect.Value{reflect.ValueOf(fresh), reflect.ValueOf(x)})
			ck(out[0].Int() == wantResult(m.Name, r, x), "value method expression result "+m.Name)
			bound := reflect.ValueOf(fresh).Method(i)
			ck(bound.Type().NumIn() == m.Type.NumIn()-1, "value bound method drops receiver")
			ck(bound.Call([]reflect.Value{reflect.ValueOf(x)})[0].Int() == wantResult(m.Name, r, x), "value bound method result "+m.Name)
		}

		// 可寻址值不会因为 Addressable 而获得指针方法；必须显式取 Addr。
		ov := reflect.ValueOf(o).Elem()
		ck(!ov.MethodByName("Ptr").IsValid(), "addressable value keeps value method set")
		ck(ov.Addr().MethodByName("Ptr").IsValid(), "Addr exposes pointer methods")
		ck(!reflect.ValueOf(o).Elem().MethodByName("secret").IsValid(), "unexported bound method hidden")
		ck(reflect.ValueOf(o).Method(0).Type().NumIn() == 1, "pointer Value.Method count")

		// 提升方法：通过外层类型调用，返回外层接收者签名。
		promoted, ok := vt.MethodByName("BaseVal")
		ck(ok, "promoted value method lookup")
		pout := promoted.Func.Call([]reflect.Value{reflect.ValueOf(*newObj(r)), reflect.ValueOf(x)})
		ck(pout[0].Int() == wantResult("BaseVal", r, x), "promoted method expression")
		promotedPtr, ok := pt.MethodByName("BasePtr")
		ck(ok, "promoted pointer method lookup")
		pout = promotedPtr.Func.Call([]reflect.Value{reflect.ValueOf(newObj(r)), reflect.ValueOf(x)})
		ck(pout[0].Int() == wantResult("BasePtr", r, x), "promoted pointer method expression")

		// 动态值通过接口调用，TypeOf 得到具体指针类型。
		var vi valuer = o
		ck(reflect.TypeOf(vi) == pt, "interface dynamic type is concrete pointer")
		ck(reflect.TypeOf(vi).NumMethod() == 4, "dynamic type full method set")
		ck(reflect.TypeOf(vi).Implements(it), "dynamic type implements interface")
		ck(vi.Val(x) == wantResult("Val", r, x), "interface dispatch result")

		// 保存方法表达式与绑定方法，GC 后继续调用。
		methSink = exprFns
		anySink = []any{reflect.ValueOf(o).MethodByName("Val").Interface(), o}
		gcStress()
		for i, m := range methSink {
			name := pt.Method(i).Name
			recv := newObj(r)
			out := m.Call([]reflect.Value{reflect.ValueOf(recv), reflect.ValueOf(x)})
			ck(out[0].Int() == wantResult(name, r, x), "method expression after GC "+name)
		}
		fn := anySink[0].(func(int64) int64)
		ck(fn(x) == wantResult("Val", r, x), "bound method interface after GC")
		ck(anySink[1].(valuer).Val(x) == wantResult("Val", r, x), "interface value after GC")

		// 反射调用控制指针接收者方法的变更可见。
		ptMut, ok := pt.MethodByName("Ptr")
		ck(ok, "pointer method lookup")
		fresh := newObj(r)
		ptMut.Func.Call([]reflect.Value{reflect.ValueOf(fresh), reflect.ValueOf(x)})
		ck(fresh.N == int64(100+r)+x, "method expression mutates receiver")
		ck(vi.Val(x) == wantResult("Val", r, x), "interface dispatch after mutation")
	}
	finish("l06_method_expr_reflect")
}
