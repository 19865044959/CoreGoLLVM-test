// L06: 接口解释器：Expr/Op 接口派发、类型 switch 解释路径、reflect.Value.Call 宿主调用、
// 接口装箱/断言与函数值闭包，跨 GC 三种求值路径结果一致。
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

type Env struct {
	Vars map[string]int64
	Ops  map[string]Op
	Tag  *node
}

type Expr interface {
	Eval(*Env) (int64, error)
}

type Op interface {
	Apply(int64) int64
	Name() string
}

type literal int64

func (l literal) Eval(*Env) (int64, error) { return int64(l), nil }

type varRef string

func (v varRef) Eval(env *Env) (int64, error) {
	if env == nil {
		return 0, fmt.Errorf("nil env")
	}
	x, ok := env.Vars[string(v)]
	if !ok {
		return 0, fmt.Errorf("undefined var %s", string(v))
	}
	return x, nil
}

type addExpr struct {
	L, R Expr
}

func (e addExpr) Eval(env *Env) (int64, error) {
	l, err := e.L.Eval(env)
	if err != nil {
		return 0, err
	}
	r, err := e.R.Eval(env)
	if err != nil {
		return 0, err
	}
	return l + r, nil
}

type mulExpr struct {
	L, R Expr
}

func (e mulExpr) Eval(env *Env) (int64, error) {
	l, err := e.L.Eval(env)
	if err != nil {
		return 0, err
	}
	r, err := e.R.Eval(env)
	if err != nil {
		return 0, err
	}
	return l * r, nil
}

type negExpr struct {
	X Expr
}

func (e negExpr) Eval(env *Env) (int64, error) {
	x, err := e.X.Eval(env)
	if err != nil {
		return 0, err
	}
	return -x, nil
}

type callExpr struct {
	Name string
	Arg  Expr
}

func (e callExpr) Eval(env *Env) (int64, error) {
	arg, err := e.Arg.Eval(env)
	if err != nil {
		return 0, err
	}
	op, ok := env.Ops[e.Name]
	if !ok {
		return 0, fmt.Errorf("unknown op %s", e.Name)
	}
	return op.Apply(arg), nil
}

type errExpr struct{}

func (errExpr) Eval(*Env) (int64, error) { return 0, fmt.Errorf("err expr") }

type offsetOp struct {
	delta int64
	label string
}

func (o offsetOp) Apply(x int64) int64 { return x + o.delta }
func (o offsetOp) Name() string        { return o.label }

type scaleOp struct {
	factor int64
	label  string
}

func (o scaleOp) Apply(x int64) int64 { return x * o.factor }
func (o scaleOp) Name() string        { return o.label }

type funcOp struct {
	id  string
	fn  func(int64) int64
	tag *node
}

func (o funcOp) Apply(x int64) int64 { return o.fn(x) }
func (o funcOp) Name() string        { return o.id }

// evalSwitch 不依赖 Expr.Eval 的动态派发，完全用具体类型 switch 解释；
// 宿主 Op 调用走 reflect.Value.Call，模拟解释器回调宿主的路径。
func evalSwitch(e Expr, env *Env) (int64, error) {
	switch n := e.(type) {
	case literal:
		return int64(n), nil
	case varRef:
		return n.Eval(env)
	case addExpr:
		l, err := evalSwitch(n.L, env)
		if err != nil {
			return 0, err
		}
		r, err := evalSwitch(n.R, env)
		if err != nil {
			return 0, err
		}
		return l + r, nil
	case mulExpr:
		l, err := evalSwitch(n.L, env)
		if err != nil {
			return 0, err
		}
		r, err := evalSwitch(n.R, env)
		if err != nil {
			return 0, err
		}
		return l * r, nil
	case negExpr:
		x, err := evalSwitch(n.X, env)
		if err != nil {
			return 0, err
		}
		return -x, nil
	case callExpr:
		arg, err := evalSwitch(n.Arg, env)
		if err != nil {
			return 0, err
		}
		op, ok := env.Ops[n.Name]
		if !ok {
			return 0, fmt.Errorf("unknown op %s", n.Name)
		}
		out := reflect.ValueOf(op).MethodByName("Apply").Call([]reflect.Value{reflect.ValueOf(arg)})
		if len(out) != 1 {
			return 0, fmt.Errorf("apply arity")
		}
		return out[0].Int(), nil
	default:
		return 0, fmt.Errorf("unknown expr")
	}
}

func evalReflect(e Expr, env *Env) (int64, error) {
	out := reflect.ValueOf(e).MethodByName("Eval").Call([]reflect.Value{reflect.ValueOf(env)})
	if len(out) != 2 {
		return 0, fmt.Errorf("eval arity")
	}
	if !out[1].IsNil() {
		return 0, out[1].Interface().(error)
	}
	return out[0].Int(), nil
}

func box(x int64) any { return x }

func unbox(v any) (int64, bool) {
	x, ok := v.(int64)
	return x, ok
}

func unboxReflect(v any) (int64, bool) {
	rv := reflect.ValueOf(v)
	if !rv.IsValid() || rv.Kind() != reflect.Int64 {
		return 0, false
	}
	return rv.Int(), true
}

