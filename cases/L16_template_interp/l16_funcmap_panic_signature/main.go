// L16: FuncMap 的 panic 包装、非法签名注册、nil 函数与调用参数错误；错误文本跨 GC 稳定。
package main

import (
	"errors"
	"fmt"
	"os"
	"runtime"
	"strconv"
	"strings"
	"sync/atomic"
	"text/template"
)

var (
	failed  int32
	gcSink  [][]byte
	live    []any
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
	for i := 0; i < 64; i++ {
		fn := func(x int) int { return x + i }
		_ = fn
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

func add(a, b int) int { return a + b }

func safeDiv(a, b int) (int, error) {
	if b == 0 {
		return 0, errors.New("div-zero")
	}
	return a / b, nil
}

func sum(vs ...int) (int, error) {
	total := 0
	for _, v := range vs {
		total += v
	}
	return total, nil
}

func boomString() string { panic("kaboom-fn") }

func boomError() (int, error) { panic(errors.New("boom-err")) }

var nilFn func(string) string

func catchPanic(fn func()) (msg string, panicked bool) {
	defer func() {
		if r := recover(); r != nil {
			panicked = true
			if e, ok := r.(error); ok {
				msg = e.Error()
			} else {
				msg = fmt.Sprint(r)
			}
		}
	}()
	fn()
	return "", false
}

func funcMap() template.FuncMap {
	return template.FuncMap{
		"add":        add,
		"safeDiv":    safeDiv,
		"sum":        sum,
		"boomString": boomString,
		"boomError":  boomError,
		"nilFn":      nilFn,
	}
}

func render(t *template.Template) (string, error) {
	var sb strings.Builder
	err := t.Execute(&sb, nil)
	return sb.String(), err
}

func main() {
	uafSeed = envSeed()
	fm := funcMap()

	ok := template.Must(template.New("ok").Funcs(fm).Parse(`{{add 2 3}}|{{safeDiv 9 3}}|{{sum 1 2 3}}`))
	divErr := template.Must(template.New("divErr").Funcs(fm).Parse(`pre{{safeDiv 1 0}}post`))
	panicStr := template.Must(template.New("panicStr").Funcs(fm).Parse(`pre{{boomString}}post`))
	panicErr := template.Must(template.New("panicErr").Funcs(fm).Parse(`pre{{boomError}}post`))
	nilCall := template.Must(template.New("nilCall").Funcs(fm).Parse(`pre{{nilFn "x"}}post`))
	arity := template.Must(template.New("arity").Funcs(fm).Parse(`{{add 1}}`))
	extra := template.Must(template.New("extra").Funcs(fm).Parse(`{{add 1 2 3}}`))
	badArg := template.Must(template.New("badArg").Funcs(fm).Parse(`{{add "s" 2}}`))
	badVar := template.Must(template.New("badVar").Funcs(fm).Parse(`{{sum 1 "x" 3}}`))
	live = append(live, ok, divErr, panicStr, panicErr, nilCall, arity, extra, badArg, badVar, fm)

	out, err := render(ok)
	ck(err == nil, fmt.Sprintf("ok execute: %v", err))
	ck(out == "5|3|6", fmt.Sprintf("ok output=%q want %q", out, "5|3|6"))

	type errCase struct {
		name string
		t    *template.Template
		want string
		out  string
	}
	errCases := []errCase{
		{"divErr", divErr, "error calling safeDiv: div-zero", "pre"},
		{"panicStr", panicStr, "error calling boomString: kaboom-fn", "pre"},
		{"panicErr", panicErr, "error calling boomError: boom-err", "pre"},
		{"nilCall", nilCall, "call of nil function", "pre"},
		{"arity", arity, "wrong number of args for add: want 2 got 1", ""},
		{"extra", extra, "wrong number of args for add: want 2 got 3", ""},
		{"badArg", badArg, `expected integer; found "s"`, ""},
		{"badVar", badVar, `expected integer; found "x"`, ""},
	}
	firstErr := make(map[string]string, len(errCases))
	for _, ec := range errCases {
		got, e := render(ec.t)
		ck(e != nil, fmt.Sprintf("%s: expected error, got nil (out=%q)", ec.name, got))
		if e != nil {
			ck(strings.Contains(e.Error(), ec.want),
				fmt.Sprintf("%s: error=%q missing %q", ec.name, e.Error(), ec.want))
			firstErr[ec.name] = e.Error()
		}
		ck(got == ec.out, fmt.Sprintf("%s: wrote output %q want %q", ec.name, got, ec.out))
	}

	// 非法函数签名/非函数值在 Funcs 注册阶段 panic。
	registrations := []struct {
		name string
		fm   template.FuncMap
		want string
	}{
		{"badret", template.FuncMap{"badret": func() (int, string) { return 1, "s" }},
			"invalid function signature for badret: second return value should be error; is string"},
		{"toomany", template.FuncMap{"toomany": func() (int, error, error) { return 1, nil, nil }},
			"function toomany has 3 return values; should be 1 or 2"},
		{"none", template.FuncMap{"none": func() {}},
			"function none has 0 return values; should be 1 or 2"},
		{"notfunc", template.FuncMap{"notfunc": 42},
			"value for notfunc not a function"},
		{"nilmap", template.FuncMap{"nilmap": template.FuncMap{}},
			"value for nilmap not a function"},
	}
	for _, rc := range registrations {
		msg, panicked := catchPanic(func() { template.New("reg").Funcs(rc.fm) })
		ck(panicked, fmt.Sprintf("%s: Funcs did not panic", rc.name))
		ck(strings.Contains(msg, rc.want), fmt.Sprintf("%s: panic=%q missing %q", rc.name, msg, rc.want))
	}

	// 失败的 Funcs 不能破坏已有模板或克隆。
	base := template.Must(template.New("base").Funcs(template.FuncMap{"one": func() int { return 1 }}).Parse(`{{one}}`))
	_, panicked := catchPanic(func() {
		base.Funcs(template.FuncMap{"bad": func() (int, string) { return 0, "" }})
	})
	ck(panicked, "late bad Funcs did not panic")
	out, err = render(base)
	ck(err == nil && out == "1", fmt.Sprintf("base after failed Funcs=(%q,%v)", out, err))
	clone := template.Must(base.Clone())
	out, err = render(clone)
	ck(err == nil && out == "1", fmt.Sprintf("clone after failed Funcs=(%q,%v)", out, err))

	rounds := 8 + int(uafSeed%5)
	for r := 0; r < rounds; r++ {
		out, err := render(ok)
		ck(err == nil && out == "5|3|6", fmt.Sprintf("round %d ok=(%q,%v)", r, out, err))

		for _, ec := range errCases {
			got, e := render(ec.t)
			ck(e != nil, fmt.Sprintf("round %d %s: expected error", r, ec.name))
			ck(got == ec.out, fmt.Sprintf("round %d %s output=%q want %q", r, ec.name, got, ec.out))
			if e != nil {
				ck(e.Error() == firstErr[ec.name], fmt.Sprintf("round %d %s error changed: %q", r, ec.name, e.Error()))
			}
		}

		if got := add(20, 22); got != 42 {
			ck(false, fmt.Sprintf("round %d add=%d", r, got))
		}
		if v, e := safeDiv(9, 3); e != nil || v != 3 {
			ck(false, fmt.Sprintf("round %d safeDiv=(%d,%v)", r, v, e))
		}
		if v, e := sum(1, 2, 3); e != nil || v != 6 {
			ck(false, fmt.Sprintf("round %d sum=(%d,%v)", r, v, e))
		}
		if _, e := safeDiv(1, 0); e == nil || e.Error() != "div-zero" {
			ck(false, fmt.Sprintf("round %d safeDiv err=%v", r, e))
		}

		gcStress()

		for _, ec := range errCases {
			got, e := render(ec.t)
			ck(e != nil && e.Error() == firstErr[ec.name],
				fmt.Sprintf("round %d %s error after GC=%v", r, ec.name, e))
			ck(got == ec.out, fmt.Sprintf("round %d %s output after GC=%q", r, ec.name, got))
		}
		out, err = render(ok)
		ck(err == nil && out == "5|3|6", fmt.Sprintf("round %d ok after GC=(%q,%v)", r, out, err))
		out, err = render(base)
		ck(err == nil && out == "1", fmt.Sprintf("round %d base after GC=(%q,%v)", r, out, err))
	}

	finish("l16_funcmap_panic_signature")
}
