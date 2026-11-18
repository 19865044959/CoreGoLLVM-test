// L16: html/template 的 JS/CSS/URL/srcset 多上下文转义与安全类型；GC 后模板树与 golden 一致。
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

type escData struct {
	Color, Bg, JS, Re, Href, Path, Src string
	SafeURL                            template.URL
	SafeJS                             template.JS
	SafeCSS                            template.CSS
	SafeHTML                           template.HTML
}

const pageSrc = `<style>p{color:{{.Color}};background-image:url('{{.Bg}}')}</style>` +
	`<script>var s="{{.JS}}";var r=new RegExp("{{.Re}}");</script>` +
	`<a href="{{.Href}}">x</a>` +
	`<a href="/p/{{.Path}}?q={{.Path}}">y</a>` +
	`<img srcset="{{.Src}} 1x" alt="z">` +
	`<a href="{{.SafeURL}}">s</a><script>var t={{.SafeJS}};</script>` +
	`<style>.x{color:{{.SafeCSS}}}</style><div>{{.SafeHTML}}</div>`

// golden 由 Go 1.24.1 html/template 对下面三组输入的实际输出固定，逐字节比较。
const golden0 = "<style>p{color:ZgotmplZ;background-image:url('#ZgotmplZ')}</style><script>var s=\"\\u003c\\/script\\u003e\\u003cscript\\u003ealert(\\u0022x\\u0022)\\u003c\\/script\\u003e\";var r=new RegExp(\"a\\/b\\u0022c\\\\\\u003cd\\u003e\");</script><a href=\"#ZgotmplZ\">x</a><a href=\"/p/a%20b/c?d&amp;e=f#g?q=a%20b%2fc%3fd%26e%3df%23g\">y</a><img srcset=\"https://e/a.png 1x,#ZgotmplZ 1x\" alt=\"z\"><a href=\"/ok?a=1&amp;b=2\">s</a><script>var t=alert(\"hi\");</script><style>.x{color:red; background: blue}</style><div><b>bold</b></div>"

const golden1 = "<style>p{color:ZgotmplZ;background-image:url('//host/a.png')}</style><script>var s=\"plain \\u0022q\\u0022 \\\\ end\";var r=new RegExp(\"ab\\u002bc(d)\");</script><a href=\"/ok?x=1&amp;y=2\">x</a><a href=\"/p/x/y?q=x%2fy\">y</a><img srcset=\"https://e/b.png 2x 1x\" alt=\"z\"><a href=\"/s?x=1&amp;y=2\">s</a><script>var t=1+2;</script><style>.x{color:blue}</style><div><i>i</i></div>"

const golden2 = "<style>p{color:blue;background-image:url('https://e/a%20b.png')}</style><script>var s=\"line1\\nline2 end\";var r=new RegExp(\"a{1,3}\");</script><a href=\"mailto:a@b\">x</a><a href=\"/p//?q=%2f\">y</a><img srcset=\"https://e/c.png 3x, /rel/d.png 4x 1x\" alt=\"z\"><a href=\"mailto:a@b\">s</a><script>var t=var x=1;;</script><style>.x{color:background:url(/x.png)}</style><div><u>u</u></div>"

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

func variants() []escData {
	return []escData{
		{
			Color: `red;x:expression(alert(1))`, Bg: `javascript:alert(1)`,
			JS: "</script><script>alert(\"x\")</script>", Re: `a/b"c\<d>`,
			Href: `javascript:alert(1)`, Path: `a b/c?d&e=f#g`,
			Src:     `https://e/a.png 1x, javascript:alert(1) 2x`,
			SafeURL: template.URL(`/ok?a=1&b=2`), SafeJS: template.JS(`alert("hi")`),
			SafeCSS: template.CSS(`red; background: blue`), SafeHTML: template.HTML(`<b>bold</b>`),
		},
		{
			Color: `rgb(1,2,3)`, Bg: `//host/a.png`, JS: "plain \"q\" \\ end",
			Re: `ab+c(d)`, Href: `/ok?x=1&y=2`, Path: `x/y`,
			Src:     `https://e/b.png 2x`,
			SafeURL: template.URL(`/s?x=1&y=2`), SafeJS: template.JS(`1+2`),
			SafeCSS: template.CSS(`blue`), SafeHTML: template.HTML(`<i>i</i>`),
		},
		{
			Color: `blue`, Bg: `https://e/a b.png`, JS: "line1\nline2 end",
			Re: `a{1,3}`, Href: `mailto:a@b`, Path: `/`,
			Src:     `https://e/c.png 3x, /rel/d.png 4x`,
			SafeURL: template.URL(`mailto:a@b`), SafeJS: template.JS(`var x=1;`),
			SafeCSS: template.CSS(`background:url(/x.png)`), SafeHTML: template.HTML(`<u>u</u>`),
		},
	}
}

func main() {
	uafSeed = envSeed()
	rounds := tortureRounds(12 + int(uafSeed%7))
	goldens := []string{golden0, golden1, golden2}
	zcount := []int{4, 1, 0}
	data := variants()

	rng := rand.New(rand.NewSource(uafSeed))
	for r := 0; r < rounds; r++ {
		idx := rng.Intn(len(data))
		d := data[idx]
		want := goldens[idx]

		t, err := template.New("ctx").Parse(pageSrc)
		ck(err == nil, fmt.Sprintf("round %d parse: %v", r, err))
		if err != nil {
			continue
		}
		live = append(live[:0], t)

		var sb strings.Builder
		err = t.Execute(&sb, d)
		ck(err == nil, fmt.Sprintf("round %d execute: %v", r, err))
		out := sb.String()
		ck(out == want, fmt.Sprintf("round %d variant %d output\n got: %q\nwant: %q", r, idx, out, want))
		ck(strings.Count(out, "ZgotmplZ") == zcount[idx],
			fmt.Sprintf("round %d variant %d ZgotmplZ count=%d want %d", r, idx, strings.Count(out, "ZgotmplZ"), zcount[idx]))
		ck(!strings.Contains(out, "var s=\"</script>"), fmt.Sprintf("round %d raw JS close tag leaked", r))
		if idx == 0 {
			ck(strings.Contains(out, `\u003c\/script\u003e`), fmt.Sprintf("round %d JS string not unicode-escaped", r))
			ck(strings.Contains(out, `?q=a%20b%2fc%3fd%26e%3df%23g`), fmt.Sprintf("round %d URL query not escaped", r))
			ck(strings.Contains(out, `color:ZgotmplZ`), fmt.Sprintf("round %d unsafe CSS value not filtered", r))
		}

		gcStress()

		ck(d.SafeHTML == template.HTML(`<b>bold</b>`) || idx != 0, fmt.Sprintf("round %d safe HTML value mutated", r))
		ck(d.SafeURL == template.URL(`/ok?a=1&b=2`) || idx != 0, fmt.Sprintf("round %d safe URL value mutated", r))
		sb.Reset()
		err = t.Execute(&sb, d)
		ck(err == nil, fmt.Sprintf("round %d execute after GC: %v", r, err))
		ck(sb.String() == want, fmt.Sprintf("round %d variant %d output after GC\n got: %q\nwant: %q", r, idx, sb.String(), want))
	}

	finish("l16_css_js_url_escape")
}
