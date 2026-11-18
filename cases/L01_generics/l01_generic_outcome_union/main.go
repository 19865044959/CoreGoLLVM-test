// L01: 泛型结果 union（四态标签联合）：OK/RETRY/SKIP/FAIL 的取值不变量、
// Map/Fold 泛型函数与重试转换，跨 GC 校验负载指针。
// 与 l01_error_result_union 的二态 error union 不同，本例覆盖四态标签与终态语义。
package main

import (
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

type outcomeTag uint8

const (
	outOK outcomeTag = iota + 1
	outRetry
	outSkip
	outFail
)

type outcome[T any] struct {
	tag     outcomeTag
	val     *T
	attempt int
	reason  string
	code    int
}

func okOut[T any](v *T) outcome[T] { return outcome[T]{tag: outOK, val: v} }

func retryOut[T any](v *T, attempt int) outcome[T] {
	return outcome[T]{tag: outRetry, val: v, attempt: attempt}
}

func skipOut[T any](reason string) outcome[T] { return outcome[T]{tag: outSkip, reason: reason} }

func failOut[T any](code int) outcome[T] { return outcome[T]{tag: outFail, code: code} }

func (o outcome[T]) Tag() outcomeTag { return o.tag }

// Value 只对 OK 暴露负载；SKIP/FAIL/RETRY 不得伪装成成功。
func (o outcome[T]) Value() (*T, bool) {
	if o.tag != outOK {
		return nil, false
	}
	return o.val, true
}

func (o outcome[T]) Pending() (*T, bool) {
	if o.tag != outRetry {
		return nil, false
	}
	return o.val, true
}

func (o outcome[T]) IsTerminal() bool {
	return o.tag == outOK || o.tag == outSkip || o.tag == outFail
}

// MapOutcome 是泛型函数而非泛型方法：Go 方法不能再带类型参数。
func MapOutcome[T, U any](o outcome[T], f func(*T) *U) outcome[U] {
	res := outcome[U]{tag: o.tag, attempt: o.attempt, reason: o.reason, code: o.code}
	if o.val != nil {
		res.val = f(o.val)
	}
	return res
}

func FoldOutcome[T, R any](o outcome[T],
	onOK func(*T) R,
	onRetry func(*T, int) R,
	onSkip func(string) R,
	onFail func(int) R) R {
	switch o.tag {
	case outOK:
		return onOK(o.val)
	case outRetry:
		return onRetry(o.val, o.attempt)
	case outSkip:
		return onSkip(o.reason)
	default:
		return onFail(o.code)
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
	n := 220 + int(uafSeed%41)

	cells := make([]*pcell, n)
	for i := range cells {
		cells[i] = &pcell{
			id:   int64(i),
			name: fmt.Sprintf("u%d", i),
			blob: make([]byte, 6+i%7),
		}
		cells[i].blob[0] = byte(i)
		cells[i].blob[len(cells[i].blob)-1] = byte(i * 3)
	}
	cellSink = cells

	outs := make([]outcome[pcell], n)
	for i := 0; i < n; i++ {
		switch i % 7 {
		case 3:
			outs[i] = failOut[pcell](500 + i%5)
		case 5:
			outs[i] = skipOut[pcell]("throttled")
		case 6:
			outs[i] = retryOut[pcell](cells[i], 1+i%3)
		default:
			outs[i] = okOut[pcell](cells[i])
		}
	}
	gcStress()

	// 逐项校验标签与取值不变量，并独立复算四态计数。
	var wantOK, wantRetry, wantSkip, wantFail int64
	var wantIDSum int64
	for i := range outs {
		o := outs[i]
		switch o.Tag() {
		case outOK:
			v, ok := o.Value()
			ck(ok && v == cells[i], fmt.Sprintf("ok outcome %d lost value", i))
			if v != nil {
				ck(v.id == int64(i) && v.name == fmt.Sprintf("u%d", i), "ok outcome payload corrupted")
				ck(v.blob[0] == byte(i), "ok outcome blob corrupted")
			}
			_, pending := o.Pending()
			ck(!pending, "ok outcome reported pending")
			ck(o.IsTerminal(), "ok outcome not terminal")
			wantOK++
			wantIDSum += o.val.id
		case outRetry:
			v, ok := o.Pending()
			ck(ok && v == cells[i], fmt.Sprintf("retry outcome %d lost value", i))
			_, direct := o.Value()
			ck(!direct, "retry outcome exposed Value()")
			ck(!o.IsTerminal(), "retry outcome incorrectly terminal")
			ck(o.attempt == 1+i%3, "retry attempt corrupted")
			wantRetry++
		case outSkip:
			_, ok := o.Value()
			ck(!ok, "skip outcome exposed Value()")
			ck(o.reason == "throttled", "skip reason corrupted")
			ck(o.IsTerminal(), "skip outcome not terminal")
			wantSkip++
		case outFail:
			_, ok := o.Value()
			ck(!ok, "fail outcome exposed Value()")
			ck(o.code == 500+i%5, "fail code corrupted")
			ck(o.IsTerminal(), "fail outcome not terminal")
			wantFail++
		default:
			ck(false, "unknown outcome tag")
		}
	}
	ck(wantOK+wantRetry+wantSkip+wantFail == int64(n), "outcome counts do not add up")

	// MapOutcome：标签与元数据保留，负载换成 string；SKIP/FAIL 不调用映射函数。
	mapped := make([]outcome[string], n)
	calls := 0
	for i := range outs {
		mapped[i] = MapOutcome(outs[i], func(p *pcell) *string {
			calls++
			s := fmt.Sprintf("m%d", p.id)
			return &s
		})
	}
	ck(calls == int(wantOK+wantRetry), fmt.Sprintf("MapOutcome calls got=%d want=%d", calls, wantOK+wantRetry))
	gcStress()
	for i := range mapped {
		ck(mapped[i].Tag() == outs[i].Tag(), "MapOutcome changed tag")
		if mapped[i].Tag() == outOK || mapped[i].Tag() == outRetry {
			ck(mapped[i].val != nil && *mapped[i].val == fmt.Sprintf("m%d", i), "MapOutcome value corrupted")
		} else {
			ck(mapped[i].val == nil, "MapOutcome produced value for skip/fail")
		}
	}

	// FoldOutcome：四分支都参与，结果与独立计数一致。
	var okN, retryN, skipN, failN int64
	var foldSum int64
	for i := range outs {
		foldSum += FoldOutcome(outs[i],
			func(p *pcell) int64 { okN++; return p.id },
			func(p *pcell, attempt int) int64 { retryN++; return int64(attempt) * 1000 },
			func(string) int64 { skipN++; return 1 },
			func(int) int64 { failN++; return 2 },
		)
	}
	ck(okN == wantOK && retryN == wantRetry && skipN == wantSkip && failN == wantFail, "fold branch counts mismatch")
	var wantFold int64 = wantIDSum + wantSkip + 2*wantFail
	for i := range outs {
		if outs[i].Tag() == outRetry {
			wantFold += int64(outs[i].attempt) * 1000
		}
	}
	ck(foldSum == wantFold, fmt.Sprintf("fold sum got=%d want=%d", foldSum, wantFold))

	// 重试收敛：attempt<2 的 RETRY 转成 OK（复用同一负载指针），其余保持。
	resolved := make([]outcome[pcell], n)
	copy(resolved, outs)
	converted := 0
	for i := range resolved {
		if resolved[i].Tag() != outRetry {
			continue
		}
		if resolved[i].attempt < 2 {
			resolved[i] = okOut[pcell](cells[i])
			converted++
		}
	}
	gcStress()
	seenOK := 0
	for i := range resolved {
		switch resolved[i].Tag() {
		case outOK:
			v, ok := resolved[i].Value()
			ck(ok && v == cells[i], fmt.Sprintf("resolved ok %d lost pointer", i))
			seenOK++
		case outRetry:
			v, ok := resolved[i].Pending()
			ck(ok && v == cells[i] && resolved[i].attempt >= 2, "unresolved retry corrupted")
		}
	}
	ck(seenOK == int(wantOK)+converted, fmt.Sprintf("resolved ok count got=%d want=%d", seenOK, int(wantOK)+converted))
	ck(converted > 0, "no retry outcome converged")

	// 第二次实例化 outcome[int64]：值型负载与 fold 求和。
	ios := make([]outcome[int64], 30)
	for i := range ios {
		switch i % 4 {
		case 0:
			ios[i] = failOut[int64](700 + i)
		case 1:
			ios[i] = retryOut[int64](int64Ptr(int64(i*3)), 1)
		default:
			ios[i] = okOut[int64](int64Ptr(int64(i * 5)))
		}
	}
	gcStress()
	var intSum int64
	for i := range ios {
		intSum += FoldOutcome(ios[i],
			func(p *int64) int64 { return *p },
			func(p *int64, attempt int) int64 { return *p + int64(attempt) },
			func(string) int64 { return 0 },
			func(int) int64 { return 0 },
		)
	}
	var wantInt int64
	for i := 0; i < 30; i++ {
		switch i % 4 {
		case 0:
		case 1:
			wantInt += int64(i*3) + 1
		default:
			wantInt += int64(i * 5)
		}
	}
	ck(intSum == wantInt, fmt.Sprintf("int64 outcome fold got=%d want=%d", intSum, wantInt))

	finish("l01_generic_outcome_union")
}

func int64Ptr(v int64) *int64 { return &v }
