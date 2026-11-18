// L16: text/template 解析错误与 html/template 执行期上下文错误：错误文本、位置、稳定性和 GC。
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
	texttemplate "text/template"
)

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

// parseErrText 解析固定错误文本并在注释里说明其来源行号。
type parseErrText struct {
	name string
	src  string
	line int
	want string
}

var textErrCases = []parseErrText{
	{"unclosed_action", "ok\n{{.Name\n", 3, "unclosed action started at unclosed_action:2"},
	{"unexpected_end", "{{end}}", 1, "unexpected {{end}}"},
	{"undefined_func", "line1\nline2 {{nosuchfunc .}}\n", 2, `function "nosuchfunc" not defined`},
	{"bad_if", "{{if}}x{{end}}", 1, "missing value for if"},
	{"unclosed_if", "{{if .A}}x\n", 2, "unexpected EOF"},
	{"bad_range", "{{range $x :=}}{{end}}", 1, "missing value for range"},
	{"bad_number", "{{1.2.3}}", 1, `unexpected ".3" in operand`},
	{"empty_action", "{{}}", 1, "missing value for command"},
	{"unclosed_comment", "{{/* unclosed", 1, "unclosed comment"},
	{"define_no_name", "{{define}}x{{end}}", 1, `unexpected "}}" in define clause`},
}

// htmlErrCase 的 Parse 成功，首次 Execute 时由转义上下文分析报错。
type htmlErrCase struct {
	name string
	src  string
	want string
}

var htmlErrCases = []htmlErrCase{
	{"ambig_url", `<a href="{{if .F}}/foo?a={{else}}/bar/{{end}}{{.H}}">`, "appears in an ambiguous context within a URL"},
	{"nontext_attr", `<a b=1 c={{.H}}`, "ends in a non-text context"},
	{"unclosed_script", `<script>foo();`, "ends in a non-text context: {stateJS"},
	{"range_context", `{{range .Items}}<a{{if .X}}{{continue}}{{end}}>{{end}}`, "branches end in different contexts"},
	{"bad_attr", `<input type=button value=onclick=>`, `"=" in unquoted attr`},
	{"urlquery_mid", `Hello, {{. | urlquery | print}}!`, `predefined escaper "urlquery" disallowed`},
	{"no_template", `{{template "foo"}}`, `no such template "foo"`},
}

func mustPanicErr(fn func()) (msg string, panicked bool) {
	defer func() {
		if r := recover(); r != nil {
			panicked = true
			if e, ok := r.(error); ok {
				msg = e.Error()
			} else {
				msg = fmt.Sprint(r)
			}
		}
	}()
	fn()
	return "", false
}

func main() {
	uafSeed = envSeed()
	rounds := 3 + int(uafSeed%3)
	rng := rand.New(rand.NewSource(uafSeed))

	for r := 0; r < rounds; r++ {
		type liveHTML struct {
			t    *template.Template
			want string
		}
		var roundTpls []liveHTML
		for _, idx := range rng.Perm(len(textErrCases)) {
			tc := textErrCases[idx]
			t, err := texttemplate.New(tc.name).Parse(tc.src)
			ck(t == nil, fmt.Sprintf("round %d %s: failed parse returned non-nil template", r, tc.name))
			ck(err != nil, fmt.Sprintf("round %d %s: expected parse error, got none", r, tc.name))
			if err == nil {
				continue
			}
			msg := err.Error()
			ck(strings.Contains(msg, "template: "+tc.name+":"),
				fmt.Sprintf("round %d %s error lacks template prefix: %q", r, tc.name, msg))
			ck(strings.Contains(msg, fmt.Sprintf("%s:%d:", tc.name, tc.line)),
				fmt.Sprintf("round %d %s error lacks line %d: %q", r, tc.name, tc.line, msg))
			ck(strings.Contains(msg, tc.want), fmt.Sprintf("round %d %s error=%q missing %q", r, tc.name, msg, tc.want))

			pmsg, panicked := mustPanicErr(func() {
				texttemplate.Must(texttemplate.New(tc.name).Parse(tc.src))
			})
			ck(panicked, fmt.Sprintf("round %d %s: template.Must did not panic", r, tc.name))
			ck(pmsg == msg, fmt.Sprintf("round %d %s: Must panic %q != Parse error %q", r, tc.name, pmsg, msg))
		}

		for _, idx := range rng.Perm(len(htmlErrCases)) {
			hc := htmlErrCases[idx]
			t, err := template.New(hc.name).Parse(hc.src)
			ck(err == nil, fmt.Sprintf("round %d %s: Parse unexpectedly failed: %v", r, hc.name, err))
			if err != nil {
				continue
			}
			live = append(live, t)
			roundTpls = append(roundTpls, liveHTML{t: t, want: hc.want})

			var sb strings.Builder
			e1 := t.Execute(&sb, map[string]any{
				"F": true, "H": "x",
				"Items": []map[string]any{{"X": true}},
			})
			ck(e1 != nil, fmt.Sprintf("round %d %s: expected escape error at Execute, got none (out=%q)", r, hc.name, sb.String()))
			if e1 != nil {
				msg := e1.Error()
				ck(strings.Contains(msg, hc.name), fmt.Sprintf("round %d %s error lacks template name: %q", r, hc.name, msg))
				ck(strings.Contains(msg, hc.want), fmt.Sprintf("round %d %s error=%q missing %q", r, hc.name, msg, hc.want))
			}
			ck(sb.Len() == 0, fmt.Sprintf("round %d %s wrote output before escape error: %q", r, hc.name, sb.String()))

			var sb2 strings.Builder
			e2 := t.Execute(&sb2, map[string]any{
				"F": true, "H": "x",
				"Items": []map[string]any{{"X": true}},
			})
			ck(e2 != nil && e1 != nil && e2.Error() == e1.Error(),
				fmt.Sprintf("round %d %s escape error not stable", r, hc.name))
		}

		gcStress()

		// 已保存的 html 模板在 GC 后再次执行，转义错误必须原样复现。
		for _, lh := range roundTpls {
			var sb strings.Builder
			e := lh.t.Execute(&sb, map[string]any{"F": true, "H": "x", "Items": []map[string]any{{"X": true}}})
			ck(e != nil && strings.Contains(e.Error(), lh.want),
				fmt.Sprintf("round %d stored template %s error after GC: %v", r, lh.t.Name(), e))
		}
	}

	// GC 后重新解析/执行，错误文本必须与之前一致。
	for _, tc := range textErrCases {
		_, err := texttemplate.New(tc.name).Parse(tc.src)
		ck(err != nil && strings.Contains(err.Error(), tc.want),
			fmt.Sprintf("%s: error changed after GC: %v", tc.name, err))
	}
	for _, hc := range htmlErrCases {
		t, err := template.New(hc.name).Parse(hc.src)
		ck(err == nil, fmt.Sprintf("%s: Parse after GC failed: %v", hc.name, err))
		if err != nil {
			continue
		}
		var sb strings.Builder
		e := t.Execute(&sb, map[string]any{"F": true, "H": "x", "Items": []map[string]any{{"X": true}}})
		ck(e != nil && strings.Contains(e.Error(), hc.want),
			fmt.Sprintf("%s: escape error changed after GC: %v", hc.name, e))
	}

	finish("l16_parse_errors")
}
