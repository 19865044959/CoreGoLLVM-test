// L16: 模板调用数据方法（指针接收者、指针返回值、带参方法）；GC 后方法仍可调用。
package main

import (
	"fmt"
	"os"
	"runtime"
	"strconv"
	"strings"
	"sync/atomic"
	"text/template"
)

type dept struct {
	Title string
}

type tree struct {
	Name   string
	N      int
	Parent *tree
	Kids   []*tree
}

func (t *tree) Label() string {
	return t.Name + "#" + strconv.Itoa(t.N)
}

func (t *tree) Add(d int) int {
	t.N += d
	return t.N
}

func (t *tree) Child(i int) *tree {
	if i < 0 || i >= len(t.Kids) {
		return nil
	}
	return t.Kids[i]
}

func (t *tree) Dept() *dept {
	return &dept{Title: t.Name + "-dept"}
}

var (
	failed  int32
	gcSink  [][]byte
	live    []*tree
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
		_ = &tree{Name: "trash", N: i, Parent: &tree{Name: "p"}, Kids: []*tree{{Name: "k"}}}
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

const tplSrc = `{{.Label}}|` +
	`{{.Dept.Title}}|` +
	`{{range .Kids}}{{.Label}},{{end}}|` +
	`{{with .Child 1}}{{.Name}}{{else}}none{{end}}|` +
	`{{.Add 5}}|` +
	`{{with .Child 9}}{{.Name}}{{else}}nil{{end}}|` +
	`{{with .Parent}}{{.Label}}{{else}}none{{end}}`

func render(t *template.Template, tr *tree) (string, error) {
	var sb strings.Builder
	err := t.Execute(&sb, tr)
	return sb.String(), err
}

func main() {
	uafSeed = envSeed()
	t, err := template.New("tree").Parse(tplSrc)
	ck(err == nil, fmt.Sprintf("parse template: %v", err))
	if err != nil {
		finish("l16_method_templates")
		return
	}

	rounds := 10 + int(uafSeed%5)
	for r := 0; r < rounds; r++ {
		parent := &tree{Name: "up", N: 9}
		root := &tree{Name: "root", N: 1, Parent: parent}
		root.Kids = []*tree{
			{Name: "a", N: 2, Parent: root},
			{Name: "b", N: 3, Parent: root},
		}
		live = append(live[:0], root, parent, root.Kids[0], root.Kids[1])

		want1 := "root#1|root-dept|a#2,b#3,|b|6|nil|up#9"
		out, err := render(t, root)
		ck(err == nil, fmt.Sprintf("round %d first render: %v", r, err))
		ck(out == want1, fmt.Sprintf("round %d first render\n got: %q\nwant: %q", r, out, want1))
		ck(root.N == 6, fmt.Sprintf("round %d method Add mutated receiver N=%d", r, root.N))
		ck(root.Child(1) == root.Kids[1], fmt.Sprintf("round %d method Child identity", r))
		ck(root.Dept().Title == "root-dept", fmt.Sprintf("round %d method pointer return", r))

		gcStress()

		ck(root.Kids[0] != nil && root.Kids[0].N == 2, fmt.Sprintf("round %d kid pointer after GC", r))
		ck(root.Parent != nil && root.Parent.Name == "up", fmt.Sprintf("round %d parent pointer after GC", r))
		ck(root.Label() == "root#6", fmt.Sprintf("round %d Label method after GC", r))
		ck(root.Child(0).Label() == "a#2", fmt.Sprintf("round %d Child method after GC", r))
		ck(root.Dept().Title == "root-dept", fmt.Sprintf("round %d Dept method after GC", r))
		ck(root.Child(9) == nil, fmt.Sprintf("round %d Child out of range after GC", r))

		want2 := "root#6|root-dept|a#2,b#3,|b|11|nil|up#9"
		out2, err := render(t, root)
		ck(err == nil, fmt.Sprintf("round %d second render: %v", r, err))
		ck(out2 == want2, fmt.Sprintf("round %d second render after GC\n got: %q\nwant: %q", r, out2, want2))
		ck(root.N == 11, fmt.Sprintf("round %d Add after second render N=%d", r, root.N))
	}

	finish("l16_method_templates")
}
