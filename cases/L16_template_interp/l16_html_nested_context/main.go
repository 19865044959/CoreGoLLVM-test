// L16: html/template 嵌套上下文：define/template 调用继承 HTML/URL/JS/CSS 上下文，
// block 覆盖顺序、安全类型、属性/范围转义与未闭合上下文的解析错误；跨 GC 重放。
package main

import (
	"fmt"
	"html/template"
	"math/rand"
	"os"
	"runtime"
	"strconv"
	"strings"
	"sync/atomic"
)

const defs = `{{define "frag"}}{{.}}{{end}}` +
	`{{define "body"}}<p>{{template "frag" .Text}}</p><ul>{{range .Items}}<li>{{template "frag" .}}</li>{{end}}</ul>{{end}}` +
	`{{define "link"}}<a href="/q?x={{template "frag" .Query}}">L</a>{{end}}` +
	`{{define "script"}}<script>var v="{{template "frag" .Text}}";</script>{{end}}` +
	`{{define "style"}}<style>.x{content:"{{template "frag" .Text}}"}</style>{{end}}` +
	`{{define "card"}}<span>{{template "frag" .Text}}</span>{{end}}` +
	`{{define "layout"}}<div class="{{.Class}}">{{template "body" .}}{{template "link" .}}{{template "script" .}}{{template "style" .}}{{template "card" .}}</div>{{end}}`

const blockLayout = `{{define "layout2"}}<div>{{block "footer" .}}DEFAULT{{end}}</div>{{end}}`
const blockOverride = `{{define "footer"}}OV{{end}}`

type view struct {
	Class    string
	Text     string
	Query    string
	Items    []string
	URLStr   string
	SafeURL  template.URL
	SafeAttr template.HTMLAttr
	SafeJS   template.JSStr
	SafeCSS  template.CSS
	SafeSrc  template.Srcset
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
		b := make([]byte, 8192)
		b[0] = byte(i)
		gcSink = append(gcSink[:0], b)
	}
	for i := 0; i < 64; i++ {
		_ = &view{Class: "g", Text: strconv.Itoa(i), Items: []string{"a", "b"}}
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
	if n > 50000 {
		n = 50000
	}
	return n
}

func exec(t *template.Template, name string, data any) (string, error) {
	var b strings.Builder
	err := t.ExecuteTemplate(&b, name, data)
	return b.String(), err
}

