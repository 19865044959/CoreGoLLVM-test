// L16: 嵌套 define/template + block 覆盖 + 数据方法（指针接收者/返回指针/返回 error），跨 GC。
package main

import (
	"errors"
	"fmt"
	"math/rand"
	"os"
	"runtime"
	"strconv"
	"strings"
	"sync/atomic"
	"text/template"
)

type item struct {
	Name   string
	N      int
	Parent *item
	Kids   []*item
}

func (i *item) Label() string {
	if i == nil {
		return "nil"
	}
	return i.Name + "#" + strconv.Itoa(i.N)
}

func (i *item) Child(n int) *item {
	if i == nil || n < 0 || n >= len(i.Kids) {
		return nil
	}
	return i.Kids[n]
}

func (i *item) Find(name string) (*item, error) {
	if i == nil {
		return nil, nil
	}
	if i.Name == name {
		return i, nil
	}
	for _, k := range i.Kids {
		if f, _ := k.Find(name); f != nil {
			return f, nil
		}
	}
	return nil, nil
}

func (i *item) FindErr(name string) (*item, error) {
	return nil, errors.New("find-failed: " + name)
}

func (i *item) Boom() (string, error) {
	return "", errors.New("boom-method")
}

func (i *item) Scale(f int) int {
	i.N *= f
	return i.N
}

type rowData struct {
	Root  *item
	Kid   *item
	Index int
}

type page struct {
	Title string
	Root  *item
	Rows  []rowData
}

var defs = []string{
	`{{define "layout"}}<{{template "head" .}}|{{template "body" .}}|{{block "footer" .}}DEFAULT{{end}}>{{end}}`,
	`{{define "head"}}{{.Title}}/{{.Root.Label}}{{end}}`,
	`{{define "body"}}{{range $i, $r := .Rows}}{{$i}}:{{template "row" $r}};{{else}}none{{end}}{{end}}`,
	`{{define "row"}}{{.Root.Label}}>{{.Kid.Label}}@{{.Index}}/p:{{with .Kid.Parent}}{{.Label}}{{else}}none{{end}}{{end}}`,
}

const overrideFooter = `{{define "footer"}}X[{{.Root.Label}}/` +
	`{{with .Root.Find "a1"}}{{.Label}}{{else}}miss{{end}}/` +
	`{{with .Root.Child 1}}{{.Label}}{{else}}none{{end}}/` +
	`{{with .Root.Child 9}}{{.Label}}{{else}}none{{end}}]{{end}}`

const wantLayout = `<T/r#1|0:r#1>a#2@0/p:r#1;1:r#1>b#4@1/p:r#1;|X[r#1/a1#3/b#4/none]>`
const wantDefault = `<T/r#1|0:r#1>a#2@0/p:r#1;1:r#1>b#4@1/p:r#1;|DEFAULT>`

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
		_ = &item{Name: "trash", N: i, Parent: &item{Name: "p"}, Kids: []*item{{Name: "k"}}}
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

func tortureRounds(def int) int {
	s := os.Getenv("TORTURE_ROUNDS")
	if s == "" {
		return def
	}
	n, err := strconv.Atoi(s)
	if err != nil || n <= 0 {
		return def
	}
	if n > 200000 {
		n = 200000
	}
	return n
}

func build(rng *rand.Rand, overrideLast bool) *template.Template {
	t := template.New("layout")
	if !overrideLast {
		t = template.Must(t.Parse(overrideFooter))
	}
	for _, i := range rng.Perm(len(defs)) {
		t = template.Must(t.Parse(defs[i]))
	}
	if overrideLast {
		t = template.Must(t.Parse(overrideFooter))
	}
	return t
}

func newPage() (*item, page) {
	root := &item{Name: "r", N: 1}
	a := &item{Name: "a", N: 2, Parent: root}
	a1 := &item{Name: "a1", N: 3, Parent: a}
	a.Kids = []*item{a1}
	b := &item{Name: "b", N: 4, Parent: root}
	root.Kids = []*item{a, b}
	return root, page{
		Title: "T",
		Root:  root,
		Rows: []rowData{
			{Root: root, Kid: a, Index: 0},
			{Root: root, Kid: b, Index: 1},
		},
	}
}

func execute(t *template.Template, p page) (string, error) {
	var sb strings.Builder
	err := t.Execute(&sb, p)
	return sb.String(), err
}

