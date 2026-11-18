// L06: 自定义 errors.As 路由：As(any) 合成目标、Join/多 %w 森林左到右遍历顺序、
// typed nil 与自定义 Unwrap 链、As 目标校验 panic，跨 GC 保持标签负载。
package main

import (
	"errors"
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

type routeTarget struct {
	Route string
	Tag   *node
}

func (t *routeTarget) Error() string {
	if t == nil {
		return "route-target-nil"
	}
	return "route:" + t.Route
}

type routeErr struct {
	route string
	cause error
	tag   *node
}

func (e *routeErr) Error() string {
	if e == nil {
		return "route-nil"
	}
	return "route:" + e.route
}

func (e *routeErr) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.cause
}

func (e *routeErr) As(target any) bool {
	if e == nil {
		return false
	}
	switch t := target.(type) {
	case **routeTarget:
		*t = &routeTarget{Route: e.route, Tag: e.tag}
		return true
	}
	return false
}

type boxErr struct {
	cause error
	tag   *node
}

func (e *boxErr) Error() string {
	if e == nil {
		return "box-nil"
	}
	return "box"
}

func (e *boxErr) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.cause
}

type forestErr struct {
	errs []error
}

func (e *forestErr) Error() string { return "forest" }

func (e *forestErr) Unwrap() []error {
	if e == nil {
		return nil
	}
	return e.errs
}

