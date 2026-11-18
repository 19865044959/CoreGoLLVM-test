// L16: html/template 在 HTML/属性/URL/JS 多上下文中的自动转义；GC 前后输出逐字节一致。
package main

import (
	"fmt"
	"html/template"
	"os"
	"runtime"
	"strconv"
	"strings"
	"sync/atomic"
)

type escData struct {
	Title    string
	Body     string
	Q        string
	Name     string
	RoleAttr string
	Note     *string
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
	for i := 0; i < 32; i++ {
		_ = make([]string, 256)
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

const pageSrc = `<p title="{{.Title}}">{{.Body}}</p>` +
	`<a href="/s?q={{.Q}}">go</a>` +
	`<script>var n={{.Name}};</script>` +
	`<div class="{{.RoleAttr}}">{{.Body}}</div>` +
	`<em>{{.Note}}</em>`

// 期望值由 HTML 文本/属性/URL 查询/JS 字符串四个上下文的转义规则决定。
var bs = string(rune(92)) // 反斜杠，JS 上下文转义前缀

var wantOut = `<p title="x&#34;&lt;y&gt;">&lt;b&gt;hi &amp; bye&lt;/b&gt;</p>` +
	`<a href="/s?q=a%20b%26c%2fd%3f">go</a>` +
	`<script>var n="a` + bs + `"b` + bs + `u003cc` + bs + `u003e";</script>` +
	`<div class="r1 x&#34;y">&lt;b&gt;hi &amp; bye&lt;/b&gt;</div>` +
	`<em>&lt;i&gt;p&lt;/i&gt;</em>`

func render(t *template.Template, d escData) (string, error) {
	var sb strings.Builder
	err := t.Execute(&sb, d)
	return sb.String(), err
}

func main() {
	uafSeed = envSeed()
	rounds := tortureRounds(30 + int(uafSeed%5))
	note := "<i>p</i>"
	data := escData{
		Title:    `x"<y>`,
		Body:     `<b>hi & bye</b>`,
		Q:        `a b&c/d?`,
		Name:     `a"b<c>`,
		RoleAttr: `r1 x"y`,
		Note:     &note,
	}

	for r := 0; r < rounds; r++ {
		t, err := template.New("page").Parse(pageSrc)
		ck(err == nil, fmt.Sprintf("round %d parse: %v", r, err))
		if err != nil {
			continue
		}
		live = append(live[:0], t)

		out, err := render(t, data)
		ck(err == nil, fmt.Sprintf("round %d execute: %v", r, err))
		ck(out == wantOut, fmt.Sprintf("round %d output\n got: %s\nwant: %s", r, out, wantOut))
		ck(strings.Contains(out, "&lt;b&gt;hi &amp; bye&lt;/b&gt;"),
			fmt.Sprintf("round %d HTML text context not escaped: %s", r, out))
		ck(strings.Contains(out, `&#34;`), fmt.Sprintf("round %d quoted attribute context not escaped: %s", r, out))
		ck(strings.Contains(out, `q=a%20b%26c%2fd%3f`), fmt.Sprintf("round %d URL query context not escaped: %s", r, out))
		ck(strings.Contains(out, "a"+bs+`"b`+bs+"u003cc"), fmt.Sprintf("round %d JS string context not escaped: %s", r, out))
		ck(!strings.Contains(out, `<b>hi`), fmt.Sprintf("round %d raw HTML leaked: %s", r, out))

		gcStress()

		ck(note == "<i>p</i>", fmt.Sprintf("round %d note pointer after GC", r))
		out2, err := render(t, data)
		ck(err == nil, fmt.Sprintf("round %d execute after GC: %v", r, err))
		ck(out2 == wantOut, fmt.Sprintf("round %d output after GC\n got: %s\nwant: %s", r, out2, wantOut))
	}

	finish("l16_html_escape")
}
