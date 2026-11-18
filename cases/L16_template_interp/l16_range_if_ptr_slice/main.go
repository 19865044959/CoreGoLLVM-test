// L16: text/template 在指针切片上 range/if/with/else/break/continue；nil 指针语义与数据跨 GC 稳定。
package main

import (
	"fmt"
	"math/rand"
	"os"
	"runtime"
	"strconv"
	"strings"
	"sync/atomic"
	"text/template"
)

type leaf struct {
	Label string
	Owner *row
}

type row struct {
	Name  string
	Score *int
	Leaf  *leaf
	Skip  bool
}

var (
	failed  int32
	gcSink  [][]byte
	live    []*row
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
	// 栈上的指针垃圾会迫使本轮 GC 扫描根集与堆对象图。
	trash := make([]*row, 128)
	for i := range trash {
		trash[i] = &row{Name: "trash", Score: new(int), Leaf: &leaf{Label: "t"}}
		trash[i].Leaf.Owner = trash[i]
	}
	gcSink = gcSink[:0]
	runtime.GC()
	runtime.GC()
	_ = trash
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

type data struct {
	Title string
	Rows  []*row
	Tags  []*leaf
}

var snippets = []struct {
	name string
	src  string
	want string
}{
	{
		name: "cond",
		src:  `{{range $i, $r := .Rows}}({{$i}}:{{if $r}}{{$r.Name}}{{if $r.Score}}={{$r.Score}}{{else}}=-{{end}}{{else}}NIL{{end}}){{end}}`,
		want: `(0:a=10)(1:NIL)(2:b=-)(3:c=30)(4:d=40)`,
	},
	{
		name: "with",
		src:  `{{range .Rows}}{{if .}}{{with .Leaf}}[{{.Label}}/{{$.Title}}/{{.Owner.Name}}]{{else}}[leaf=nil]{{end}}{{end}}{{end}}`,
		want: `[L1/root/a][leaf=nil][L3/root/c][L4/root/d]`,
	},
	{
		name: "continue",
		src:  `{{range .Rows}}{{if .}}{{if not .Score}}{{continue}}{{end}}{{.Name}}{{end}}{{end}}`,
		want: `acd`,
	},
	{
		name: "break",
		src:  `{{range .Rows}}{{if .}}{{if .Skip}}{{break}}{{end}}{{.Name}}{{end}}{{end}}`,
		want: `abc`,
	},
	{
		name: "else",
		src:  `{{range .Tags}}{{.Label}}@{{.Owner.Name}},{{else}}no-tags{{end}}|{{range .Rows}}{{if .}}{{.Name}},{{else}}NIL,{{end}}{{else}}no-rows{{end}}`,
		want: `no-tags|a,NIL,b,c,d,`,
	},
	{
		name: "ifelse",
		src:  `{{range .Rows}}{{if and . .Leaf}}{{.Leaf.Label}}{{else if .}}{{.Name}}-plain{{else}}nil{{end}};{{end}}`,
		want: `L1;nil;b-plain;L3;L4;`,
	},
}

func newData() (data, *row, *row) {
	s10, s30, s40 := 10, 30, 40
	r0 := &row{Name: "a", Score: &s10, Leaf: &leaf{Label: "L1"}}
	r0.Leaf.Owner = r0
	r2 := &row{Name: "b"}
	r3 := &row{Name: "c", Score: &s30, Leaf: &leaf{Label: "L3"}}
	r3.Leaf.Owner = r3
	r4 := &row{Name: "d", Score: &s40, Leaf: &leaf{Label: "L4"}, Skip: true}
	r4.Leaf.Owner = r4
	rows := []*row{r0, nil, r2, r3, r4}
	// 把分数与叶子放在独立分配的路径上，让模板执行时经历多级指针。
	return data{Title: "root", Rows: rows, Tags: nil}, r0, r3
}

func main() {
	uafSeed = envSeed()
	rounds := 5 + int(uafSeed%4)

	// 解析顺序受种子控制，执行结果必须与顺序无关。
	tpls := make(map[string]*template.Template, len(snippets))
	rng := rand.New(rand.NewSource(uafSeed))
	for _, idx := range rng.Perm(len(snippets)) {
		s := snippets[idx]
		t, err := template.New(s.name).Parse(s.src)
		ck(err == nil, fmt.Sprintf("parse %s: %v", s.name, err))
		tpls[s.name] = t
	}

	for r := 0; r < rounds; r++ {
		d, r0, r3 := newData()
		live = append(live[:0], d.Rows[0], d.Rows[2], d.Rows[3], d.Rows[4])
		ck(d.Rows[1] == nil, fmt.Sprintf("round %d expected nil row", r))

		for _, op := range rng.Perm(len(snippets)) {
			s := snippets[op]
			t := tpls[s.name]
			if t == nil {
				continue
			}
			var sb strings.Builder
			err := t.Execute(&sb, d)
			ck(err == nil, fmt.Sprintf("round %d execute %s: %v", r, s.name, err))
			ck(sb.String() == s.want, fmt.Sprintf("round %d %s\n got: %q\nwant: %q", r, s.name, sb.String(), s.want))
		}
		ck(r0.Score != nil && *r0.Score == 10, fmt.Sprintf("round %d r0 score after execute", r))
		ck(r0.Leaf != nil && r0.Leaf.Owner == r0, fmt.Sprintf("round %d leaf owner identity", r))

		gcStress()

		ck(d.Rows[0] != nil && d.Rows[0].Name == "a", fmt.Sprintf("round %d row0 after GC", r))
		ck(d.Rows[1] == nil, fmt.Sprintf("round %d nil row corrupted after GC", r))
		ck(d.Rows[0].Score != nil && *d.Rows[0].Score == 10, fmt.Sprintf("round %d score pointer after GC", r))
		ck(d.Rows[2].Score == nil, fmt.Sprintf("round %d nil score pointer after GC", r))
		ck(r0.Leaf != nil && r0.Leaf.Owner == r0 && r0.Leaf.Label == "L1",
			fmt.Sprintf("round %d leaf chain after GC", r))
		ck(r3.Leaf != nil && r3.Leaf.Owner == r3, fmt.Sprintf("round %d second leaf chain after GC", r))

		for _, op := range rng.Perm(len(snippets)) {
			s := snippets[op]
			t := tpls[s.name]
			if t == nil {
				continue
			}
			var sb strings.Builder
			err := t.Execute(&sb, d)
			ck(err == nil, fmt.Sprintf("round %d execute %s after GC: %v", r, s.name, err))
			ck(sb.String() == s.want, fmt.Sprintf("round %d %s after GC\n got: %q\nwant: %q", r, s.name, sb.String(), s.want))
		}
	}

	finish("l16_range_if_ptr_slice")
}
