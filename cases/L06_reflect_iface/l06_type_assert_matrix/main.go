// L06: 类型断言矩阵：20 个动态类型 × 8 个接口目标、方法集/提升、接口到接口、typed nil、panic，跨 GC。
package main

import (
	"fmt"
	"os"
	"reflect"
	"runtime"
	"strconv"
	"sync/atomic"
)

type reader interface{ Read() int64 }
type writer interface{ Write(int64) int64 }
type readWriter interface {
	reader
	writer
}
type secretReader interface {
	reader
	secret() int64
}
type secretOnly interface{ secret() int64 }
type rwAnon interface {
	Read() int64
	Write(int64) int64
}

type val struct{ V int64 }

func (v val) Read() int64 { return v.V }

func (v *val) Write(d int64) int64 {
	v.V += d
	return v.V
}

type both struct{ V int64 }

func (b both) Read() int64         { return b.V }
func (b both) Write(d int64) int64 { return b.V + d }

type hush struct{ V int64 }

func (h hush) Read() int64   { return h.V }
func (h hush) secret() int64 { return h.V * 3 }

type ptrOnly struct{ V int64 }

func (p *ptrOnly) Read() int64         { return p.V }
func (p *ptrOnly) Write(d int64) int64 { return p.V + d }

type myErr struct{ Code int64 }

func (e *myErr) Error() string { return fmt.Sprintf("err%d", e.Code) }

type strV struct{}

func (strV) String() string { return "str" }

type wrap struct{ val }

type cand struct {
	name string
	x    any
	exp  [8]bool // any, reader, writer, readWriter, secretReader, error, Stringer, secretOnly
}

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

func targetBits(x any) [8]bool {
	var b [8]bool
	_, b[0] = x.(any)
	_, b[1] = x.(reader)
	_, b[2] = x.(writer)
	_, b[3] = x.(readWriter)
	_, b[4] = x.(secretReader)
	_, b[5] = x.(error)
	_, b[6] = x.(fmt.Stringer)
	_, b[7] = x.(secretOnly)
	return b
}

