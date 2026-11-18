// L16: text/template 渲染含指针字段的 struct；GC 前后输出一致，指针字段不被破坏。
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

type address struct {
	City string
	ZIP  string
}

type user struct {
	Name   string
	Addr   *address
	Score  *int
	Friend *user
	Tags   []string
}

var (
	failed  int32
	gcSink  [][]byte
	live    []*user
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
	// 额外指针垃圾，让本轮模板数据经历老->新引用与清扫。
	trash := make([]*address, 128)
	for i := range trash {
		trash[i] = &address{City: "garbage", ZIP: strconv.Itoa(i)}
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

const tplSrc = `{{.Name}}|{{.Addr.City}}|{{.Addr.ZIP}}|{{.Score}}|{{.Friend.Name}}|{{.Friend.Addr.City}}|{{range .Tags}}{{.}},{{end}}|{{if .Friend.Friend}}bad{{else}}nil{{end}}`

func render(t *template.Template, u *user) (string, error) {
	var sb strings.Builder
	err := t.Execute(&sb, u)
	return sb.String(), err
}

func main() {
	uafSeed = envSeed()
	t, err := template.New("user").Parse(tplSrc)
	ck(err == nil, fmt.Sprintf("parse template: %v", err))
	if err != nil {
		finish("l16_text_template_fields")
		return
	}

	rounds := 16 + int(uafSeed%9)
	for r := 0; r < rounds; r++ {
		score := 10 + r
		u := &user{
			Name:  fmt.Sprintf("u%d", r),
			Addr:  &address{City: "Beijing", ZIP: fmt.Sprintf("100%02d", r%100)},
			Score: &score,
			Tags:  []string{"a", "b"},
		}
		u.Friend = &user{Name: "f", Addr: &address{City: "Shanghai", ZIP: "200000"}}
		live = append(live[:0], u) // 确保模板数据在堆上被引用

		want := fmt.Sprintf("u%d|Beijing|100%02d|%d|f|Shanghai|a,b,|nil", r, r%100, score)

		out, err := render(t, u)
		ck(err == nil, fmt.Sprintf("round %d first render error: %v", r, err))
		ck(out == want, fmt.Sprintf("round %d first render\n got: %q\nwant: %q", r, out, want))

		gcStress()

		ck(u.Score != nil && *u.Score == score, fmt.Sprintf("round %d score pointer after GC", r))
		ck(u.Addr != nil && u.Addr.ZIP == fmt.Sprintf("100%02d", r%100), fmt.Sprintf("round %d addr pointer after GC", r))
		ck(u.Friend != nil && u.Friend.Addr != nil && u.Friend.Addr.City == "Shanghai",
			fmt.Sprintf("round %d friend pointer chain after GC", r))
		ck(u.Friend.Friend == nil, fmt.Sprintf("round %d nil friend pointer after GC", r))

		out2, err := render(t, u)
		ck(err == nil, fmt.Sprintf("round %d second render error: %v", r, err))
		ck(out2 == want, fmt.Sprintf("round %d second render after GC\n got: %q\nwant: %q", r, out2, want))
	}

	finish("l16_text_template_fields")
}
