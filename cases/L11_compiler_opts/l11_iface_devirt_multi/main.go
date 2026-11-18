// L11: 多实现接口去虚拟化——10 个值实现 + 指针实现 + nil 安全实现经
// 接口调用、方法值、方法表达式、type switch、泛型与断言的结果必须一致。
package main

import (
	"fmt"
	"os"
	"runtime"
	"strconv"
	"sync/atomic"
)

var (
	failed int32
	gcSink [][]byte
	seed   uint64 = 1
)

func ck(ok bool, msg string) {
	if !ok {
		atomic.AddInt32(&failed, 1)
		fmt.Printf("FAIL: %s (UAF_SEED=%d)\n", msg, seed)
	}
}

func gcStress() {
	for i := 0; i < 16; i++ {
		b := make([]byte, 4096)
		b[0] = byte(i)
		b[len(b)-1] = byte(255 - i)
		gcSink = append(gcSink[:0], b)
	}
	gcSink = gcSink[:0]
	runtime.GC()
	runtime.GC()
}

func loadSeed() uint64 {
	s := os.Getenv("UAF_SEED")
	if s == "" {
		return 1
	}
	n, err := strconv.ParseUint(s, 10, 64)
	if err != nil {
		return 1
	}
	return n
}

func finish(name string) {
	if n := atomic.LoadInt32(&failed); n != 0 {
		fmt.Printf("FAILED %s (%d failed checks, seed=%d)\n", name, n, seed)
		os.Exit(1)
	}
	fmt.Println("ALL PASS " + name)
}

type op interface {
	ID() int
	Calc(int64) int64
	Tag() string
}

type ider interface {
	ID() int
}

type opBase struct {
	id  int
	mul int64
	add int64
}

func (b opBase) ID() int            { return b.id }
func (b opBase) Calc(x int64) int64 { return x*b.mul + b.add }

type opA struct{ opBase }
type opB struct{ opBase }
type opC struct{ opBase }
type opD struct{ opBase }
type opE struct{ opBase }
type opF struct{ opBase }
type opG struct{ opBase }
type opH struct{ opBase }
type opI struct{ opBase }
type opJ struct{ opBase }

func (o opA) Tag() string { return "A" }
func (o opB) Tag() string { return "B" }
func (o opC) Tag() string { return "C" }
func (o opD) Tag() string { return "D" }
func (o opE) Tag() string { return "E" }
func (o opF) Tag() string { return "F" }
func (o opG) Tag() string { return "G" }
func (o opH) Tag() string { return "H" }
func (o opI) Tag() string { return "I" }
func (o opJ) Tag() string { return "J" }

type opM struct{ mul, add int64 }

func (o *opM) ID() int            { return 201 }
func (o *opM) Calc(x int64) int64 { return x*o.mul + o.add }
func (o *opM) Tag() string        { return "M" }

type opN struct{ mul, add int64 }

func (o *opN) ID() int            { return 202 }
func (o *opN) Calc(x int64) int64 { return x*o.mul + o.add }
func (o *opN) Tag() string        { return "N" }

// opNilSafe 的指针接收者显式处理 nil，经接口调用也必须安全。
type opNilSafe struct{}

func (o *opNilSafe) ID() int { return 300 }
func (o *opNilSafe) Calc(x int64) int64 {
	if o == nil {
		return -1
	}
	return x
}
func (o *opNilSafe) Tag() string { return "O" }

// opIderOnly 只实现窄接口，用于断言失败路径。
type opIderOnly struct{ id int }

func (o opIderOnly) ID() int { return o.id }

type opSpec struct {
	tag string
	id  int
	mul int64
	add int64
}

func opSpecs() []opSpec {
	return []opSpec{
		{"A", 1, 3, 5},
		{"B", 2, 5, -7},
		{"C", 3, -2, 11},
		{"D", 4, 7, 0},
		{"E", 5, -3, -9},
		{"F", 6, 9, 13},
		{"G", 7, 1, -1},
		{"H", 8, 11, 17},
		{"I", 9, -5, 21},
		{"J", 10, 13, -23},
		{"M", 201, 2, 101},
		{"N", 202, -4, 7},
		{"O", 300, 0, 0},
	}
}

func buildOps() []op {
	s := opSpecs()
	return []op{
		opA{opBase{s[0].id, s[0].mul, s[0].add}},
		opB{opBase{s[1].id, s[1].mul, s[1].add}},
		opC{opBase{s[2].id, s[2].mul, s[2].add}},
		opD{opBase{s[3].id, s[3].mul, s[3].add}},
		opE{opBase{s[4].id, s[4].mul, s[4].add}},
		opF{opBase{s[5].id, s[5].mul, s[5].add}},
		opG{opBase{s[6].id, s[6].mul, s[6].add}},
		opH{opBase{s[7].id, s[7].mul, s[7].add}},
		opI{opBase{s[8].id, s[8].mul, s[8].add}},
		opJ{opBase{s[9].id, s[9].mul, s[9].add}},
		&opM{mul: s[10].mul, add: s[10].add},
		&opN{mul: s[11].mul, add: s[11].add},
		(*opNilSafe)(nil),
	}
}

func expectedCalc(s opSpec, x int64) int64 {
	if s.tag == "O" {
		return -1
	}
	return x*s.mul + s.add
}