var (
	failed    int32
	gcSink    [][]byte
	envSink   *Env
	exprSink  Expr
	ifaceSink []any
	uafSeed   int64 = 1
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

func envInt(name string, def, max int) int {
	s := os.Getenv(name)
	if s == "" {
		return def
	}
	n, err := strconv.Atoi(s)
	if err != nil || n <= 0 {
		return def
	}
	if n > max {
		n = max
	}
	return n
}

func main() {
	uafSeed = envSeed()
	rounds := envInt("TORTURE_ROUNDS", 40, 2000)

	exprT := reflect.TypeOf((*Expr)(nil)).Elem()
	opT := reflect.TypeOf((*Op)(nil)).Elem()
	ck(exprT.NumMethod() == 1 && exprT.Method(0).Name == "Eval", "Expr interface shape")
	ck(opT.NumMethod() == 2 && opT.Method(0).Name == "Apply" && opT.Method(1).Name == "Name", "Op interface shape")
	ck(reflect.TypeOf(addExpr{}).Implements(exprT), "addExpr implements Expr")
	ck(reflect.TypeOf((*addExpr)(nil)).Implements(exprT), "pointer addExpr implements Expr")
	ck(reflect.TypeOf(funcOp{}).Implements(opT), "funcOp implements Op")
	_, isStringer := any(addExpr{}).(fmt.Stringer)
	ck(!isStringer, "addExpr does not implement Stringer")
	_, isExpr := any(addExpr{}).(Expr)
	ck(isExpr, "addExpr boxes as Expr")

	// 独立错误路径。
	badEnv := &Env{Vars: map[string]int64{}, Ops: map[string]Op{}}
	bad := varRef("missing")
	if _, err := bad.Eval(badEnv); err == nil {
		ck(false, "undefined var direct error")
	}
	if _, err := evalSwitch(bad, badEnv); err == nil {
		ck(false, "undefined var switch error")
	}
	if _, err := evalReflect(bad, badEnv); err == nil {
		ck(false, "undefined var reflect error")
	}
	if _, err := (callExpr{Name: "nope", Arg: literal(1)}).Eval(badEnv); err == nil {
		ck(false, "unknown op direct error")
	}
	if _, err := evalSwitch(errExpr{}, badEnv); err == nil {
		ck(false, "unknown expr switch error")
	}
	if _, err := evalReflect(errExpr{}, badEnv); err == nil {
		ck(false, "err expr reflect error")
	}

	for r := 0; r < rounds; r++ {
		base := uafSeed*100 + int64(r)*7
		delta := int64(uafSeed%5) + 1 + int64(r%3)
		factor := int64(2 + (uafSeed+int64(r))%3)
		env := &Env{
			Vars: map[string]int64{"x": base + 1, "y": base + 2},
			Ops: map[string]Op{
				"inc": funcOp{id: "inc", fn: func(v int64) int64 { return v + delta }, tag: &node{V: base, Label: "fn"}},
				"tri": scaleOp{factor: factor, label: "tri"},
				"off": offsetOp{delta: delta, label: "off"},
			},
			Tag: &node{V: base + 3, Label: "env"},
		}
		expr := addExpr{
			L: mulExpr{L: varRef("x"), R: literal(3)},
			R: callExpr{Name: "inc", Arg: addExpr{L: varRef("y"), R: callExpr{Name: "off", Arg: literal(4)}}},
		}
		want := 3*(base+1) + (base + 2) + 4 + 2*delta

		envSink = env
		exprSink = expr
		ifaceSink = []any{expr, env, env.Ops["inc"], env.Ops["tri"], env.Ops["off"]}
		gcStress()

		got1, err1 := expr.Eval(env)
		got2, err2 := evalSwitch(expr, env)
		got3, err3 := evalReflect(expr, env)
		ck(err1 == nil && err2 == nil && err3 == nil, "three eval paths no error")
		ck(got1 == want && got2 == want && got3 == want, "three eval paths agree")
		ck(env.Tag.V == base+3 && env.Tag.Label == "env", "env payload after GC")

		// 接口断言、反射方法调用与装箱值。
		e2, ok := ifaceSink[0].(Expr)
		ck(ok, "boxed expr assertion")
		g, err := e2.Eval(envSink)
		ck(err == nil && g == want, "boxed expr eval after GC")
		op, ok := ifaceSink[2].(Op)
		ck(ok && op.Name() == "inc", "boxed op name")
		ck(op.Apply(5) == 5+delta, "boxed op apply after GC")
		ck(reflect.ValueOf(op).MethodByName("Apply").Call([]reflect.Value{reflect.ValueOf(int64(5))})[0].Int() == 5+delta, "reflect op apply")
		ck(reflect.ValueOf(op).MethodByName("Name").Call(nil)[0].String() == "inc", "reflect op name")
		tr, trErr := callExpr{Name: "tri", Arg: literal(base)}.Eval(env)
		ck(trErr == nil && tr == base*factor, "scale op dispatch")
		tr2, trErr2 := evalSwitch(callExpr{Name: "tri", Arg: literal(base)}, env)
		ck(trErr2 == nil && tr2 == tr, "scale op switch dispatch")
		mv := op.Apply
		gcStress()
		ck(mv(7) == 7+delta, "interface method value after GC")

		raw := box(want)
		v1, ok1 := unbox(raw)
		v2, ok2 := unboxReflect(raw)
		ck(ok1 && ok2 && v1 == want && v2 == want, "boxed value unbox after GC")

		// 嵌套负号表达式保持同一结果。
		ne := negExpr{X: addExpr{L: literal(base), R: literal(delta)}}
		nv, err := ne.Eval(env)
		nv2, err2 := evalSwitch(ne, env)
		ck(err == nil && err2 == nil && nv == -(base+delta) && nv2 == nv, "neg expr paths agree")
	}
	finish("l06_iface_interp_eval")
}
