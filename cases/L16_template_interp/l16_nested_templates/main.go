// L16: define/template 嵌套、数据传递与定义注册顺序无关；解析结果跨 GC 保持可用。
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
}

type item struct {
	Name  string
	Score *int
}

type child struct {
	ID    int
	Items []*item
	Leaf  *leaf
}

type root struct {
	Child *child
}

var (
	failed  int32
	gcSink  [][]byte
	live    []*template.Template
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
		_ = &root{Child: &child{ID: i, Leaf: &leaf{Label: "g"}}}
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

// 四个 define 分片，按不同顺序 Parse 到同一模板集合。
var defineSnippets = []string{
	`{{define "root"}}[R {{template "child" .Child}}|{{template "leaf" .Child.Leaf}}]{{end}}`,
	`{{define "child"}}<C{{.ID}}:{{range .Items}}{{template "item" .}}{{end}}>{{end}}`,
	`{{define "item"}}({{.Name}}={{.Score}}){{end}}`,
	`{{define "leaf"}}<L{{.Label}}>{{end}}`,
	`{{define "vars"}}{{$c := .Child}}{{$c.ID}}:{{with $c.Leaf}}{{.Label}}{{end}}{{end}}`,
}

func execute(t *template.Template, name string, data any) (string, error) {
	var sb strings.Builder
	err := t.ExecuteTemplate(&sb, name, data)
	return sb.String(), err
}

func main() {
	uafSeed = envSeed()
	rounds := tortureRounds(24)

	const wantVars = `7:top`

	for r := 0; r < rounds; r++ {
		s1, s2 := 1+r%5, 2+r%5
		wantRoot := fmt.Sprintf(`[R <C7:(a=%d)(b=%d)>|<Ltop>]`, s1, s2)
		d := &root{Child: &child{
			ID: 7,
			Items: []*item{
				{Name: "a", Score: &s1},
				{Name: "b", Score: &s2},
			},
			Leaf: &leaf{Label: "top"},
		}}
		liveData := d

		t := template.New("root")
		rng := rand.New(rand.NewSource(uafSeed*1000 + int64(r)))
		for _, idx := range rng.Perm(len(defineSnippets)) {
			var err error
			t, err = t.Parse(defineSnippets[idx])
			ck(err == nil, fmt.Sprintf("round %d parse snippet %d: %v", r, idx, err))
		}
		live = append(live[:0], t)

		out, err := execute(t, "root", d)
		ck(err == nil, fmt.Sprintf("round %d execute root: %v", r, err))
		ck(out == wantRoot, fmt.Sprintf("round %d root output\n got: %q\nwant: %q", r, out, wantRoot))

		vout, err := execute(t, "vars", d)
		ck(err == nil, fmt.Sprintf("round %d execute vars: %v", r, err))
		ck(vout == wantVars, fmt.Sprintf("round %d vars output\n got: %q\nwant: %q", r, vout, wantVars))

		ck(d.Child.Items[0].Score != nil && *d.Child.Items[0].Score == s1,
			fmt.Sprintf("round %d item score pointer after execute", r))
		ck(d.Child.Items[1].Score != nil && *d.Child.Items[1].Score == s2,
			fmt.Sprintf("round %d second score pointer after execute", r))

		gcStress()

		ck(liveData.Child != nil && liveData.Child.Leaf != nil && liveData.Child.Leaf.Label == "top",
			fmt.Sprintf("round %d data pointers after GC", r))
		ck(liveData.Child.Items[0].Score != nil && *liveData.Child.Items[0].Score == s1,
			fmt.Sprintf("round %d item score pointer after GC", r))

		out2, err := execute(t, "root", liveData)
		ck(err == nil, fmt.Sprintf("round %d execute root after GC: %v", r, err))
		ck(out2 == wantRoot, fmt.Sprintf("round %d root output after GC\n got: %q\nwant: %q", r, out2, wantRoot))
		vout2, err := execute(t, "vars", liveData)
		ck(err == nil, fmt.Sprintf("round %d execute vars after GC: %v", r, err))
		ck(vout2 == wantVars, fmt.Sprintf("round %d vars output after GC\n got: %q\nwant: %q", r, vout2, wantVars))
	}

	finish("l16_nested_templates")
}