// concreteCalc 在 type switch 的具体类型分支上直接调用，形成去虚拟化对照。
func concreteCalc(o op, x int64) int64 {
	switch v := o.(type) {
	case opA:
		return v.Calc(x)
	case opB:
		return v.Calc(x)
	case opC:
		return v.Calc(x)
	case opD:
		return v.Calc(x)
	case opE:
		return v.Calc(x)
	case opF:
		return v.Calc(x)
	case opG:
		return v.Calc(x)
	case opH:
		return v.Calc(x)
	case opI:
		return v.Calc(x)
	case opJ:
		return v.Calc(x)
	case *opM:
		return v.Calc(x)
	case *opN:
		return v.Calc(x)
	case *opNilSafe:
		return v.Calc(x)
	default:
		return -999999
	}
}

func staticLocalCall(x int64, tag string) int64 {
	switch tag {
	case "A":
		var o op = opA{opBase{1, 3, 5}}
		return o.Calc(x)
	case "B":
		var o op = opB{opBase{2, 5, -7}}
		return o.Calc(x)
	default:
		var o op = &opM{mul: 2, add: 101}
		return o.Calc(x)
	}
}

func genericCalc[T op](v T, x int64) int64 { return v.Calc(x) }

func expectPanic(name string, fn func()) {
	defer func() {
		r := recover()
		ck(r != nil, name+": expected panic")
	}()
	fn()
}

func main() {
	seed = loadSeed()
	ops := buildOps()
	specs := opSpecs()
	ck(len(ops) == len(specs), "ops/spec length mismatch")

	xs := []int64{0, 1, -1, 7, -13, 1 << 20, -(1 << 20), int64(seed%2000) - 1000}

	for round := 0; round < 3; round++ {
		for i := range ops {
			o, s := ops[i], specs[i]
			ck(o.ID() == s.id, "interface ID mismatch")
			ck(o.Tag() == s.tag, "interface Tag mismatch")
			for _, x := range xs {
				want := expectedCalc(s, x)
				if o.Calc(x) != want {
					ck(false, "interface dispatch mismatch")
				}
				mv := o.Calc
				if mv(x) != want {
					ck(false, "method value dispatch mismatch")
				}
				if concreteCalc(o, x) != want {
					ck(false, "type-switch direct dispatch mismatch")
				}
			}
		}
		gcStress()
	}

	// type switch 全覆盖且无落到 default 的实例。
	for i := range ops {
		ck(concreteCalc(ops[i], 3) != -999999, "type switch missed concrete type")
	}

	// 方法表达式与直接调用。
	expr := opA.Calc
	ck(expr(opA{opBase{1, 3, 5}}, 4) == 17, "method expression opA mismatch")
	pexpr := (*opM).Calc
	ck(pexpr(&opM{mul: 2, add: 1}, 3) == 7, "method expression opM mismatch")

	// 泛型接口约束下的静态调用。
	ck(genericCalc(opA{opBase{1, 3, 5}}, 6) == 23, "generic opA mismatch")
	ck(genericCalc(&opM{mul: 2, add: 101}, 3) == 107, "generic opM mismatch")
	ck(genericCalc(opJ{opBase{10, 13, -23}}, -2) == -49, "generic opJ mismatch")

	// 局部静态类型已知的接口调用（可去虚拟化）。
	ck(staticLocalCall(9, "A") == 32, "static local opA mismatch")
	ck(staticLocalCall(9, "B") == 38, "static local opB mismatch")
	ck(staticLocalCall(9, "M") == 119, "static local opM mismatch")

	// 接口相等性仅比较动态类型与值。
	var i1, i2 op = opA{opBase{1, 3, 5}}, opA{opBase{1, 3, 5}}
	var i3 op = opB{opBase{1, 3, 5}}
	ck(i1 == i2, "same dynamic type/value interface equality mismatch")
	ck(i1 != i3, "different dynamic type interface equality mismatch")

	// 窄接口与接口到接口断言。
	var only ider = opIderOnly{id: 77}
	_, ok := only.(op)
	ck(!ok, "ider-only value unexpectedly implements op")
	var iop op = ops[0]
	if iv, ok := iop.(ider); ok {
		ck(iv.ID() == specs[0].id, "interface to narrow assertion ID mismatch")
	} else {
		ck(false, "interface to narrow assertion failed")
	}
	var wide ider = iop
	if back, ok := wide.(op); ok {
		ck(back.Tag() == specs[0].tag, "interface to interface assertion Tag mismatch")
	} else {
		ck(false, "interface to interface assertion failed")
	}

	// nil 接口与 typed nil。
	var nilOp op
	ck(nilOp == nil, "nil interface comparison mismatch")
	expectPanic("nil-interface-call", func() { _ = nilOp.Calc(1) })
	var mp *opM
	var typedNil op = mp
	ck(typedNil != nil, "typed nil interface must not equal nil")
	expectPanic("typed-nil-deref", func() { _ = typedNil.Calc(1) })

	// nil 安全接收者经接口仍返回固定值。
	var ns op = (*opNilSafe)(nil)
	ck(ns != nil && ns.ID() == 300 && ns.Tag() == "O", "nil-safe interface identity mismatch")
	ck(ns.Calc(12345) == -1, "nil-safe receiver through interface mismatch")

	// 全部接口值与结果跨最终 GC。
	gcStress()
	for i := range ops {
		ck(ops[i].ID() == specs[i].id, "retained interface ID corrupted after GC")
		ck(ops[i].Tag() == specs[i].tag, "retained interface Tag corrupted after GC")
		ck(ops[i].Calc(11) == expectedCalc(specs[i], 11), "retained interface Calc corrupted after GC")
	}
	ck(only.ID() == 77, "retained narrow interface corrupted after GC")

	finish("l11_iface_devirt_multi")
}
