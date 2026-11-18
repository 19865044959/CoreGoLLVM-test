// L16: 同一模板源码分别由 text/template 与 html/template 执行，对比转义/安全类型差异；跨 GC。
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
	texttmpl "text/template"
)

type escData struct {
	Attr, Text, Query, BadURL, JSString string
	SafeJS                              template.JS
	SafeURL                             template.URL
	SafeHTML                            template.HTML
	HTMLString                          string
	SafeCSS                             template.CSS
}

const src = `<p title="{{.Attr}}">{{.Text}}</p>` +
	`<a href="/q?x={{.Query}}">q</a>` +
	`<a href="{{.BadURL}}">bad</a>` +
	`<script>var a={{.JSString}};var b={{.SafeJS}};</script>` +
	`<img srcset="{{.SafeURL}} 1x">` +
	`<div>{{.SafeHTML}}</div>` +
	`<em>{{.HTMLString}}</em>` +
	`<style>p{color:{{.SafeCSS}}}</style>`

const wantHTML = `<p title="x&#34;&lt;y&gt;">&lt;b&gt;&amp;&#34;</p>` +
	`<a href="/q?x=a%20b%26c%2fd%3f">q</a>` +
	`<a href="#ZgotmplZ">bad</a>` +
	`<script>var a="a\"b\u003cc\u003e";var b=alert(1);</script>` +
	`<img srcset="/img?a=1&amp;b=2 1x">` +
	`<div><i>ok</i></div>` +
	`<em>&lt;i&gt;plain&lt;/i&gt;</em>` +
	`<style>p{color:red; background: blue}</style>`

const wantText = `<p title="x"<y>"><b>&"</p>` +
	`<a href="/q?x=a b&c/d?">q</a>` +
	`<a href="javascript:alert(1)">bad</a>` +
	`<script>var a=a"b<c>;var b=alert(1);</script>` +
	`<img srcset="/img?a=1&b=2 1x">` +
	`<div><i>ok</i></div>` +
	`<em><i>plain</i></em>` +
	`<style>p{color:red; background: blue}</style>`

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

func data() escData {
	return escData{
		Attr: `x"<y>`, Text: `<b>&"`, Query: `a b&c/d?`, BadURL: `javascript:alert(1)`,
		JSString: `a"b<c>`, SafeJS: template.JS(`alert(1)`), SafeURL: template.URL(`/img?a=1&b=2`),
		SafeHTML: template.HTML(`<i>ok</i>`), HTMLString: `<i>plain</i>`,
		SafeCSS: template.CSS(`red; background: blue`),
	}
}

func renderHTML(t *template.Template, d escData) (string, error) {
	var sb strings.Builder
	err := t.Execute(&sb, d)
	return sb.String(), err
}

func renderText(t *texttmpl.Template, d escData) (string, error) {
	var sb strings.Builder
	err := t.Execute(&sb, d)
	return sb.String(), err
}

func main() {
	uafSeed = envSeed()
	rounds := 6 + int(uafSeed%4)
	d := data()
	rng := rand.New(rand.NewSource(uafSeed))

	for r := 0; r < rounds; r++ {
		ht, err := template.New("h").Parse(src)
		ck(err == nil, fmt.Sprintf("round %d parse html: %v", r, err))
		xt, err := texttmpl.New("t").Parse(src)
		ck(err == nil, fmt.Sprintf("round %d parse text: %v", r, err))
		if ht == nil || xt == nil {
			continue
		}
		live = []any{ht, xt, d.SafeJS, d.SafeURL, d.SafeHTML, d.SafeCSS}

		for _, idx := range rng.Perm(2) {
			if idx == 0 {
				out, err := renderHTML(ht, d)
				ck(err == nil, fmt.Sprintf("round %d execute html: %v", r, err))
				ck(out == wantHTML, fmt.Sprintf("round %d html output\n got: %q\nwant: %q", r, out, wantHTML))
				ck(out != wantText, fmt.Sprintf("round %d html unexpectedly equals text output", r))
				for _, want := range []string{"#ZgotmplZ", "&lt;b&gt;", "&amp;", `<`, "&lt;i&gt;plain&lt;/i&gt;", "<i>ok</i>"} {
					ck(strings.Contains(out, want), fmt.Sprintf("round %d html missing %q", r, want))
				}
				for _, forb := range []string{`<b>&"`, "javascript:alert(1)", "<i>plain</i>"} {
					ck(!strings.Contains(out, forb), fmt.Sprintf("round %d html leaked %q", r, forb))
				}
			} else {
				out, err := renderText(xt, d)
				ck(err == nil, fmt.Sprintf("round %d execute text: %v", r, err))
				ck(out == wantText, fmt.Sprintf("round %d text output\n got: %q\nwant: %q", r, out, wantText))
				for _, want := range []string{`<b>&"`, "javascript:alert(1)", "<i>plain</i>", "<i>ok</i>"} {
					ck(strings.Contains(out, want), fmt.Sprintf("round %d text missing raw %q", r, want))
				}
				ck(!strings.Contains(out, "&lt;b&gt;"), fmt.Sprintf("round %d text escaped HTML", r))
				ck(!strings.Contains(out, "#ZgotmplZ"), fmt.Sprintf("round %d text filtered URL", r))
			}
		}

		ck(d.SafeHTML == template.HTML(`<i>ok</i>`), fmt.Sprintf("round %d SafeHTML mutated", r))
		ck(d.SafeURL == template.URL(`/img?a=1&b=2`), fmt.Sprintf("round %d SafeURL mutated", r))
		ck(d.SafeJS == template.JS(`alert(1)`), fmt.Sprintf("round %d SafeJS mutated", r))
		ck(d.SafeCSS == template.CSS(`red; background: blue`), fmt.Sprintf("round %d SafeCSS mutated", r))

		gcStress()

		out, err := renderHTML(ht, d)
		ck(err == nil, fmt.Sprintf("round %d html after GC: %v", r, err))
		ck(out == wantHTML, fmt.Sprintf("round %d html output after GC\n got: %q\nwant: %q", r, out, wantHTML))
		out, err = renderText(xt, d)
		ck(err == nil, fmt.Sprintf("round %d text after GC: %v", r, err))
		ck(out == wantText, fmt.Sprintf("round %d text output after GC\n got: %q\nwant: %q", r, out, wantText))
	}

	finish("l16_html_vs_text_escape")
}
