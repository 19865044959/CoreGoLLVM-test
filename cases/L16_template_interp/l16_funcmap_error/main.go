// L16: text/template FuncMap 中返回 (T, error) 的函数：成功值、错误中止、嵌套模板错误传播与 GC。
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

type item struct {
	Name string
	N    int
	Next *item
}

var (
	failed  int32
	gcSink  [][]byte
	live    []*item
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
		_ = &item{Name: "trash", N: i, Next: &item{Name: "n"}}
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

func div(a, b int) (int, error) {
	if b == 0 {
		return 0, errors.New("div: divide by zero")
	}
	return a / b, nil
}

func mk(name string, n int) (*item, error) {
	if name == "" {
		return nil, errors.New("mk: empty name")
	}
	if name == "nil" {
		return nil, nil
	}
	return &item{Name: name, N: n}, nil
}

func nameOf(it *item) (string, error) {
	if it == nil {
		return "", errors.New("name: nil item")
	}
	if it.Name == "" {
		return "", errors.New("name: blank item")
	}
	return it.Name, nil
}

func total(vs ...int) (int, error) {
	sum := 0
	for _, v := range vs {
		if v < 0 {
			return 0, errors.New("total: negative value")
		}
		sum += v
	}
	return sum, nil
}

type data struct {
	A, B    int
	Who     string
	N       int
	Nums    []int
	Item    *item
	NilItem *item
	Neg     []int
}

var errCases = []struct {
	name    string
	src     string
	wantErr string
	forbid  string
}{
	{"divzero", `pre{{div .A 0}}TAIL`, "div: divide by zero", "TAIL"},
	{"mkbad", `{{with mk "" 3}}X{{else}}Y{{end}}`, "mk: empty name", "X"},
	{"namenil", `A{{nameOf .NilItem}}B`, "name: nil item", "B"},
	{"totalneg", `{{total 1 -1 2}}`, "total: negative value", ""},
	{"nested", `{{define "sub"}}s{{div .A 0}}t{{end}}pre{{template "sub" .}}post`, "div: divide by zero", "post"},
}

func funcs() template.FuncMap {
	return template.FuncMap{
		"div":    div,
		"mk":     mk,
		"nameOf": nameOf,
		"total":  total,
	}
}

func main() {
	uafSeed = envSeed()
	fm := funcs()

	const okSrc = `{{div .A .B}}|{{with mk .Who .N}}{{.Name}}:{{.N}}{{else}}none{{end}}|{{total 1 2 3}}|{{nameOf .Item}}|{{div .A .B | printf "%03d"}}`
	tok, err := template.New("ok").Funcs(fm).Parse(okSrc)
	ck(err == nil, fmt.Sprintf("parse ok template: %v", err))
	if err != nil {
		finish("l16_funcmap_error")
		return
	}

	errTpls := make(map[string]*template.Template, len(errCases))
	for _, ec := range errCases {
		t, perr := template.New(ec.name).Funcs(fm).Parse(ec.src)
		ck(perr == nil, fmt.Sprintf("parse %s: %v", ec.name, perr))
		errTpls[ec.name] = t
	}

	rounds := 8 + int(uafSeed%5)
	for r := 0; r < rounds; r++ {
		it := &item{Name: "i", N: 9, Next: &item{Name: "tail", N: 1}}
		d := data{
			A: 10 + r, B: 2, Who: "x", N: 7 + r,
			Nums: []int{1, 2, 3}, Item: it, Neg: []int{1, -1, 2},
		}
		live = append(live[:0], it, it.Next)

		want := fmt.Sprintf("%d|x:%d|6|i|%03d", d.A/d.B, d.N, d.A/d.B)
		var sb strings.Builder
		err := tok.Execute(&sb, d)
		ck(err == nil, fmt.Sprintf("round %d execute ok: %v", r, err))
		ck(sb.String() == want, fmt.Sprintf("round %d ok output\n got: %q\nwant: %q", r, sb.String(), want))
		if v, err := div(d.A, d.B); err != nil || v != d.A/d.B {
			ck(false, fmt.Sprintf("round %d direct div=(%d,%v)", r, v, err))
		}
		if v, err := total(d.Nums...); err != nil || v != 6 {
			ck(false, fmt.Sprintf("round %d direct total=(%d,%v)", r, v, err))
		}

		for _, ec := range errCases {
			t := errTpls[ec.name]
			if t == nil {
				continue
			}
			var b strings.Builder
			e := t.Execute(&b, d)
			ck(e != nil, fmt.Sprintf("round %d %s: expected execute error, got nil (out=%q)", r, ec.name, b.String()))
			if e != nil {
				ck(strings.Contains(e.Error(), ec.wantErr),
					fmt.Sprintf("round %d %s error=%q missing %q", r, ec.name, e.Error(), ec.wantErr))
				ck(strings.Contains(e.Error(), "executing"),
					fmt.Sprintf("round %d %s error lacks execution context: %q", r, ec.name, e.Error()))
			}
			if ec.forbid != "" {
				ck(!strings.Contains(b.String(), ec.forbid),
					fmt.Sprintf("round %d %s executed past the error: %q", r, ec.name, b.String()))
			}
			// 错误文本必须在 GC 后逐字节稳定。
			var b2 strings.Builder
			e2 := t.Execute(&b2, d)
			ck(e2 != nil && e != nil && e2.Error() == e.Error(),
				fmt.Sprintf("round %d %s error not stable across executes", r, ec.name))
		}

		// 返回 (nil, nil) 的指针函数：with 走 else，直接打印为 <nil>。
		tNil := template.Must(template.New("nil").Funcs(fm).Parse(`{{with mk "nil" 1}}Y{{else}}none{{end}}|{{mk "nil" 1}}`))
		var nb strings.Builder
		err = tNil.Execute(&nb, d)
		ck(err == nil, fmt.Sprintf("round %d nil func execute: %v", r, err))
		ck(nb.String() == "none|<nil>", fmt.Sprintf("round %d nil func output=%q", r, nb.String()))

		gcStress()

		ck(it.Name == "i" && it.N == 9, fmt.Sprintf("round %d item after GC", r))
		ck(it.Next != nil && it.Next.Name == "tail", fmt.Sprintf("round %d item chain after GC", r))
		if v, e := nameOf(it); e != nil || v != "i" {
			ck(false, fmt.Sprintf("round %d nameOf after GC=(%q,%v)", r, v, e))
		}
		if p, e := mk("y", 4); e != nil || p == nil || p.Name != "y" || p.N != 4 {
			ck(false, fmt.Sprintf("round %d mk after GC=(%+v,%v)", r, p, e))
		}
		if _, e := div(1, 0); e == nil || e.Error() != "div: divide by zero" {
			ck(false, fmt.Sprintf("round %d div error after GC=%v", r, e))
		}

		sb.Reset()
		err = tok.Execute(&sb, d)
		ck(err == nil, fmt.Sprintf("round %d execute ok after GC: %v", r, err))
		ck(sb.String() == want, fmt.Sprintf("round %d ok output after GC\n got: %q\nwant: %q", r, sb.String(), want))
	}

	finish("l16_funcmap_error")
}
