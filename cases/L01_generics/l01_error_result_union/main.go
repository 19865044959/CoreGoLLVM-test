// L01: 泛型错误 union：result[T,E] 和类型（ok/err 二选一）+ 泛型错误分类，跨 GC 校验错误与负载指针。
package main

import (
	"errors"
	"fmt"
	"os"
	"runtime"
	"strconv"
	"sync/atomic"
)

type pcell struct {
	id   int64
	name string
	blob []byte
}

type code uint8

const (
	codeNotFound code = iota + 1
	codeTimeout
	codeCorrupt
)

var (
	errNotFound = &qerr{kind: codeNotFound, msg: "not found"}
	errTimeout  = &qerr{kind: codeTimeout, msg: "timeout"}
	errCorrupt  = &qerr{kind: codeCorrupt, msg: "corrupt"}
)

type qerr struct {
	kind   code
	msg    string
	detail *pcell
}

func (e *qerr) Error() string { return fmt.Sprintf("%s (code=%d)", e.msg, e.kind) }

// Is 让 errors.Is 能按错误类别（union 成员）匹配。
func (e *qerr) Is(target error) bool {
	t, ok := target.(*qerr)
	return ok && t.kind == e.kind
}

// result[T,E] 是 ok/err 的 union：ok=true 时只有 v 有意义，否则只有 e。
type result[T any, E error] struct {
	v  T
	e  E
	ok bool
}

func okRes[T any, E error](v T) result[T, E]  { return result[T, E]{v: v, ok: true} }
func errRes[T any, E error](e E) result[T, E] { return result[T, E]{e: e} }

func (r result[T, E]) IsOK() bool { return r.ok }

func (r result[T, E]) Val() (T, bool) {
	if !r.ok {
		var zero T
		return zero, false
	}
	return r.v, true
}

// Err 返回 error 视图；any 断言依赖 E 的 error 约束。
func (r result[T, E]) Err() (error, bool) {
	if r.ok {
		return nil, false
	}
	e, _ := any(r.e).(error)
	return e, true
}

// fold 同时使用 T/E/R 三个类型参数，验证多参数实例化。
func fold[T any, E error, R any](r result[T, E], onOK func(T) R, onErr func(error) R) R {
	if r.ok {
		return onOK(r.v)
	}
	e, _ := r.Err()
	return onErr(e)
}

// classify 是泛型错误分类器：union 成员 -> 枚举。
func classify[E error](e E) code {
	switch v := any(e).(type) {
	case *qerr:
		return v.kind
	default:
		return 0
	}
}

var (
	failed   int32
	gcSink   [][]byte
	cellSink []*pcell
	uafSeed  int64 = 1
)

func ck(ok bool, msg string) {
	if !ok {
		atomic.AddInt32(&failed, 1)
		fmt.Println("FAIL:", msg)
	}
}