var (
	failed  int32
	gcSink  [][]byte
	errSink []error
	anySink []any
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

func routeOf(err error) (string, bool) {
	var rt *routeTarget
	if !errors.As(err, &rt) {
		return "", false
	}
	return rt.Route, true
}

func main() {
	uafSeed = envSeed()
	const rounds = 40

	errT := reflect.TypeOf((*error)(nil)).Elem()
	asT := reflect.TypeOf((*interface{ As(any) bool })(nil)).Elem()
	unwrapAllT := reflect.TypeOf((*interface{ Unwrap() []error })(nil)).Elem()
	ck(reflect.TypeOf(&routeErr{}).Implements(errT), "routeErr implements error")
	ck(reflect.TypeOf(&routeErr{}).Implements(asT), "routeErr implements custom As")
	ck(reflect.TypeOf(&boxErr{}).Implements(errT), "boxErr implements error")
	ck(!reflect.TypeOf(&boxErr{}).Implements(asT), "boxErr lacks custom As")
	ck(reflect.TypeOf(&forestErr{}).Implements(unwrapAllT), "forestErr implements Unwrap []error")
	ck(reflect.TypeOf(&routeTarget{}).Implements(errT), "routeTarget implements error")

	// 自定义 As 合成链上不存在的目标；Is 不受影响。
	direct := &routeErr{route: "direct", tag: &node{V: 11, Label: "d"}}
	var rt *routeTarget
	ck(errors.As(direct, &rt) && rt != nil, "custom As synthesizes target")
	ck(rt.Route == "direct" && rt.Tag == direct.tag && rt.Tag.V == 11, "custom As payload")
	ck(!errors.Is(direct, rt), "custom As target not visible to Is")
	var re *routeErr
	ck(errors.As(direct, &re) && re == direct, "standard As still finds concrete error")

	// 无 As 的外层盒子解包后触发内层自定义 As。
	boxed := &boxErr{cause: direct, tag: &node{V: 12, Label: "b"}}
	ck(errors.As(boxed, &rt) && rt.Route == "direct", "custom As through box unwrap")

	// Join 左到右优先，As 取第一棵匹配子树。
	j := errors.Join(
		&routeErr{route: "j1", tag: &node{V: 21, Label: "j1"}},
		&routeErr{route: "j2", tag: &node{V: 22, Label: "j2"}},
	)
	ck(errors.As(j, &rt) && rt.Route == "j1", "Join custom As left to right")
	var jr *routeErr
	ck(errors.As(j, &jr) && jr.route == "j1", "Join standard As left to right")
	if rr, rok := routeOf(j); !rok || rr != "j1" {
		ck(false, "Join route helper")
	}

	// 多 %w：两个分支、Unwrap 返回 nil、Is 两条都命中。
	m1 := &routeErr{route: "m1", tag: &node{V: 31, Label: "m1"}}
	m2 := &routeErr{route: "m2", tag: &node{V: 32, Label: "m2"}}
	mw := fmt.Errorf("both: %w | %w", m1, m2)
	ck(errors.Unwrap(mw) == nil, "multi-wrap Unwrap returns nil")
	ub, ok := mw.(interface{ Unwrap() []error })
	ck(ok && len(ub.Unwrap()) == 2 && ub.Unwrap()[0] == m1 && ub.Unwrap()[1] == m2, "multi-wrap branch order")
	ck(errors.Is(mw, m1) && errors.Is(mw, m2), "multi-wrap Is both branches")
	ck(errors.As(mw, &rt) && rt.Route == "m1", "multi-wrap As first branch")
	ck(mw.Error() == "both: route:m1 | route:m2", "multi-wrap deterministic text")

	// 单 %w 包裹 Join 时 As 继续下钻。
	jn := errors.Join(&routeErr{route: "p", tag: &node{V: 41, Label: "p"}}, &routeErr{route: "q", tag: &node{V: 42, Label: "q"}})
	mwj := fmt.Errorf("wrap: %w", jn)
	ck(errors.As(mwj, &rt) && rt.Route == "p", "wrapped Join custom As")
	ck(errors.Is(mwj, jn), "wrapped Join Is")

	// 自定义 forest 与 Join 等价遍历。
	fe := &forestErr{errs: []error{m1, &routeErr{route: "m3", tag: &node{V: 33, Label: "m3"}}}}
	ck(errors.As(fe, &rt) && rt.Route == "m1", "forest custom As first child")
	ck(errors.Is(fe, m1), "forest Is first child")

	// Join nil 过滤与 typed nil 分支。
	ck(errors.Join(nil, nil) == nil, "Join all nil is nil")
	one := errors.Join(m1)
	oub, ok := one.(interface{ Unwrap() []error })
	ck(ok && len(oub.Unwrap()) == 1, "Join single non-nil branch")
	clean := errors.Join(m1, nil, m2)
	cub, ok := clean.(interface{ Unwrap() []error })
	ck(ok && len(cub.Unwrap()) == 2, "Join filters nil branches")
	ck(errors.Is(clean, m1) && errors.Is(clean, m2), "Join filtered Is")
	var tn *routeErr
	var tnErr error = tn
	jtn := errors.Join(tnErr, m2)
	tub, ok := jtn.(interface{ Unwrap() []error })
	ck(ok && len(tub.Unwrap()) == 2, "Join keeps typed nil branch")
	ck(errors.Is(jtn, tnErr) && errors.Is(jtn, m2), "Join typed nil Is")
	var got *routeErr
	ck(errors.As(jtn, &got) && got == nil, "typed nil standard As target")
	var nt *routeTarget
	ck(!errors.As(tnErr, &nt), "custom As nil receiver fails cleanly")
	var nilTarget *routeErr
	ck(errors.As(tnErr, &nilTarget) && nilTarget == nil, "typed nil direct As")

	// As 目标校验 panic。
	mustPanic("As nil target", func() { errors.As(m1, nil) })
	mustPanic("As non-pointer target", func() {
		var e error = m1
		errors.As(m1, e)
	})
	mustPanic("As pointer to non-error", func() {
		var x int
		errors.As(m1, &x)
	})

	for r := 0; r < rounds; r++ {
		base := uafSeed*100 + int64(r)
		aErr := &routeErr{route: fmt.Sprintf("a%d", base), cause: &boxErr{tag: &node{V: base, Label: "box"}}, tag: &node{V: base + 1, Label: "a"}}
		bErr := &routeErr{route: fmt.Sprintf("b%d", base), cause: aErr, tag: &node{V: base + 2, Label: "b"}}
		cErr := &routeErr{route: fmt.Sprintf("c%d", base), tag: &node{V: base + 3, Label: "c"}}
		dErr := &routeErr{route: fmt.Sprintf("d%d", base), tag: &node{V: base + 4, Label: "d"}}
		joined := errors.Join(bErr, cErr)
		mixed := fmt.Errorf("mix(%w|%w)", aErr, dErr)
		errSink = []error{joined, mixed, bErr, aErr}
		anySink = []any{joined, mixed}
		gcStress()

		var tr *routeTarget
		ck(errors.As(joined, &tr) && tr.Route == fmt.Sprintf("b%d", base) && tr.Tag.V == base+2, "Join As after GC")
		ck(errors.As(mixed, &tr) && tr.Route == fmt.Sprintf("a%d", base) && tr.Tag.V == base+1, "multi-wrap As after GC")
		var ce *boxErr
		ck(errors.As(aErr, &ce) && ce.tag.V == base, "box Err payload after GC")
		ck(errors.As(bErr, &tr) && tr.Route == fmt.Sprintf("b%d", base), "chain custom As after GC")

		var rb *routeErr
		ck(errors.As(joined, &rb) && rb == bErr, "Join standard As identity")
		ck(errors.Is(joined, bErr) && errors.Is(joined, aErr) && errors.Is(joined, cErr), "Join Is chain")
		ck(!errors.Is(joined, dErr), "Join Is excludes other branch")
		ck(errors.Is(mixed, aErr) && errors.Is(mixed, dErr), "multi-wrap Is after GC")
		ck(errors.Unwrap(mixed) == nil, "multi-wrap Unwrap nil after GC")

		var wb *boxErr
		wrapped := &boxErr{cause: bErr, tag: &node{V: base + 5, Label: "wb"}}
		ck(errors.As(wrapped, &wb) && wb == wrapped, "outer box standard As")
		ck(errors.As(wrapped, &tr) && tr.Route == fmt.Sprintf("b%d", base), "box unwrap custom As")
		ck(errors.As(wrapped, &rb) && rb == bErr, "box unwrap standard As")
	}
	finish("l06_error_custom_as")
}
