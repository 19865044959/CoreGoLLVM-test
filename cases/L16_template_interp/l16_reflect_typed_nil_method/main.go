// L16: reflect 方法调用与 typed-nil 接口：方法集/方法值/方法表达式/绑定接收者/panic，跨 GC。
package main

import (
	"fmt"
	"os"
	"reflect"
	"runtime"
	"strconv"
	"strings"
	"sync/atomic"
)

type Named interface {
	Name() string
}

type Speaker interface {
	Named
	Speak() string
}

type base struct {
	Tag string
	N   int
}

func (b *base) Speak() string {
	if b == nil {
		return "nil-speak"
	}
	return "speak:" + b.Tag
}

func (b base) Name() string { return b.Tag }

func (b *base) Bump(d int) int {
	b.N += d
	return b.N
}

type other struct{ ID int }

func (o *other) Speak() string { return "other" }
func (o *other) Name() string  { return "o" }

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
		_ = &base{Tag: "trash", N: i}
		_ = &other{ID: i}
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

func methodNames(t reflect.Type) []string {
	out := make([]string, 0, t.NumMethod())
	for i := 0; i < t.NumMethod(); i++ {
		out = append(out, t.Method(i).Name)
	}
	return out
}

func mustPanic(fn func()) (panicked bool, msg string) {
	defer func() {
		if r := recover(); r != nil {
			panicked = true
			msg = fmt.Sprint(r)
		}
	}()
	fn()
	return false, ""
}

