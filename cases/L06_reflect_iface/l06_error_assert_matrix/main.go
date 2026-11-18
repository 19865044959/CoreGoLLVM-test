// L06: 错误类型断言矩阵：errors.As/Is/Unwrap、Join/forest、typed nil、panic 语义，跨 GC。
package main

import (
	"errors"
	"fmt"
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

type plainErr struct {
	tag *node
	msg string
}

func (e *plainErr) Error() string {
	if e == nil {
		return "plain-nil"
	}
	return e.msg + ":" + e.tag.Label
}

type codeErr struct {
	Code int64
	tag  *node
}

func (e codeErr) Error() string { return "code" }

func (e codeErr) Is(target error) bool {
	t, ok := target.(*codeErr)
	return ok && t != nil && t.Code == e.Code
}

type timeoutErr struct {
	tag *node
}

func (e *timeoutErr) Error() string { return "timeout" }
func (e *timeoutErr) Timeout() bool { return true }

type boxErr struct {
	inner error
	tag   *node
}

func (e *boxErr) Error() string { return "box" }
func (e *boxErr) Unwrap() error { return e.inner }

type forestErr struct {
	errs []error
	tag  *node
}

func (e *forestErr) Error() string   { return "forest" }
func (e *forestErr) Unwrap() []error { return e.errs }

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

type targetMask [6]bool

// checkAs 对 6 类 target 逐一执行 errors.As，与期望掩码比对。
// 顺序：*plainErr, *codeErr, *timeoutErr, interface{Timeout}, *boxErr, *forestErr。
func checkAs(err error, name string, want targetMask) {
	var tp *plainErr
	var tc *codeErr
	var tt *timeoutErr
	var ti interface{ Timeout() bool }
	var tb *boxErr
	var tf *forestErr
	got := targetMask{
		errors.As(err, &tp),
		errors.As(err, &tc),
		errors.As(err, &tt),
		errors.As(err, &ti),
		errors.As(err, &tb),
		errors.As(err, &tf),
	}
	for i := range got {
		if got[i] != want[i] {
			ck(false, fmt.Sprintf("%s target[%d] As mismatch", name, i))
		}
	}
	if got[0] {
		ck(tp == nil || tp.tag != nil, name+" plain target payload readable")
	}
	if got[2] {
		ck(tt == nil || tt.tag != nil, name+" timeout target payload readable")
	}
	if got[3] {
		ck(ti == nil || ti.Timeout(), name+" timeout interface target callable")
	}
}

func main() {
	uafSeed = envSeed()
	const rounds = 20

	for r := 0; r < rounds; r++ {
		rnd := rand.New(rand.NewSource(uafSeed + int64(r)*401))
		base := int64(r * 100)
		chainLen := 2 + rnd.Intn(3)

		plain := &plainErr{tag: &node{V: base + 1, Label: "p"}, msg: "boom"}
		var wrapped error = plain
		for i := 0; i < chainLen; i++ {
			wrapped = fmt.Errorf("L%d: %w", i, wrapped)
		}
		codeVal := codeErr{Code: 42, tag: &node{V: base + 2, Label: "cv"}}
		codePtr := &codeErr{Code: 42, tag: &node{V: base + 3, Label: "cp"}}
		tmo := &timeoutErr{tag: &node{V: base + 4, Label: "t"}}
		joined := errors.Join(wrapped, tmo, codePtr)
		forest := &forestErr{errs: []error{wrapped, tmo}, tag: &node{V: base + 5, Label: "f"}}
		boxed := &boxErr{inner: joined, tag: &node{V: base + 6, Label: "b"}}
		var typedNil error = (*plainErr)(nil)

		anySink = []error{wrapped, joined, forest, boxed, typedNil}
		gcStress()

		rows := []struct {
			name string
			err  error
			want targetMask
		}{
			{"nil", nil, targetMask{}},
			{"plain", plain, targetMask{true, false, false, false, false, false}},
			{"wrapped", wrapped, targetMask{true, false, false, false, false, false}},
			{"codeValue", codeVal, targetMask{}},
			{"codePtr", codePtr, targetMask{false, true, false, false, false, false}},
			{"timeout", tmo, targetMask{false, false, true, true, false, false}},
			{"joined", joined, targetMask{true, true, true, true, false, false}},
			{"forest", forest, targetMask{true, false, true, true, false, true}},
			{"boxed", boxed, targetMask{true, true, true, true, true, false}},
			{"typedNil", typedNil, targetMask{true, false, false, false, false, false}},
		}
		for _, row := range rows {
			checkAs(row.err, row.name, row.want)
		}

		// 指向接口的 As target：*error、*any、*interface{Timeout}。
		// errors.As 命中第一个可赋值错误（最外层），不会继续解包。
		var anyErr error
		ck(errors.As(wrapped, &anyErr), "As into *error target")
		ck(anyErr == wrapped, "As *error target holds outermost error")
		var anyT any
		ck(errors.As(wrapped, &anyT), "As into *any target")
		ck(anyT != nil && anyT.(error) == wrapped, "*any target holds outermost error")
		var ti interface{ Timeout() bool }
		ck(errors.As(boxed, &ti) && ti.Timeout(), "As into timeout interface through box")

		// 直接断言只看最外层，不自动解包。
		var iWrapped error = wrapped
		var iJoined error = joined
		var iForest error = forest
		var iCodePtr error = codePtr
		var iTypedNil error = typedNil
		_, ok1 := iWrapped.(*plainErr)
		_, ok2 := iJoined.(*plainErr)
		_, ok3 := iJoined.(interface{ Unwrap() []error })
		_, ok4 := iForest.(interface{ Unwrap() []error })
		cp, ok5 := iCodePtr.(*codeErr)
		vn, ok6 := iTypedNil.(*plainErr)
		_, ok7 := error(nil).(*plainErr)
		ck(!ok1 && !ok2 && ok3 && ok4 && ok5 && cp.Code == 42, "direct assertion matrix")
		ck(ok6 && vn == nil, "typed nil direct assertion")
		ck(!ok7, "nil interface assertion fails")

		// errors.Is：链、Join 森林、自定义 Is、typed nil。
		ck(errors.Is(wrapped, plain), "Is through fmt wrap chain")
		ck(errors.Is(joined, plain), "Is through Join forest")
		ck(!errors.Is(wrapped, &plainErr{msg: "other"}), "Is distinct plain error false")
		ck(errors.Is(codeVal, &codeErr{Code: 42}), "custom Is matches code")
		ck(!errors.Is(codeVal, &codeErr{Code: 7}), "custom Is rejects wrong code")
		ck(errors.Is(codePtr, &codeErr{Code: 42}), "custom Is on pointer")
		ck(errors.Is(nil, nil) && !errors.Is(plain, nil), "Is nil semantics")
		ck(errors.Is(typedNil, (*plainErr)(nil)), "Is typed nil equals typed nil")
		ck(!errors.Is(codePtr, codeVal), "code value and pointer differ")

		// Unwrap 单链与 fork 链。
		ck(errors.Unwrap(wrapped) != nil, "Unwrap single chain")
		ck(errors.Unwrap(joined) == nil, "errors.Unwrap ignores Unwrap() []error")
		u, okU := joined.(interface{ Unwrap() []error })
		ck(okU && len(u.Unwrap()) == 3, "Join exposes three errors")
		fu, okF := iForest.(interface{ Unwrap() []error })
		ck(okF && len(fu.Unwrap()) == 2, "forest exposes two errors")
		ck(reflect.TypeOf(joined).Implements(reflect.TypeOf((*interface{ Unwrap() []error })(nil)).Elem()), "Join implements forest unwrap")

		// panic 语义：target 必须是非 nil 指针且指向 error 或接口。
		func() {
			defer func() { ck(recover() != nil, "As non-pointer target panics") }()
			errors.As(wrapped, plain)
		}()
		func() {
			defer func() { ck(recover() != nil, "As nil target pointer panics") }()
			var p *plainErr
			errors.As(wrapped, p)
		}()
		func() {
			defer func() { ck(recover() != nil, "As pointer to non-error panics") }()
			var x int
			errors.As(wrapped, &x)
		}()

		// error 接口本身的方法集/实现关系。
		tErr := reflect.TypeOf((*error)(nil)).Elem()
		ck(tErr.NumMethod() == 1 && tErr.Method(0).Name == "Error", "error interface shape")
		ck(reflect.TypeOf(plain).Implements(tErr) && reflect.TypeOf(codeVal).Implements(tErr), "error implementers")
		ck(!reflect.TypeOf(node{}).Implements(tErr), "node does not implement error")
		ck(reflect.TypeOf(tmo).Implements(reflect.TypeOf((*interface{ Timeout() bool })(nil)).Elem()), "timeout implementer")

		gcStress()
		var tb *boxErr
		ck(errors.As(anySink.([]error)[3], &tb) && tb != nil && tb.tag != nil, "box target after GC")
		checkAs(wrapped, "wrapped-after-GC", targetMask{true, false, false, false, false, false})
		checkAs(boxed, "boxed-after-GC", targetMask{true, true, true, true, true, false})
		ck(plain.tag.V == base+1 && plain.tag.Label == "p", "error payload survives GC")
		ck(codePtr.tag.V == base+3 && tmo.tag.V == base+4, "wrapped pointers survive GC")
		ck(errors.Is(anySink.([]error)[1], plain), "Is after GC")
	}
	finish("l06_error_assert_matrix")
}
