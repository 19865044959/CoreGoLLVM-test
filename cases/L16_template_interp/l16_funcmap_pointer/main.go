// L16: FuncMap 函数接收/返回指针；模板调用与 GC 后的直接调用都保持对象有效。
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

type item struct {
	ID   int
	Name string
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
		_ = &item{ID: i, Name: "trash", Next: &item{ID: -i}}
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

func newItem(id int, name string) *item {
	return &item{ID: id, Name: name}
}

func label(it *item) string {
	if it == nil {
		return "nil"
	}
	return strconv.Itoa(it.ID) + "/" + it.Name
}

func bump(it *item) *item {
	if it == nil {
		return nil
	}
	return &item{ID: it.ID, Name: it.Name, Next: it}
}

func link(a, b *item) *item {
	if a == nil || b == nil {
		return nil
	}
	return &item{ID: a.ID*10 + b.ID, Name: a.Name + "+" + b.Name, Next: b}
}

func score(p *int) int {
	if p == nil {
		return -1
	}
	return *p
}

func pick(ns ...*item) *item {
	if len(ns) == 0 {
		return nil
	}
	return ns[0]
}

func countPtrs(ns ...*item) int {
	return len(ns)
}

const tplSrc = `{{label (newItem 1 "a")}};` +
	`{{with bump (newItem 2 "b")}}{{label .}}{{end}};` +
	`{{label (link (newItem 3 "c") (newItem 4 "d"))}};` +
	`{{score .Score}};` +
	`{{label (pick .A .B)}};` +
	`{{countPtrs .A .B}}`

func main() {
	uafSeed = envSeed()
	fm := template.FuncMap{
		"newItem":   newItem,
		"label":     label,
		"bump":      bump,
		"link":      link,
		"score":     score,
		"pick":      pick,
		"countPtrs": countPtrs,
	}
	t, err := template.New("fm").Funcs(fm).Parse(tplSrc)
	ck(err == nil, fmt.Sprintf("parse template: %v", err))
	if err != nil {
		finish("l16_funcmap_pointer")
		return
	}

	rounds := 12 + int(uafSeed%7)
	for r := 0; r < rounds; r++ {
		sc := 41 + r
		a := &item{ID: 3 + r%3, Name: "c"}
		b := &item{ID: 4, Name: "d"}
		live = append(live[:0], a, b)
		data := struct {
			A     *item
			B     *item
			Score *int
		}{A: a, B: b, Score: &sc}

		want := fmt.Sprintf("1/a;2/b;34/c+d;%d;%d/c;2", sc, 3+r%3)
		var sb strings.Builder
		err := t.Execute(&sb, data)
		ck(err == nil, fmt.Sprintf("round %d execute: %v", r, err))
		ck(sb.String() == want, fmt.Sprintf("round %d output\n got: %q\nwant: %q", r, sb.String(), want))

		bumped := bump(a)
		linked := link(a, b)
		picked := pick(a, b)
		ck(bumped != nil && bumped.Next == a, fmt.Sprintf("round %d bump pointer link", r))
		ck(linked != nil && linked.Next == b && linked.ID == a.ID*10+b.ID, fmt.Sprintf("round %d link pointer fields", r))
		ck(picked == a, fmt.Sprintf("round %d pick pointer identity", r))
		ck(score(&sc) == sc && score(nil) == -1, fmt.Sprintf("round %d score pointer arg", r))
		ck(countPtrs(a, b, picked) == 3, fmt.Sprintf("round %d variadic pointer count", r))
		live = append(live, bumped, linked, picked)

		gcStress()

		ck(a.ID == 3+r%3 && a.Name == "c", fmt.Sprintf("round %d arg pointer after GC", r))
		ck(bumped.Next == a && bumped.Name == "c", fmt.Sprintf("round %d bumped pointer chain after GC", r))
		ck(linked.Next == b && b.Name == "d", fmt.Sprintf("round %d linked pointer chain after GC", r))
		ck(picked == a, fmt.Sprintf("round %d picked pointer identity after GC", r))

		sb.Reset()
		err = t.Execute(&sb, data)
		ck(err == nil, fmt.Sprintf("round %d execute after GC: %v", r, err))
		ck(sb.String() == want, fmt.Sprintf("round %d output after GC\n got: %q\nwant: %q", r, sb.String(), want))
	}

	finish("l16_funcmap_pointer")
}