func main() {
	uafSeed = envSeed()
	rounds := 5 + int(uafSeed%4)

	speakerType := reflect.TypeOf((*Speaker)(nil)).Elem()
	ptrBase := reflect.TypeOf(&base{})
	valBase := reflect.TypeOf(base{})
	ck(speakerType.Kind() == reflect.Interface && speakerType.NumMethod() == 2,
		fmt.Sprintf("Speaker type=%v methods=%d", speakerType, speakerType.NumMethod()))
	ck(strings.Join(methodNames(speakerType), ",") == "Name,Speak",
		fmt.Sprintf("Speaker methods=%v", methodNames(speakerType)))
	for i := 0; i < speakerType.NumMethod(); i++ {
		m := speakerType.Method(i)
		ck(!m.Func.IsValid(), fmt.Sprintf("interface method %s has valid Func", m.Name))
		ck(m.Type.String() == "func() string", fmt.Sprintf("interface method %s type=%v", m.Name, m.Type))
	}
	ck(strings.Join(methodNames(ptrBase), ",") == "Bump,Name,Speak",
		fmt.Sprintf("*base methods=%v", methodNames(ptrBase)))
	ck(strings.Join(methodNames(valBase), ",") == "Name",
		fmt.Sprintf("base methods=%v", methodNames(valBase)))

	speakExpr, speakOK := ptrBase.MethodByName("Speak")
	ck(speakOK && speakExpr.Func.IsValid() && speakExpr.Func.Type().NumIn() == 1,
		fmt.Sprintf("Speak method expr=%v", speakExpr.Func.Type()))
	if speakExpr.Func.IsValid() {
		got := speakExpr.Func.Call([]reflect.Value{reflect.ValueOf((*base)(nil))})[0].String()
		ck(got == "nil-speak", fmt.Sprintf("Speak method expr nil receiver=%q", got))
	}
	nameExpr, nameOK := valBase.MethodByName("Name")
	ck(nameOK && nameExpr.Func.IsValid() && nameExpr.Func.Type().NumIn() == 1,
		fmt.Sprintf("Name method expr=%v", nameExpr.Func.Type()))
	if nameExpr.Func.IsValid() {
		got := nameExpr.Func.Call([]reflect.Value{reflect.ValueOf(base{Tag: "z"})})[0].String()
		ck(got == "z", fmt.Sprintf("Name method expr=%q", got))
	}

	var firstNilNamePanic, firstWrongArgsPanic string
	for r := 0; r < rounds; r++ {
		var s Speaker = (*base)(nil)
		v := reflect.ValueOf(s)
		ck(v.Kind() == reflect.Ptr && v.IsNil(), fmt.Sprintf("round %d typed nil value kind=%v nil=%v", r, v.Kind(), v.IsNil()))
		ck(v.NumMethod() == 3, fmt.Sprintf("round %d typed nil NumMethod=%d", r, v.NumMethod()))
		ck(reflect.TypeOf(s) == ptrBase, fmt.Sprintf("round %d TypeOf(typed nil)=%v", r, reflect.TypeOf(s)))
		got := v.MethodByName("Speak").Call(nil)[0].String()
		ck(got == "nil-speak", fmt.Sprintf("round %d nil receiver Speak=%q", r, got))
		ck(!v.MethodByName("Nope").IsValid(), fmt.Sprintf("round %d unknown method reported valid", r))

		iv := reflect.ValueOf(&s).Elem()
		ck(iv.Kind() == reflect.Interface && !iv.IsNil() && iv.NumMethod() == 2,
			fmt.Sprintf("round %d interface value kind=%v nil=%v methods=%d", r, iv.Kind(), iv.IsNil(), iv.NumMethod()))
		ck(iv.Elem().Kind() == reflect.Ptr && iv.Elem().IsNil(),
			fmt.Sprintf("round %d interface dynamic elem=%v nil=%v", r, iv.Elem().Kind(), iv.Elem().IsNil()))
		got = iv.MethodByName("Speak").Call(nil)[0].String()
		ck(got == "nil-speak", fmt.Sprintf("round %d interface nil Speak=%q", r, got))

		panicked, msg := mustPanic(func() {
			v.MethodByName("Name").Call(nil)
		})
		ck(panicked, fmt.Sprintf("round %d nil value-receiver Name did not panic", r))
		ck(strings.Contains(msg, "nil *base pointer"), fmt.Sprintf("round %d nil receiver panic=%q", r, msg))
		if r == 0 {
			firstNilNamePanic = msg
		} else {
			ck(msg == firstNilNamePanic, fmt.Sprintf("round %d nil receiver panic changed: %q", r, msg))
		}
		panicked, msg = mustPanic(func() {
			iv.MethodByName("Speak").Call([]reflect.Value{reflect.ValueOf(1)})
		})
		ck(panicked, fmt.Sprintf("round %d wrong args did not panic", r))
		ck(strings.Contains(msg, "too many input arguments"), fmt.Sprintf("round %d wrong args panic=%q", r, msg))
		if r == 0 {
			firstWrongArgsPanic = msg
		} else {
			ck(msg == firstWrongArgsPanic, fmt.Sprintf("round %d wrong args panic changed: %q", r, msg))
		}

		// 绑定方法值捕获接收者；接口换动态值后旧方法值仍指向原对象。
		boundNil := v.MethodByName("Speak")
		s = &other{ID: 1}
		ck(s.Speak() == "other", fmt.Sprintf("round %d reassigned interface Speak=%q", r, s.Speak()))
		ck(reflect.TypeOf(s).String() == "*main.other", fmt.Sprintf("round %d reassigned interface type=%v", r, reflect.TypeOf(s)))
		got = boundNil.Call(nil)[0].String()
		ck(got == "nil-speak", fmt.Sprintf("round %d bound nil method after reassign=%q", r, got))
		iv2 := reflect.ValueOf(&s).Elem()
		ck(iv2.NumMethod() == 2, fmt.Sprintf("round %d interface after reassign NumMethod=%d", r, iv2.NumMethod()))
		got = iv2.MethodByName("Speak").Call(nil)[0].String()
		ck(got == "other", fmt.Sprintf("round %d interface after reassign Speak=%q", r, got))
		got = iv2.MethodByName("Name").Call(nil)[0].String()
		ck(got == "o", fmt.Sprintf("round %d interface Name=%q", r, got))
		ck(iv2.Elem().Type().String() == "*main.other", fmt.Sprintf("round %d interface dynamic type=%v", r, iv2.Elem().Type()))

		b := &base{Tag: "b", N: 10 + r}
		boundBump := reflect.ValueOf(b).MethodByName("Bump")
		ck(boundBump.IsValid(), fmt.Sprintf("round %d Bump method value invalid", r))
		if boundBump.IsValid() {
			got := boundBump.Call([]reflect.Value{reflect.ValueOf(5)})[0].Int()
			ck(got == int64(15+r), fmt.Sprintf("round %d bound Bump=%d want %d", r, got, 15+r))
		}
		bumpExpr, bumpOK := ptrBase.MethodByName("Bump")
		if bumpOK && bumpExpr.Func.IsValid() {
			res := bumpExpr.Func.Call([]reflect.Value{reflect.ValueOf(b), reflect.ValueOf(3)})
			ck(res[0].Int() == int64(18+r), fmt.Sprintf("round %d Bump expr=%d", r, res[0].Int()))
		}
		ck(b.N == 18+r, fmt.Sprintf("round %d Bump receiver N=%d want %d", r, b.N, 18+r))

		var zero reflect.Value
		ck(!zero.IsValid(), fmt.Sprintf("round %d zero Value reported valid", r))
		panicked, msg = mustPanic(func() { zero.MethodByName("X") })
		ck(panicked && strings.Contains(msg, "zero Value"), fmt.Sprintf("round %d zero Value MethodByName panic=%q", r, msg))
		panicked, msg = mustPanic(func() { zero.Call(nil) })
		ck(panicked && strings.Contains(msg, "zero Value"), fmt.Sprintf("round %d zero Value Call panic=%q", r, msg))

		live = []any{s, &s, boundNil, iv2, b, boundBump}
		gcStress()

		ck(s.Speak() == "other", fmt.Sprintf("round %d interface after GC Speak=%q", r, s.Speak()))
		got = boundNil.Call(nil)[0].String()
		ck(got == "nil-speak", fmt.Sprintf("round %d bound nil method after GC=%q", r, got))
		iv3 := reflect.ValueOf(&s).Elem()
		ck(iv3.NumMethod() == 2 && iv3.Elem().Type().String() == "*main.other",
			fmt.Sprintf("round %d interface type after GC=%v", r, iv3.Elem().Type()))
		gotInt := boundBump.Call([]reflect.Value{reflect.ValueOf(2)})[0].Int()
		ck(gotInt == int64(20+r) && b.N == 20+r, fmt.Sprintf("round %d bound Bump after GC=%d b.N=%d", r, gotInt, b.N))
		var ns Speaker = (*base)(nil)
		got = reflect.ValueOf(ns).MethodByName("Speak").Call(nil)[0].String()
		ck(got == "nil-speak", fmt.Sprintf("round %d fresh typed nil after GC=%q", r, got))
		panicked, msg = mustPanic(func() {
			reflect.ValueOf(ns).MethodByName("Name").Call(nil)
		})
		ck(panicked && strings.Contains(msg, "nil *base pointer"), fmt.Sprintf("round %d nil Name panic after GC=%q", r, msg))
		panicked, msg = mustPanic(func() {
			reflect.ValueOf(&s).Elem().MethodByName("Speak").Call([]reflect.Value{reflect.ValueOf(1)})
		})
		ck(panicked && strings.Contains(msg, "too many input arguments"),
			fmt.Sprintf("round %d wrong args panic after GC=%q", r, msg))
		ck(strings.Join(methodNames(ptrBase), ",") == "Bump,Name,Speak",
			fmt.Sprintf("round %d *base methods changed after GC", r))
		ck(strings.Join(methodNames(speakerType), ",") == "Name,Speak",
			fmt.Sprintf("round %d Speaker methods changed after GC", r))
	}

	finish("l16_reflect_typed_nil_method")
}
