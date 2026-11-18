// L16: html/template 边缘嵌套上下文：RCDATA(script/style 注释/JSON)、事件处理器、
// CSS url/@import、xlink:href、srcset、srcdoc、未加引号属性与 urlquery 管道；跨 GC 重放。
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

type view struct {
	Text     string
	URL      string
	SafeURL  template.URL
	SafeHTML template.HTML
	C        bool
}

type tmplCase struct {
	name string
	body string
	want string
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
		_ = &view{Text: strconv.Itoa(i), URL: "u"}
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

func exec(t *template.Template, data any) (string, error) {
	var b strings.Builder
	err := t.Execute(&b, data)
	return b.String(), err
}

func main() {
	uafSeed = envSeed()
	rng := rand.New(rand.NewSource(uafSeed))

	v := view{
		Text:     `<b>&"'`,
		URL:      `a b&c?x=1`,
		SafeURL:  template.URL("a b&c"),
		SafeHTML: template.HTML("<i>ok</i>"),
		C:        true,
	}
	unsafe := view{
		Text:    `<b>&"'`,
		URL:     `javascript:alert(1)//x y`,
		SafeURL: template.URL("javascript:alert(1)"),
	}
	cases := []tmplCase{
		{"textarea", `<textarea>{{.Text}}</textarea>`, `<textarea>&lt;b&gt;&amp;&#34;&#39;</textarea>`},
		{"title", `<title>{{.Text}}</title>`, `<title>&lt;b&gt;&amp;&#34;&#39;</title>`},
		{"comment", `<p><!-- {{.Text}} --></p>`, `<p></p>`},
		{"comment-tail", `<p><!-- x -->{{.Text}}</p>`, `<p>&lt;b&gt;&amp;&#34;&#39;</p>`},
		{"onclick", `<button onclick="alert({{.Text}})">x</button>`, `<button onclick="alert(&#34;\u003cb\u003e\u0026\&#34;&#39;&#34;)">x</button>`},
		{"style-url", `<button style="background:url('{{.URL}}')">x</button>`, `<button style="background:url('a%20b&amp;c?x=1')">x</button>`},
		{"xlink", `<svg><a xlink:href="{{.URL}}">x</a></svg>`, `<svg><a xlink:href="a%20b&amp;c?x=1">x</a></svg>`},
		{"meta-refresh", `<meta http-equiv="refresh" content="0;url={{.URL}}">`, `<meta http-equiv="refresh" content="0;url=a b&amp;c?x=1">`},
		{"unquoted-attr", `<div data-x={{.Text}}>x</div>`, `<div data-x=&lt;b&gt;&amp;&#34;&#39;>x</div>`},
		{"printf-attr", `<div data-x="{{printf "%s" .Text}}">x</div>`, `<div data-x="&lt;b&gt;&amp;&#34;&#39;">x</div>`},
		{"script-comment", `<script>/*{{.Text}}*/</script>`, `<script> </script>`},
		{"style-comment", `<style>/*{{.Text}}*/</style>`, `<style> </style>`},
		{"json-script", `<script type="application/json">{"v":{{.Text}}}</script>`, `<script type="application/json">{"v":"\u003cb\u003e\u0026\"'"}</script>`},
		{"safe-url", `<a href="{{.SafeURL}}">s</a>`, `<a href="a%20b&amp;c">s</a>`},
		{"safe-html", `<div>{{.SafeHTML}}</div>`, `<div><i>ok</i></div>`},
		{"nested-textarea", `<textarea>{{template "frag" .}}</textarea>{{define "frag"}}{{.Text}}{{end}}`, `<textarea>&lt;b&gt;&amp;&#34;&#39;</textarea>`},
		{"href-mixed", `<a href="/?q={{.Text}}&r={{.URL}}">q</a>`, `<a href="/?q=%3cb%3e%26%22%27&r=a%20b%26c%3fx%3d1">q</a>`},
		{"css-import", `<style>@import "{{.URL}}";</style>`, `<style>@import "a b\26 c?x=1";</style>`},
		{"css-fn", `<style>.x{background:url({{.URL}})}</style>`, `<style>.x{background:url(a%20b&c?x=1)}</style>`},
		{"urlquery", `<a href="{{.URL | urlquery}}">q</a>`, `<a href="a&#43;b%26c%3Fx%3D1">q</a>`},
		{"urlquery-path", `<a href="/?q={{.Text | urlquery}}">q</a>`, `<a href="/?q=%3Cb%3E%26%22%27">q</a>`},
		{"srcset", `<img srcset="{{.URL}} 1x">`, `<img srcset="#ZgotmplZ 1x">`},
		{"srcdoc", `<iframe srcdoc="{{.Text}}"></iframe>`, `<iframe srcdoc="&lt;b&gt;&amp;&#34;&#39;"></iframe>`},
		{"title-attr", `<div title="{{.Text}}">x</div>`, `<div title="&lt;b&gt;&amp;&#34;&#39;">x</div>`},
		{"js-string", `<script>var x = "{{.URL}}"</script>`, `<script>var x = "a b\u0026c?x=1"</script>`},
		{"js-value", `<script>var x = {{.URL}}</script>`, `<script>var x = "a b\u0026c?x=1"</script>`},
		{"href-space", `<a href="  {{.URL}}">s</a>`, `<a href="  a%20b&amp;c?x=1">s</a>`},
	}
	unsafeCases := []tmplCase{
		{"unsafe-href", `<a href="{{.URL}}">u</a>`, `<a href="#ZgotmplZ">u</a>`},
		{"unsafe-srcset", `<img srcset="{{.URL}} 2x">`, `<img srcset="#ZgotmplZ 2x">`},
		{"unsafe-style", `<div style="background:url('{{.URL}}')">x</div>`, `<div style="background:url('#ZgotmplZ')">x</div>`},
	}

	got := map[string]string{}
	run := func(c tmplCase, data any) {
		t, err := template.New(c.name).Parse(c.body)
		ck(err == nil, fmt.Sprintf("%s parse err=%v", c.name, err))
		if err != nil {
			return
		}
		out, err := exec(t, data)
		ck(err == nil, fmt.Sprintf("%s exec err=%v", c.name, err))
		got[c.name] = out
		ck(out == c.want, fmt.Sprintf("%s output=%q want %q", c.name, out, c.want))
		live = append(live, t)
	}
	for _, c := range cases {
		run(c, v)
	}
	for _, c := range unsafeCases {
		run(c, unsafe)
	}

	// 错误边界：未闭合/歧义上下文在执行期报错。
	bad := []struct {
		name string
		body string
		want string
	}{
		{"bad-script", `<script>var x = {{.Text}}`, "non-text context"},
		{"bad-attr", `<div class="{{.Text}}"`, "non-text context"},
		{"ambiguous-url", `<a href="{{if .C}}?{{end}}a={{.URL}}"></a>`, "ambiguous context"},
	}
	for _, c := range bad {
		t, err := template.New(c.name).Parse(c.body)
		ck(err == nil, fmt.Sprintf("%s parse err=%v", c.name, err))
		if err != nil {
			continue
		}
		_, err = exec(t, v)
		ck(err != nil && strings.Contains(err.Error(), c.want), fmt.Sprintf("%s exec err=%v", c.name, err))
	}

	// GC 后按种子顺序重放全部成功用例，输出逐字节一致。
	all := append(append([]tmplCase{}, cases...), unsafeCases...)
	for round := 0; round < 3; round++ {
		gcStress()
		for _, idx := range rng.Perm(len(all)) {
			c := all[idx]
			data := any(v)
			if strings.HasPrefix(c.name, "unsafe-") {
				data = unsafe
			}
			t, err := template.New(c.name).Parse(c.body)
			ck(err == nil, fmt.Sprintf("round %d %s parse err=%v", round, c.name, err))
			if err != nil {
				continue
			}
			out, err := exec(t, data)
			ck(err == nil && out == got[c.name], fmt.Sprintf("round %d %s output=%q err=%v", round, c.name, out, err))
		}
		ck(v.Text == `<b>&"'` && v.SafeHTML == template.HTML("<i>ok</i>"), fmt.Sprintf("round %d data changed", round))
	}

	finish("l16_html_context_edge")
}