func main() {
	uafSeed = envSeed()
	rounds := tortureRounds(20)
	rng := rand.New(rand.NewSource(uafSeed))

	for r := 0; r < rounds; r++ {
		t := build(rng, true)
		defT := build(rng, false)
		root, p := newPage()
		mut := &item{Name: "m", N: 1}
		live = []any{t, defT, root, root.Kids[0], root.Kids[1], root.Kids[0].Kids[0], mut}

		names := map[string]bool{}
		for _, tt := range t.Templates() {
			names[tt.Name()] = true
		}
		for _, want := range []string{"layout", "head", "body", "row", "footer"} {
			ck(names[want], fmt.Sprintf("round %d template %q missing", r, want))
		}

		out, err := execute(t, p)
		ck(err == nil, fmt.Sprintf("round %d execute layout: %v", r, err))
		ck(out == wantLayout, fmt.Sprintf("round %d layout\n got: %q\nwant: %q", r, out, wantLayout))

		out, err = execute(defT, p)
		ck(err == nil, fmt.Sprintf("round %d execute default footer: %v", r, err))
		ck(out == wantDefault, fmt.Sprintf("round %d default footer\n got: %q\nwant: %q", r, out, wantDefault))

		boom := template.Must(template.New("boom").Parse(`pre{{.Root.Boom}}post`))
		live = append(live, boom)
		var bb strings.Builder
		errBoom := boom.Execute(&bb, p)
		ck(errBoom != nil, fmt.Sprintf("round %d Boom returned nil error", r))
		if errBoom != nil {
			ck(strings.Contains(errBoom.Error(), "error calling Boom: boom-method"),
				fmt.Sprintf("round %d Boom error=%q", r, errBoom.Error()))
		}
		ck(bb.String() == "pre", fmt.Sprintf("round %d Boom wrote past error: %q", r, bb.String()))

		findErr := template.Must(template.New("findErr").Parse(`{{with .Root.FindErr "x"}}Y{{else}}N{{end}}`))
		live = append(live, findErr)
		var fb strings.Builder
		errFind := findErr.Execute(&fb, p)
		ck(errFind != nil && strings.Contains(errFind.Error(), "find-failed: x"),
			fmt.Sprintf("round %d FindErr error=%v", r, errFind))

		scale := template.Must(template.New("scale").Parse(`{{.Scale 3}}|{{.Label}}`))
		live = append(live, scale)
		var sb strings.Builder
		errScale := scale.Execute(&sb, mut)
		ck(errScale == nil, fmt.Sprintf("round %d Scale execute: %v", r, errScale))
		ck(sb.String() == "3|m#3", fmt.Sprintf("round %d Scale first=%q", r, sb.String()))
		sb.Reset()
		errScale = scale.Execute(&sb, mut)
		ck(errScale == nil, fmt.Sprintf("round %d Scale second execute: %v", r, errScale))
		ck(sb.String() == "9|m#9", fmt.Sprintf("round %d Scale second=%q", r, sb.String()))

		gcStress()

		ck(root.Kids[0].Parent == root && root.Kids[0].Kids[0].Parent == root.Kids[0],
			fmt.Sprintf("round %d parent pointers after GC", r))
		ck(root.Child(1) == root.Kids[1] && root.Child(9) == nil,
			fmt.Sprintf("round %d Child method after GC", r))
		if f, err := root.Find("a1"); err != nil || f != root.Kids[0].Kids[0] {
			ck(false, fmt.Sprintf("round %d Find after GC=(%v,%v)", r, f, err))
		}
		out, err = execute(t, p)
		ck(err == nil, fmt.Sprintf("round %d layout after GC: %v", r, err))
		ck(out == wantLayout, fmt.Sprintf("round %d layout after GC\n got: %q\nwant: %q", r, out, wantLayout))
		var bb2 strings.Builder
		errBoom2 := boom.Execute(&bb2, p)
		ck(errBoom2 != nil && errBoom != nil && errBoom2.Error() == errBoom.Error(),
			fmt.Sprintf("round %d Boom error changed after GC", r))
		sb.Reset()
		errScale = scale.Execute(&sb, mut)
		ck(errScale == nil && sb.String() == "27|m#27",
			fmt.Sprintf("round %d Scale after GC=(%q,%v)", r, sb.String(), errScale))
	}

	finish("l16_nested_method_block")
}
