// L16: html/template 嵌套上下文与 Clone 边界：Clone 前不执行、覆写 define 后重新转义、
// AddParseTree 继承 HTML/URL 上下文、执行后禁止 Clone；TORTURE_ROUNDS 反复克隆/执行。
package main

import (
	"fmt"
	"html/template"
	"math/rand"
	"os"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"text/template/parse"
)

const baseSrc = `{{define "head"}}<title>{{.Title}}</title>{{end}}` +
	`{{define "body"}}<a href="/q?x={{.Query}}">{{.Text}}</a>` +
	`<script>var v="{{.Text}}";</script><style>.x{content:"{{.Text}}"}</style>{{end}}` +
	`{{define "layout"}}<div class="{{.Class}}">{{template "head" .}}{{template "body" .}}</div>{{end}}` +
	`{{define "frag"}}<b>{{.Text}}</b>{{end}}`

type view struct {
	Class   string
	Title   string
	Query   string
	Text    string
	SafeURL template.URL
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
		_ = &view{Class: "g", Title: "t", Text: strconv.Itoa(i)}
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
	rng := rand.New(rand.NewSource(uafSeed))
	rounds := tortureRounds(16)

	v := view{
		Class:   `x"y`,
		Title:   `<t>`,
		Query:   `a b&c`,
		Text:    `<b>"'&`,
		SafeURL: template.URL("javascript:alert(1)"),
	}
	base, err := template.New("base").Parse(baseSrc)
	ck(err == nil, fmt.Sprintf("base parse err=%v", err))
	if base == nil {
		finish("l16_html_clone_context")
		return
	}
	live = append(live, base)

	want := map[string]string{
		"head":   `<title>&lt;t&gt;</title>`,
		"body":   `<a href="/q?x=a%20b%26c">&lt;b&gt;&#34;&#39;&amp;</a><script>var v="\u003cb\u003e\u0022\u0027\u0026";</script><style>.x{content:"\3c b\3e\22\27\26 "}</style>`,
		"layout": `<div class="x&#34;y"><title>&lt;t&gt;</title><a href="/q?x=a%20b%26c">&lt;b&gt;&#34;&#39;&amp;</a><script>var v="\u003cb\u003e\u0022\u0027\u0026";</script><style>.x{content:"\3c b\3e\22\27\26 "}</style></div>`,
		"frag":   `<b>&lt;b&gt;&#34;&#39;&amp;</b>`,
	}

	// base 保持未执行状态，所有输出检查走克隆，便于之后继续 Clone。
	execBase, err := base.Clone()
	ck(err == nil, fmt.Sprintf("execBase clone err=%v", err))
	if execBase == nil {
		finish("l16_html_clone_context")
		return
	}
	live = append(live, execBase)
	for _, name := range []string{"head", "body", "layout", "frag"} {
		got, err := exec(execBase, name, v)
		ck(err == nil && got == want[name], fmt.Sprintf("%s=%q err=%v", name, got, err))
	}

	// 1) Clone 覆写 head：重新走 HTML 上下文转义；base 不受影响。
	overlay := `{{define "head"}}<h1>{{.Title}}</h1>{{end}}`
	clone1, err := base.Clone()
	ck(err == nil, fmt.Sprintf("clone1 err=%v", err))
	if clone1 != nil {
		live = append(live, clone1)
		_, err = clone1.Parse(overlay)
		ck(err == nil, fmt.Sprintf("clone1 overlay err=%v", err))
		got, err := exec(clone1, "layout", v)
		ck(err == nil && strings.Contains(got, "<h1>&lt;t&gt;</h1>") && strings.Contains(got, `<div class="x&#34;y">`),
			fmt.Sprintf("clone1 layout=%q err=%v", got, err))
		got, err = exec(execBase, "layout", v)
		ck(err == nil && got == want["layout"], "base layout changed by clone overlay")
		ck(base.Lookup("head") != nil, "base head lost")
	}

	// 2) Clone 后 Parse 新定义：base 看不到；缺失子模板在 base 执行时报错。
	clone2, err := base.Clone()
	ck(err == nil, fmt.Sprintf("clone2 err=%v", err))
	if clone2 != nil {
		live = append(live, clone2)
		_, err = clone2.Parse(`{{define "extra"}}<p>{{.Text}}</p>{{end}}`)
		ck(err == nil, fmt.Sprintf("clone2 extra err=%v", err))
		got, err := exec(clone2, "extra", v)
		ck(err == nil && got == "<p>&lt;b&gt;&#34;&#39;&amp;</p>", fmt.Sprintf("extra=%q err=%v", got, err))
		ck(base.Lookup("extra") == nil, "base must not see clone extra")
		_, err = exec(base, "extra", v)
		ck(err != nil, fmt.Sprintf("base extra err=%v", err))
	}

	// 3) AddParseTree：新子树按调用点上下文转义，Clone 隔离。
	root := template.Must(template.New("root").Parse(`{{define "a"}} {{.}} {{template "b"}} {{.}} "></a>{{end}}`))
	tree, err := parse.Parse("t", `{{define "b"}}<a href="{{end}}`, "", "", nil, nil)
	ck(err == nil && tree["b"] != nil, fmt.Sprintf("parse tree err=%v", err))
	if err == nil {
		added := template.Must(root.AddParseTree("b", tree["b"]))
		cl, ce := added.Clone()
		ck(ce == nil, fmt.Sprintf("AddParseTree clone err=%v", ce))
		got, e := exec(added, "a", "1>0")
		ck(e == nil && got == ` 1&gt;0 <a href=" 1%3e0 "></a>`, fmt.Sprintf("AddParseTree=%q err=%v", got, e))
		if ce == nil {
			live = append(live, cl)
			got2, e2 := exec(cl, "a", "1>0")
			ck(e2 == nil && got2 == got, fmt.Sprintf("AddParseTree clone=%q err=%v", got2, e2))
		}
	}

	// 4) 不完整的上下文由 Clone 后补全：不同 Clone 得到 URL/JS 两种转义。
	inc := template.Must(template.New("t0").Parse(`{{define "a"}}{{template "lhs"}}{{.}}{{template "rhs"}}{{end}}`))
	urlClone, err := inc.Clone()
	ck(err == nil, fmt.Sprintf("url clone err=%v", err))
	if urlClone != nil {
		live = append(live, urlClone)
		template.Must(urlClone.Parse(`{{define "lhs"}} <a href=" {{end}}`))
		template.Must(urlClone.Parse(`{{define "rhs"}} "></a> {{end}}`))
		got, err := exec(urlClone, "a", "<i>*/")
		ck(err == nil && got == ` <a href=" %3ci%3e*/ "></a> `, fmt.Sprintf("url clone=%q err=%v", got, err))
	}
	jsClone, err := inc.Clone()
	ck(err == nil, fmt.Sprintf("js clone err=%v", err))
	if jsClone != nil {
		live = append(live, jsClone)
		template.Must(jsClone.Parse(`{{define "lhs"}} <p onclick="javascript: {{end}}`))
		template.Must(jsClone.Parse(`{{define "rhs"}} "></p> {{end}}`))
		got, err := exec(jsClone, "a", "<i>*/")
		ck(err == nil && strings.Contains(got, `onclick="javascript: &#34;\u003ci\u003e*/&#34; "`),
			fmt.Sprintf("js clone=%q err=%v", got, err))
	}

	// 5) 执行后禁止 Clone；未闭合上下文在执行期报错。
	done, err := base.Clone()
	ck(err == nil, fmt.Sprintf("done clone err=%v", err))
	if done != nil {
		live = append(live, done)
		_, err = exec(done, "layout", v)
		ck(err == nil, fmt.Sprintf("done exec err=%v", err))
		_, err = done.Clone()
		ck(err != nil && strings.Contains(err.Error(), "cannot Clone"), fmt.Sprintf("clone after execute err=%v", err))
	}
	bad, err := base.Clone()
	ck(err == nil, fmt.Sprintf("bad clone err=%v", err))
	if bad != nil {
		live = append(live, bad)
		_, err = bad.New("bad").Parse(`<script>var x = {{.Text}}`)
		ck(err == nil, fmt.Sprintf("bad parse err=%v", err))
		_, err = exec(bad, "bad", v)
		ck(err != nil && strings.Contains(err.Error(), "non-text context"), fmt.Sprintf("bad exec err=%v", err))
	}

	// 6) 安全类型在 Clone/覆写后保持：URL 过滤仍生效。
	safeSrc := `{{define "u"}}<a href="{{.SafeURL}}">s</a>{{end}}`
	safe := template.Must(base.Clone())
	_, err = safe.Parse(safeSrc)
	ck(err == nil, fmt.Sprintf("safe parse err=%v", err))
	got, err := exec(safe, "u", v)
	ck(err == nil && got == `<a href="javascript:alert%281%29">s</a>`, fmt.Sprintf("safe=%q err=%v", got, err))
	live = append(live, safe)

	// 7) TORTURE_ROUNDS：反复 Clone + 覆写 + 多上下文执行；每 4 轮 GC。
	names := []string{"layout", "body", "frag", "round"}
	for round := 0; round < rounds; round++ {
		cl, err := base.Clone()
		ck(err == nil, fmt.Sprintf("round %d clone err=%v", round, err))
		if err != nil {
			continue
		}
		cl.Funcs(template.FuncMap{"roundTag": func(s string) string { return fmt.Sprintf("%s@%d", s, round) }})
		_, err = cl.Parse(`{{define "head"}}<h1>{{roundTag .Title}}</h1>{{end}}` +
			`{{define "round"}}<a href="/r?q={{.Query}}">{{.Text}}</a><script>var n={{.Class}};</script>{{end}}`)
		ck(err == nil, fmt.Sprintf("round %d overlay err=%v", round, err))
		perm := rng.Perm(len(names))
		for _, ni := range perm {
			name := names[ni]
			got, err := exec(cl, name, v)
			ck(err == nil, fmt.Sprintf("round %d %s err=%v", round, name, err))
			if err != nil {
				continue
			}
			switch name {
			case "layout":
				ck(strings.Contains(got, fmt.Sprintf("<h1>&lt;t&gt;@%d</h1>", round)), fmt.Sprintf("round %d layout=%q", round, got))
			case "body":
				ck(got == want["body"], fmt.Sprintf("round %d body=%q", round, got))
			case "frag":
				ck(got == want["frag"], fmt.Sprintf("round %d frag=%q", round, got))
			case "round":
				ck(strings.Contains(got, `<a href="/r?q=a%20b%26c">&lt;b&gt;&#34;&#39;&amp;</a>`) &&
					strings.Contains(got, `var n="x\"y";`), fmt.Sprintf("round %d round=%q", round, got))
			}
		}
		if round%4 == 0 {
			gcStress()
		}
		live = append(live, cl)
	}

	// 8) 并发执行独立 Clone，输出确定；base 仍可 Clone。
	var wg sync.WaitGroup
	for w := 0; w < 4; w++ {
		cl, err := base.Clone()
		ck(err == nil, fmt.Sprintf("conc clone %d err=%v", w, err))
		if err != nil {
			continue
		}
		live = append(live, cl)
		wg.Add(1)
		go func(w int, cl *template.Template) {
			defer wg.Done()
			for i := 0; i < 16; i++ {
				got, err := exec(cl, "layout", v)
				if err != nil || got != want["layout"] {
					ck(false, fmt.Sprintf("concurrent %d/%d out=%q err=%v", w, i, got, err))
				}
			}
		}(w, cl)
	}
	wg.Wait()

	// 9) 最终 GC 后复验 base 克隆与关键输出。
	gcStress()
	cl, err := base.Clone()
	ck(err == nil, fmt.Sprintf("post-GC clone err=%v", err))
	if err == nil {
		live = append(live, cl)
		got, err := exec(cl, "layout", v)
		ck(err == nil && got == want["layout"], fmt.Sprintf("post-GC layout=%q err=%v", got, err))
	}

	finish("l16_html_clone_context")
}