func gcStress() {
	for i := 0; i < 8; i++ {
		b := make([]byte, 2048)
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
	n := 120 + int(uafSeed%16)

	cells := make([]*pcell, n)
	for i := range cells {
		cells[i] = &pcell{id: int64(i), name: fmt.Sprintf("c%d", i), blob: make([]byte, 9+i%4)}
		cells[i].blob[0] = byte(i)
		cells[i].blob[len(cells[i].blob)-1] = byte(i >> 1)
	}
	cellSink = cells

	res := make([]result[*pcell, *qerr], n)
	kinds := []*qerr{errNotFound, errTimeout, errCorrupt}
	for i := 0; i < n; i++ {
		if i%4 == 0 {
			res[i] = errRes[*pcell, *qerr](&qerr{
				kind:   kinds[(i/4)%len(kinds)].kind,
				msg:    kinds[(i/4)%len(kinds)].msg,
				detail: cells[i],
			})
		} else {
			res[i] = okRes[*pcell, *qerr](cells[i])
		}
	}
	gcStress()

	for i := 0; i < n; i++ {
		r := res[i]
		if i%4 != 0 {
			ck(r.IsOK(), fmt.Sprintf("result %d should be ok", i))
			v, ok := r.Val()
			ck(ok && v == cells[i], fmt.Sprintf("result %d value pointer identity changed", i))
			if v != nil {
				ck(v.id == int64(i) && v.name == fmt.Sprintf("c%d", i), "ok payload corrupted")
			}
			_, hasErr := r.Err()
			ck(!hasErr, "ok result exposed an error")
			continue
		}
		ck(!r.IsOK(), fmt.Sprintf("result %d should be err", i))
		_, ok := r.Val()
		ck(!ok, "err result exposed a value")
		e, hasErr := r.Err()
		ck(hasErr && e != nil, "err result lost its error after GC")
		qe, isQ := e.(*qerr)
		ck(isQ, "error dynamic type changed")
		if isQ {
			wantKind := kinds[(i/4)%len(kinds)].kind
			ck(qe.kind == wantKind, "error kind corrupted")
			ck(qe.detail == cells[i], "error detail pointer identity changed after GC")
			if qe.detail != nil {
				ck(qe.detail.id == int64(i) && qe.detail.name == fmt.Sprintf("c%d", i), "error detail payload corrupted")
			}
			ck(errors.Is(e, kinds[(i/4)%len(kinds)]), "errors.Is failed for matching kind sentinel")
			ck(!errors.Is(e, errCorrupt) || wantKind == codeCorrupt, "errors.Is matched wrong union member")
			var target *qerr
			ck(errors.As(e, &target) && target == qe, "errors.As did not recover qerr")
			ck(classify(qe) == wantKind, "classify returned wrong union member")
		}
	}

	// 错误包装链 + Join：union 成员在链上仍可识别。
	firstErr := res[0]
	baseErr, _ := firstErr.Err()
	wrapped1 := fmt.Errorf("layer1: %w", baseErr)
	wrapped2 := fmt.Errorf("layer2: %w", wrapped1)
	gcStress()
	ck(errors.Is(wrapped2, errNotFound), "errors.Is failed through wrap chain after GC")
	var target *qerr
	ck(errors.As(wrapped2, &target), "errors.As failed through wrap chain")
	if target != nil && firstErr.e != nil {
		ck(target == firstErr.e, "wrapped error pointer identity changed")
		ck(target.detail != nil && target.detail == cells[0], "wrapped error detail pointer changed")
	}
	joined := errors.Join(wrapped2, errCorrupt)
	ck(errors.Is(joined, errNotFound) && errors.Is(joined, errCorrupt), "errors.Join lost a union member")

	// 第二个实例化 result[int64,*qerr]：值类型 T 与错误 union 组合。
	ires := make([]result[int64, *qerr], 24)
	for i := range ires {
		if i%3 == 0 {
			ires[i] = errRes[int64, *qerr](&qerr{kind: codeTimeout, msg: "num timeout", detail: cells[i]})
		} else {
			ires[i] = okRes[int64, *qerr](int64(i * 17))
		}
	}
	gcStress()
	for i := range ires {
		if i%3 == 0 {
			e, ok := ires[i].Err()
			ck(ok && errors.Is(e, errTimeout), "int64 result lost error union member")
			qe := e.(*qerr)
			ck(qe.detail == cells[i], "int64 result error detail pointer changed")
		} else {
			v, ok := ires[i].Val()
			ck(ok && v == int64(i*17), "int64 result value corrupted")
		}
	}

	// fold 三类型参数：GC 后分别走 ok/err 分支。
	gotOK := fold(res[1], func(p *pcell) int64 { return p.id }, func(error) int64 { return -1 })
	ck(gotOK == 1, fmt.Sprintf("fold ok branch got=%d want=1", gotOK))
	gotErr := fold(res[0], func(*pcell) int64 { return -1 }, func(error) int64 { return int64(codeNotFound) * 100 })
	ck(gotErr == int64(codeNotFound)*100, "fold err branch returned wrong value")

	finish("l01_error_result_union")
}
