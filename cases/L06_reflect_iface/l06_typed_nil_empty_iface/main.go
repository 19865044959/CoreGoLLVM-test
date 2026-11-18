// L06: typed nil 与空接口：nil 判定、断言、反射、DeepEqual、map key、比 panic 矩阵。
package main

import (
	"errors"
	"fmt"
	"io"
	"math/rand"
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

type myErr struct {
	Code int64
}

func (e *myErr) Error() string {
	if e == nil {
		return "myerr-nil"
	}
	return "myerr"
}

type safeReader struct {
	tag *node
}

func (r *safeReader) Read(p []byte) (int, error) {
	if r == nil {
		return 0, io.EOF
	}
	p[0] = byte(r.tag.V)
	return 1, nil
}

type holder struct {
	I any
	E error
	R io.Reader
}

var (
	failed  int32
	gcSink  [][]byte
	anySink []any
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

	var p *node
	var s []*node
	var m map[string]*node
	var f func() int64
	var c chan int64
	var e error = (*myErr)(nil)
	var r io.Reader = (*safeReader)(nil)

	type nilCase struct {
		name string
		v    any
		kind reflect.Kind
		typ  reflect.Type
	}
	cases := []nilCase{
		{"ptr", p, reflect.Ptr, reflect.TypeOf((*node)(nil))},
		{"slice", s, reflect.Slice, reflect.TypeOf([]*node(nil))},
		{"map", m, reflect.Map, reflect.TypeOf(map[string]*node(nil))},
		{"func", f, reflect.Func, reflect.TypeOf((func() int64)(nil))},
		{"chan", c, reflect.Chan, reflect.TypeOf((chan int64)(nil))},
		{"error", e, reflect.Ptr, reflect.TypeOf((*myErr)(nil))},
		{"reader", r, reflect.Ptr, reflect.TypeOf((*safeReader)(nil))},
	}

	var nilAny any
	ck(nilAny == nil, "nil empty interface")
	ck(reflect.TypeOf(nilAny) == nil, "TypeOf(nil interface) is nil")
	ck(!reflect.ValueOf(nilAny).IsValid(), "ValueOf(nil interface) invalid")
	func() {
		defer func() { ck(recover() != nil, "IsNil on invalid Value panics") }()
		_ = reflect.ValueOf(nilAny).IsNil()
	}()

	const rounds = 6
	for round := 0; round < rounds; round++ {
		rnd := rand.New(rand.NewSource(uafSeed + int64(round)*53))
		order := rnd.Perm(len(cases))
		for _, ci := range order {
			nc := cases[ci]
			ck(nc.v != nil, "typed nil "+nc.name+" inside interface is non-nil")
			rv := reflect.ValueOf(nc.v)
			ck(rv.Kind() == nc.kind, "typed nil kind "+nc.name)
			ck(rv.IsNil(), "typed nil IsNil "+nc.name)
			ck(reflect.TypeOf(nc.v) == nc.typ, "typed nil type "+nc.name)
		}

		ck(e != nil, "typed nil error is non-nil")
		ck(e.Error() == "myerr-nil", "nil receiver method is callable")
		var te *myErr
		ck(errors.As(e, &te) && te == nil, "errors.As matches typed nil")
		ck(errors.Is(e, (*myErr)(nil)), "errors.Is compares typed nil equal")
		direct, ok := e.(*myErr)
		ck(ok && direct == nil, "direct comma-ok assertion of typed nil")

		ck(reflect.DeepEqual(any(e), any((*myErr)(nil))), "DeepEqual typed nil same type")
		ck(!reflect.DeepEqual(any(e), nil), "DeepEqual typed nil vs nil interface")
		ck(reflect.DeepEqual(any(p), any((*node)(nil))), "DeepEqual typed nil pointer")
		ck(!reflect.DeepEqual(any(p), any((*int64)(nil))), "DeepEqual different typed nil types")

		ck(any(p) == any((*node)(nil)), "typed nil interface equality")
		ck(any(p) != any((*int64)(nil)), "different typed nil pointer types differ")
		func() {
			defer func() { ck(recover() != nil, "comparing slices as interfaces panics") }()
			_ = any(s) == any(s)
		}()

		mk := map[any]int64{}
		mk[nil] = 1
		mk[any(p)] = 2
		mk[any((*node)(nil))] = 3
		q := &node{V: 1, Label: "q"}
		mk[any(q)] = 4
		ck(len(mk) == 3 && mk[nil] == 1 && mk[any(p)] == 3 && mk[any(q)] == 4, "nil and typed nil map keys")

		var h holder
		ck(h.I == nil && h.E == nil && h.R == nil, "zero holder interfaces are nil")
		h.I = p
		h.E = e
		h.R = r
		ck(h.I != nil && h.E != nil && h.R != nil, "assigning typed nil makes fields non-nil")
		nRead, _ := h.R.Read(nil)
		ck(nRead == 0, "nil receiver Read returns EOF-safe result")
		if _, err := h.R.Read(make([]byte, 1)); err != io.EOF {
			ck(false, "typed nil reader returns io.EOF")
		}

		ck(fmt.Sprint(any(p)) == "<nil>", "fmt prints typed nil pointer as <nil>")
		ck(fmt.Sprint(any(e)) == "myerr-nil", "fmt uses nil-safe Error method")

		ck(reflect.ValueOf(any(p)).Elem().IsValid() == false, "Elem of nil pointer is invalid")
		ck(reflect.ValueOf(any(s)).Len() == 0 && reflect.ValueOf(any(m)).Len() == 0, "len of typed nil slice/map")
		ck(reflect.ValueOf(any(f)).Pointer() == 0, "nil func pointer is zero")
		ck(reflect.ValueOf(any(c)).Pointer() == 0, "nil chan pointer is zero")

		anySink = []any{p, s, m, f, c, e, r, h, mk}
		gcStress()
		ck(anySink[6] != nil && reflect.ValueOf(anySink[6]).IsNil(), "typed nil reader stable after GC")
		ck(anySink[3] != nil && reflect.ValueOf(anySink[3]).IsNil(), "typed nil func stable after GC")
		ck(anySink[8].(map[any]int64)[nil] == 1, "interface-key map stable after GC")

		gcStress()
		ck(reflect.DeepEqual(any(e), any((*myErr)(nil))), "DeepEqual typed nil after GC")
		ck(errors.As(e, &te) && te == nil, "errors.As typed nil after GC")
		h = holder{}
		ck(h.I == nil && h.E == nil && h.R == nil, "reset holder clears interface fields")
	}
	finish("l06_typed_nil_empty_iface")
}
