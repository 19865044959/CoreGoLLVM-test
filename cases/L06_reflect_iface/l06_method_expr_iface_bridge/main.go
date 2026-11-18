// L06: 接口方法表达式/方法值与具体类型方法表达式：绑定接收者、typed nil、嵌入接口、
// 反射方法派发与跨 GC 的接口值闭包。
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

type Scorer interface {
	Name() string
	Score(int64) int64
}

type Extra interface {
	Scorer
	Extra() int64
}

type valueImpl struct {
	label string
	base  int64
}

func (v valueImpl) Name() string        { return v.label }
func (v valueImpl) Score(d int64) int64 { return v.base + d }

type ptrImpl struct {
	label string
	base  int64
	Tag   *node
}

func (p *ptrImpl) Name() string {
	if p == nil {
		return "nil-ptr"
	}
	return p.label
}

func (p *ptrImpl) Score(d int64) int64 {
	if p == nil {
		return -1
	}
	return p.base + d + p.Tag.V
}

func (p *ptrImpl) Extra() int64 {
	if p == nil {
		return -2
	}
	return p.base * 10
}

type wrapper struct {
	Scorer
}

type nameFn func(Scorer) string

var (
	failed  int32
	gcSink  [][]byte
	anySink any
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
	base := uafSeed

	nameExpr := Scorer.Name
	scoreExpr := Scorer.Score
	extraExpr := Extra.Extra
	extraName := Extra.Name

	ck(reflect.TypeOf(nameExpr) == reflect.TypeOf(func(Scorer) string { return "" }), "Scorer.Name expression type")
	ck(reflect.TypeOf(scoreExpr) == reflect.TypeOf(func(Scorer, int64) int64 { return 0 }), "Scorer.Score expression type")
	ck(reflect.TypeOf(extraExpr) == reflect.TypeOf(func(Extra) int64 { return 0 }), "Extra.Extra expression type")
	ck(reflect.TypeOf(extraName) == reflect.TypeOf(func(Extra) string { return "" }), "Extra.Name promoted expression type")

	p := &ptrImpl{label: "ptr", base: base, Tag: &node{V: 4, Label: "tag"}}
	v := valueImpl{label: "val", base: base + 1}

	ck(nameExpr(p) == "ptr", "interface method expression on pointer impl")
	ck(nameExpr(v) == "val", "interface method expression on value impl")
	ck(scoreExpr(p, 2) == base+6, "score expression dispatches dynamic receiver")
	ck(scoreExpr(v, 3) == base+4, "score expression value impl")
	ck(extraExpr(p) == base*10, "promoted Extra method expression")
	ck(extraName(p) == "ptr", "promoted Extra.Name expression")

	var s Scorer = p
	boundName := s.Name
	boundScore := s.Score
	s = v
	p = nil // receiver 仅由方法值闭包保活
	gcStress()
	ck(boundName() == "ptr", "interface method value pins original receiver")
	ck(boundScore(5) == base+9, "interface bound score after GC")

	// typed nil 动态接收者：接口方法表达式与方法值都应按指针方法规则派发。
	var np *ptrImpl
	var ns Scorer = np
	nf := ns.Name
	ck(nf() == "nil-ptr", "typed nil interface method value")
	ck(nameExpr(ns) == "nil-ptr", "typed nil interface method expression")
	ck(scoreExpr(ns, 1) == -1, "typed nil score expression")

	// nil 接口：方法值创建与方法表达式调用都必须 panic。
	func() {
		defer func() { ck(recover() != nil, "method value from nil interface panics") }()
		var nilS Scorer
		_ = nilS.Name
	}()
	func() {
		defer func() { ck(recover() != nil, "interface method expression with nil interface panics") }()
		var nilS Scorer
		_ = nameExpr(nilS)
	}()

	// 嵌入接口的 wrapper 与提升方法。
	w := wrapper{Scorer: &ptrImpl{label: "wrapped", base: base + 2, Tag: &node{V: 1}}}
	ck(w.Name() == "wrapped", "promoted interface method call")
	ck(nameExpr(w) == "wrapped", "expression on wrapper promoted method")
	boundW := w.Score
	gcStress()
	ck(boundW(2) == base+5, "wrapper bound score after GC")

	// 方法表达式/方法值以接口形式存表，跨 GC 后按原签名叫回。
	table := map[string]any{
		"name":  nameExpr,
		"score": scoreExpr,
		"extra": extraExpr,
		"bound": boundName,
	}
	x := &ptrImpl{label: "x", base: base + 3, Tag: &node{V: 5}}
	gcStress()
	bname := table["name"].(func(Scorer) string)
	bscore := table["score"].(func(Scorer, int64) int64)
	bextra := table["extra"].(func(Extra) int64)
	bbound := table["bound"].(func() string)
	ck(bname(x) == "x", "method expression from any table")
	ck(bscore(x, 1) == base+9, "score expression from any table")
	ck(bextra(x) == (base+3)*10, "extra expression from any table")
	ck(bbound() == "ptr", "bound method value from any table after GC")

	// 采集中转：方法值与表达式经 chan any 传递。
	ch := make(chan any, 2)
	ch <- boundName
	ch <- extraExpr
	gcStress()
	cf := (<-ch).(func() string)
	ce := (<-ch).(func(Extra) int64)
	ck(cf() == "ptr", "method value through chan after GC")
	ck(ce(x) == (base+3)*10, "method expression through chan after GC")

	// 接口值上的反射方法派发与具体类型方法表达式。
	rs := Scorer(&ptrImpl{label: "reflect", base: base + 4, Tag: &node{V: 2}})
	anySink = rs
	gcStress()
	rv := reflect.ValueOf(rs).MethodByName("Score")
	ck(rv.IsValid() && rv.Call([]reflect.Value{reflect.ValueOf(int64(3))})[0].Int() == base+9,
		"reflect interface method dispatch after GC")
	ck(reflect.TypeOf(rs).Implements(reflect.TypeOf((*Scorer)(nil)).Elem()), "dynamic type implements Scorer")
	concName := (*ptrImpl).Name
	ck(reflect.TypeOf(concName) != reflect.TypeOf(nameExpr), "concrete and interface method expression types differ")
	ck(concName(&ptrImpl{label: "concrete"}) == "concrete", "concrete method expression")
	var nf2 nameFn = Scorer.Name
	ck(nf2(v) == "val", "interface method expression assignable to named func type")

	anySink = []any{nameExpr, scoreExpr, extraExpr, boundName, boundScore, nf}
	gcStress()
	ck(anySink.([]any)[0].(func(Scorer) string)(x) == "x", "expression retained only by interface after GC")

	finish("l06_method_expr_iface_bridge")
}
