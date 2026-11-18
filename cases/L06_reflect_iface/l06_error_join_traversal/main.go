// L06: error unwrap 组合：嵌套 Join 的 Is/As 深度优先顺序、自定义 Is/As 短路回退、
// 接口 As target、多 %w 森林、nil 分支与 GC 后的 payload。
package main

import (
	"errors"
	"fmt"
	"os"
	"runtime"
	"strconv"
	"strings"
	"sync/atomic"
)

type node struct {
	V     int64
	Label string
}

type leafErr struct {
	name string
	Tag  *node
}

func (e *leafErr) Error() string { return e.name }

var order []string

type probeErr struct{ name string }

func (p *probeErr) Error() string { return p.name }
func (p *probeErr) Is(target error) bool {
	order = append(order, "is:"+p.name)
	return false
}
func (p *probeErr) As(target any) bool {
	order = append(order, "as:"+p.name)
	return false
}

type dualErr struct {
	name  string
	child error
	tag   *node
}

func (d *dualErr) Error() string { return d.name }
func (d *dualErr) Is(target error) bool {
	order = append(order, "is:"+d.name)
	return false
}
func (d *dualErr) As(target any) bool {
	order = append(order, "as:"+d.name)
	if t, ok := target.(**leafErr); ok {
		*t = &leafErr{name: "synth-" + d.name, Tag: d.tag}
		return true
	}
	return false
}
func (d *dualErr) Unwrap() error { return d.child }

type fallbackAs struct {
	name  string
	child error
}

func (f *fallbackAs) Error() string { return f.name }
func (f *fallbackAs) As(target any) bool {
	order = append(order, "as:"+f.name)
	return false
}
func (f *fallbackAs) Unwrap() error { return f.child }

type forestErr struct{ errs []error }

func (f *forestErr) Error() string { return "forest" }
func (f *forestErr) Unwrap() []error {
	return f.errs
}

type timeoutErr struct {
	Tag *node
}

func (t *timeoutErr) Error() string { return "timeout" }
func (t *timeoutErr) Timeout() bool { return true }

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

func trace() string { return strings.Join(order, ",") }

func main() {
	uafSeed = envSeed()
	base := uafSeed * 10

	pa := &probeErr{name: "a"}
	pb := &probeErr{name: "b"}
	pc := &probeErr{name: "c"}
	root := errors.Join(errors.Join(pa, pb), pc)

	ck(root.Error() == "a\nb\nc", "nested Join error text")
	ck(errors.Unwrap(root) == nil, "Join has no single Unwrap")
	if u, ok := root.(interface{ Unwrap() []error }); ok {
		ck(len(u.Unwrap()) == 2, "outer Join has two children")
		if len(u.Unwrap()) == 2 {
			if inner, ok := u.Unwrap()[0].(interface{ Unwrap() []error }); ok {
				ck(len(inner.Unwrap()) == 2, "inner Join has two children")
			} else {
				ck(false, "inner Join exposes Unwrap []error")
			}
		}
	} else {
		ck(false, "Join exposes Unwrap []error")
	}

	// As 深度优先、先 As 后 Unwrap；所有 probe 返回 false 时顺序固定。
	order = nil
	var le *leafErr
	ok := errors.As(root, &le)
	ck(!ok && le == nil, "As misses when all probes decline")
	ck(trace() == "as:a,as:b,as:c", "As traversal is depth-first left-to-right")

	order = nil
	ck(!errors.Is(root, &leafErr{name: "target"}), "Is misses throughout Join tree")
	ck(trace() == "is:a,is:b,is:c", "Is traversal is depth-first left-to-right")

	// 接口 target 在根节点直接命中 Join 森林。
	var forest interface{ Unwrap() []error }
	ck(errors.As(root, &forest) && len(forest.Unwrap()) == 2, "As interface target matches Join")
	var single interface{ Unwrap() error }
	ck(!errors.As(root, &single), "As single-Unwrap interface misses Join")

	// 自定义 As 返回 false 后继续沿 Unwrap 下探。
	child := &leafErr{name: "child", Tag: &node{V: base + 1, Label: "fallback"}}
	fa := &fallbackAs{name: "wrap", child: child}
	order = nil
	le = nil
	ok = errors.As(fa, &le)
	ck(ok && le == child, "custom As false falls back to child")
	ck(trace() == "as:wrap", "fallback As visited once")

	// 同一 error 同时实现 Is/As：As 命中即短路，不再下探 child。
	d := &dualErr{name: "dual", child: &probeErr{name: "dualchild"}, tag: &node{V: base + 2, Label: "dual"}}
	order = nil
	le = nil
	ok = errors.As(d, &le)
	ck(ok && le != nil && le.name == "synth-dual", "dual error custom As synthesizes target")
	ck(le != nil && le.Tag != nil && le.Tag.V == base+2, "dual As payload")
	ck(trace() == "as:dual", "custom As true short-circuits child traversal")
	order = nil
	ck(!errors.Is(d, &leafErr{name: "none"}), "dual Is does not match")
	ck(trace() == "is:dual,is:dualchild", "Is calls custom Is then unwraps to child")

	// nil 分支不破坏森林遍历。
	lf := &leafErr{name: "leaf-f", Tag: &node{V: base + 3}}
	f := &forestErr{errs: []error{nil, lf, nil}}
	ck(errors.Is(f, lf), "forest contains leaf through nil branches")
	le = nil
	ck(errors.As(f, &le) && le == lf, "forest As skips nil branches")
	ck(f.Error() == "forest", "forest error text")

	// 接口方法 target（Timeout）与多 %w 森林。
	te := &timeoutErr{Tag: &node{V: base + 4, Label: "timeout"}}
	wrapped := fmt.Errorf("outer: %w", errors.Join(&leafErr{name: "leaf-w"}, te))
	var ti interface{ Timeout() bool }
	ck(errors.As(wrapped, &ti) && ti.Timeout(), "As interface Timeout target")
	multi := fmt.Errorf("multi: %w / %w", &leafErr{name: "first"}, &leafErr{name: "second"})
	ck(errors.Unwrap(multi) == nil, "multi %w has no single Unwrap")
	var many interface{ Unwrap() []error }
	ck(errors.As(multi, &many) && len(many.Unwrap()) == 2, "multi %w exposes two branches")
	var first *leafErr
	ck(errors.As(multi, &first) && first.name == "first", "multi %w traversal order")

	// 错误树只由接口槽保活，GC 后 payload 与遍历顺序不变。
	anySink = []any{root, d, f, wrapped, multi, lf}
	gcStress()
	root2 := anySink.([]any)[0].(error)
	order = nil
	le = nil
	ck(errors.As(root2, &le) == false, "As after GC still misses")
	ck(trace() == "as:a,as:b,as:c", "As order after GC")
	lf2 := anySink.([]any)[5].(*leafErr)
	ck(lf2.Tag != nil && lf2.Tag.V == base+3, "leaf payload after GC")
	var ti2 interface{ Timeout() bool }
	ck(errors.As(anySink.([]any)[3].(error), &ti2) && ti2.Timeout(), "Timeout target after GC")
	le = nil
	ck(errors.As(anySink.([]any)[1].(error), &le) && le.name == "synth-dual", "custom As after GC")
	ck(le != nil && le.Tag != nil && le.Tag.V == base+2, "custom As payload after GC")

	finish("l06_error_join_traversal")
}