func main() {
	uafSeed = envSeed()
	rounds := tortureRounds(20)

	v := &view{
		Class:    `x" onload="evil`,
		Text:     `<b>"'&`,
		Query:    `a b/&=x`,
		Items:    []string{"a<b", "c&d"},
		URLStr:   "javascript:alert(1)",
		SafeURL:  template.URL("javascript:alert(1)"),
		SafeAttr: template.HTMLAttr(`data-x="1"`),
		SafeJS:   template.JSStr(`a"</script>`),
		SafeCSS:  template.CSS(`body{color:red}`),
		SafeSrc:  template.Srcset("a.png 1x, b.png 2x"),
	}

	base, err := template.New("base").Parse(defs)
	ck(err == nil, fmt.Sprintf("base parse err=%v", err))
	if base == nil {
		finish("l16_html_nested_context")
		return
	}
	live = append(live, base)

	wantLayout := "<div class=\"x&#34; onload=&#34;evil\">" +
		"<p>&lt;b&gt;&#34;&#39;&amp;</p><ul><li>a&lt;b</li><li>c&amp;d</li></ul>" +
		"<a href=\"/q?x=a%20b%2f%26%3dx\">L</a>" +
		"<script>var v=\"\\u003cb\\u003e\\u0022\\u0027\\u0026\";</script>" +
		"<style>.x{content:\"\\3c b\\3e\\22\\27\\26 \"}</style>" +
		"<span>&lt;b&gt;&#34;&#39;&amp;</span></div>"
	got, err := exec(base, "layout", v)
	ck(err == nil, fmt.Sprintf("layout execute err=%v", err))
	ck(got == wantLayout, fmt.Sprintf("layout output=%q", got))

	// 同一 frag 在 HTML/URL/JS/CSS 四种调用点获得不同转义。
	parts := []struct{ name, want string }{
		{"body", "<p>&lt;b&gt;&#34;&#39;&amp;</p><ul><li>a&lt;b</li><li>c&amp;d</li></ul>"},
		{"link", "<a href=\"/q?x=a%20b%2f%26%3dx\">L</a>"},
		{"script", "<script>var v=\"\\u003cb\\u003e\\u0022\\u0027\\u0026\";</script>"},
		{"style", "<style>.x{content:\"\\3c b\\3e\\22\\27\\26 \"}</style>"},
		{"card", "<span>&lt;b&gt;&#34;&#39;&amp;</span>"},
	}
	for _, p := range parts {
		got, err := exec(base, p.name, v)
		ck(err == nil && got == p.want, fmt.Sprintf("%s output=%q err=%v", p.name, got, err))
	}

	// 属性内 range 仍在 HTML 属性上下文转义。
	attrRange := `<div data-x="{{range .Items}}{{.}},{{end}}">x</div>`
	tr, err := template.New("attr-range").Parse(attrRange)
	ck(err == nil, fmt.Sprintf("attr range parse err=%v", err))
	if err == nil {
		got, err := exec(tr, "attr-range", v)
		ck(err == nil && got == "<div data-x=\"a&lt;b,c&amp;d,\">x</div>", fmt.Sprintf("attr range=%q err=%v", got, err))
	}

	// 安全类型：URL 过滤、HTMLAttr、JSStr、CSS、Srcset。
	safeCases := []struct {
		name string
		body string
		want string
	}{
		{"unsafe-url", `<a href="{{.URLStr}}">u</a>`, `<a href="#ZgotmplZ">u</a>`},
		{"safe-url", `<a href="{{.SafeURL}}">u</a>`, `<a href="javascript:alert%281%29">u</a>`},
		{"safe-attr", `<div {{.SafeAttr}}></div>`, `<div data-x="1"></div>`},
		{"safe-js", `<script>var s="{{.SafeJS}}";</script>`, "<script>var s=\"a\\u0022\\u003c\\/script\\u003e\";</script>"},
		{"safe-css", `<style>{{.SafeCSS}}</style>`, `<style>body{color:red}</style>`},
		{"safe-src", `<img srcset="{{.SafeSrc}}">`, `<img srcset="a.png 1x, b.png 2x">`},
		{"dyn-attr-name", `<div {{.Text}}>z</div>`, `<div ZgotmplZ>z</div>`},
	}
	for _, c := range safeCases {
		t, err := template.New(c.name).Parse(c.body)
		ck(err == nil, fmt.Sprintf("%s parse err=%v", c.name, err))
		if err != nil {
			continue
		}
		got, err := exec(t, c.name, v)
		ck(err == nil && got == c.want, fmt.Sprintf("%s output=%q err=%v", c.name, got, err))
	}

	// block 覆盖与解析顺序：block 后解析时默认值胜出。
	t1 := template.New("o1")
	_, err = t1.Parse(blockLayout)
	ck(err == nil, fmt.Sprintf("layout2 parse err=%v", err))
	_, err = t1.Parse(blockOverride)
	ck(err == nil, fmt.Sprintf("override after layout parse err=%v", err))
	got, err = exec(t1, "layout2", nil)
	ck(err == nil && got == "<div>OV</div>", fmt.Sprintf("override after layout=%q err=%v", got, err))
	t2 := template.New("o2")
	_, err = t2.Parse(blockOverride)
	ck(err == nil, fmt.Sprintf("override parse err=%v", err))
	_, err = t2.Parse(blockLayout)
	ck(err == nil, fmt.Sprintf("layout after override parse err=%v", err))
	got, err = exec(t2, "layout2", nil)
	ck(err == nil && got == "<div>DEFAULT</div>", fmt.Sprintf("layout after override=%q err=%v", got, err))
	live = append(live, t1, t2)

	// 未闭合的 JS/属性上下文在 Parse 期报错；缺失子模板在执行期报错。
	bad := []struct {
		name string
		body string
		want string
	}{
		{"unclosed-js", `<script>var x = {{.Text}}`, "ends in a non-text context"},
		{"unclosed-attr", `<div class="{{.Text}}"`, "ends in a non-text context"},
		{"ambiguous-url", `<a href="{{if .C}}?{{end}}a={{.Query}}"></a>`, "ambiguous context"},
	}
	for _, c := range bad {
		t, perr := template.New(c.name).Parse(c.body)
		ck(perr == nil, fmt.Sprintf("%s parse err=%v", c.name, perr))
		if perr != nil {
			continue
		}
		_, err := exec(t, c.name, v)
		ck(err != nil && strings.Contains(err.Error(), c.want), fmt.Sprintf("%s err=%v", c.name, err))
	}
	miss := template.Must(template.New("miss").Parse(`{{template "nope" .}}`))
	_, err = exec(miss, "miss", v)
	ck(err != nil && strings.Contains(err.Error(), `no such template "nope"`), fmt.Sprintf("missing template err=%v", err))

	// GC 与 TORTURE_ROUNDS：随机顺序重放，输出保持一致。
	rng := rand.New(rand.NewSource(uafSeed))
	names := []string{"layout", "body", "link", "script", "style", "card"}
	for round := 0; round < rounds; round++ {
		perm := rng.Perm(len(parts))
		for _, pi := range perm {
			p := parts[pi]
			got, err := exec(base, p.name, v)
			ck(err == nil && got == p.want, fmt.Sprintf("round %d %s output=%q err=%v", round, p.name, got, err))
		}
		// 随机名字集合覆盖 Templates 查找。
		idx := rng.Intn(len(names))
		ck(base.Lookup(names[idx]) != nil, fmt.Sprintf("round %d lookup %s", round, names[idx]))
		if round%4 == 0 {
			gcStress()
		}
	}
	gcStress()
	got, err = exec(base, "layout", v)
	ck(err == nil && got == wantLayout, fmt.Sprintf("post-GC layout=%q err=%v", got, err))
	ck(v.Text == `<b>"'&` && len(v.Items) == 2 && v.SafeURL == template.URL("javascript:alert(1)"),
		"data changed after GC")

	finish("l16_html_nested_context")
}