func main() {
	uafSeed = envSeed()
	base := uafSeed%17 + 3
	targetNames := []string{"any", "reader", "writer", "readWriter", "secretReader", "error", "Stringer", "secretOnly"}

	cands := []cand{
		{"val", val{V: base}, [8]bool{true, true, false, false, false, false, false, false}},
		{"*val", &val{V: base}, [8]bool{true, true, true, true, false, false, false, false}},
		{"nil *val", (*val)(nil), [8]bool{true, true, true, true, false, false, false, false}},
		{"both", both{V: base}, [8]bool{true, true, true, true, false, false, false, false}},
		{"*both", &both{V: base}, [8]bool{true, true, true, true, false, false, false, false}},
		{"hush", hush{V: base}, [8]bool{true, true, false, false, true, false, false, true}},
		{"*hush", &hush{V: base}, [8]bool{true, true, false, false, true, false, false, true}},
		{"ptrOnly value", ptrOnly{V: base}, [8]bool{true, false, false, false, false, false, false, false}},
		{"*ptrOnly", &ptrOnly{V: base}, [8]bool{true, true, true, true, false, false, false, false}},
		{"int", 7, [8]bool{true, false, false, false, false, false, false, false}},
		{"string", "s", [8]bool{true, false, false, false, false, false, false, false}},
		{"nil any", nil, [8]bool{false, false, false, false, false, false, false, false}},
		{"nil *myErr", (*myErr)(nil), [8]bool{true, false, false, false, false, true, false, false}},
		{"myErr value", myErr{Code: 3}, [8]bool{true, false, false, false, false, false, false, false}},
		{"strV", strV{}, [8]bool{true, false, false, false, false, false, true, false}},
		{"nil *strV", (*strV)(nil), [8]bool{true, false, false, false, false, false, true, false}},
		{"func", func() {}, [8]bool{true, false, false, false, false, false, false, false}},
		{"chan", make(chan int64), [8]bool{true, false, false, false, false, false, false, false}},
		{"wrap value", wrap{val{V: base}}, [8]bool{true, true, false, false, false, false, false, false}},
		{"*wrap", &wrap{val{V: base}}, [8]bool{true, true, true, true, false, false, false, false}},
	}

	// 1) 断言矩阵：20×8，逐项与预期比较。
	observedTrue := 0
	for _, c := range cands {
		got := targetBits(c.x)
		for j := range got {
			ck(got[j] == c.exp[j], "assert "+c.name+" -> "+targetNames[j])
			if got[j] {
				observedTrue++
			}
		}
	}
	ck(len(cands) == 20 && observedTrue == 48, "assertion matrix size/true count")

	// 2) reflect.Type.Implements 与运行时断言一致。
	anyT := reflect.TypeOf((*any)(nil)).Elem()
	readerT := reflect.TypeOf((*reader)(nil)).Elem()
	writerT := reflect.TypeOf((*writer)(nil)).Elem()
	rwT := reflect.TypeOf((*readWriter)(nil)).Elem()
	srT := reflect.TypeOf((*secretReader)(nil)).Elem()
	errT := reflect.TypeOf((*error)(nil)).Elem()
	stringerT := reflect.TypeOf((*fmt.Stringer)(nil)).Elem()
	soT := reflect.TypeOf((*secretOnly)(nil)).Elem()
	targets := []reflect.Type{anyT, readerT, writerT, rwT, srT, errT, stringerT, soT}
	for _, c := range cands {
		typ := reflect.TypeOf(c.x)
		if typ == nil {
			continue
		}
		for j, t := range targets {
			ck(typ.Implements(t) == c.exp[j], "Implements "+c.name+" -> "+targetNames[j])
		}
	}

	// 3) 接口方法数与结构相同的接口类型同一性。
	ck(anyT.NumMethod() == 0 && readerT.NumMethod() == 1, "basic interface method counts")
	ck(writerT.NumMethod() == 1 && rwT.NumMethod() == 2 && srT.NumMethod() == 2, "composed interface method counts")
	ck(soT.NumMethod() == 1, "secretOnly method count")
	rwAnonT := reflect.TypeOf((*rwAnon)(nil)).Elem()
	ck(rwT.AssignableTo(rwAnonT) && rwAnonT.AssignableTo(rwT), "same method set interfaces assignable")
	var rwAny any = &val{V: base}
	_, structuralOK := rwAny.(rwAnon)
	ck(structuralOK, "dynamic type satisfies structurally equal interface")

	// 4) 成功断言的类型化结果与方法调用。
	pv := &val{V: base}
	r1 := any(pv).(reader)
	w1 := any(pv).(writer)
	w1.Write(2)
	ck(r1.Read() == base+2, "pointer interface method mutation visible")
	wr := any(val{V: base}).(reader)
	ck(wr.Read() == base, "value interface method")
	bw := any(both{V: base}).(readWriter)
	ck(bw.Write(4) == base+4, "value receiver Write")
	hse := any(hush{V: base}).(secretReader)
	ck(hse.secret() == base*3, "unexported method interface call")
	var se error = &myErr{Code: base}
	ck(se.Error() == fmt.Sprintf("err%d", base), "error interface")
	ck(strV{}.String() == "str", "Stringer interface")
	wp := any(&wrap{val{V: base}}).(readWriter)
	ck(wp.Read() == base && wp.Write(1) == base+1, "promoted methods through embedding")

	// 5) 接口到接口断言按动态类型判定。
	var rv reader = &val{V: base}
	_, ok := rv.(readWriter)
	ck(ok, "reader holding *val asserts to readWriter")
	_, ok = rv.(secretReader)
	ck(!ok, "reader holding *val not secretReader")
	var rvv reader = val{V: base}
	_, ok = rvv.(writer)
	ck(!ok, "reader holding val not writer")
	var rh reader = hush{V: base}
	_, ok = rh.(secretReader)
	ck(ok, "reader holding hush is secretReader")
	var aw any = &wrap{val{V: base}}
	_, ok = aw.(writer)
	ck(ok, "any holding *wrap is writer")
	var awv any = wrap{val{V: base}}
	_, ok = awv.(writer)
	ck(!ok, "any holding wrap value is not writer")

	// 6) typed nil 与 nil 接口。
	var tn any = (*val)(nil)
	tv, ok := tn.(reader)
	ck(ok && tv != nil, "typed nil asserts to reader interface")
	tp, ok := tn.(*val)
	ck(ok && tp == nil, "typed nil asserts back to nil pointer")
	_, ok = tn.(error)
	ck(!ok, "typed nil *val is not error")
	var tnErr any = (*myErr)(nil)
	te, ok := tnErr.(error)
	ck(ok && te != nil, "typed nil *myErr is error interface")
	var nilAny any
	_, ok = nilAny.(any)
	ck(!ok, "nil interface assertion fails")
	_, ok = nilAny.(reader)
	ck(!ok, "nil interface not reader")

	// 7) 非 comma-ok 失败断言必须 panic。
	mustPanic("val value to writer", func() { _ = any(val{V: 1}).(writer) })
	mustPanic("int to reader", func() { _ = any(1).(reader) })
	mustPanic("nil to reader", func() { var n any; _ = n.(reader) })
	mustPanic("typed nil *val to error", func() { _ = tn.(error) })

	// 8) 候选装箱进接口、GC 后重跑矩阵，指针负载仍共享。
	anySink = cands
	gcStress()
	stored := anySink.([]cand)
	againTrue := 0
	for _, c := range stored {
		for _, got := range targetBits(c.x) {
			if got {
				againTrue++
			}
		}
	}
	ck(againTrue == 48, "assertion matrix after GC")

	pv2 := &val{V: base + 20}
	var pr reader = pv2
	extra := []any{pr, &wrap{val{V: base + 30}}}
	anySink = extra
	pv2.V = base + 21
	gcStress()
	saved := anySink.([]any)
	ck(saved[0].(reader).Read() == base+21, "pointer payload after GC")
	sw := saved[1].(readWriter)
	ck(sw.Read() == base+30, "promoted reader after GC")
	sw.Write(2)
	ck(saved[1].(readWriter).Read() == base+32, "promoted writer after GC")

	finish("l06_type_assert_matrix")
}
