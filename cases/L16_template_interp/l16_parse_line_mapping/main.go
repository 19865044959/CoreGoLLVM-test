// L16: text/template 多行/CRLF/Unicode/define 场景下的 parse 错误行号与 Must/execute 错误映射，跨 GC。
package main

import (
	"fmt"
	"math/rand"
	"os"
	"runtime"
	"strconv"
	"strings"
	"sync/atomic"
	"text/template"
)

type parseCase struct {
	name string
	src  string
	line int
	want string
}

var cases = []parseCase{
	{"unclosed", "ok\n{{.Name\n", 3, "unclosed action started at unclosed:2"},
	{"missingif", "l1\nl2\nl3\n{{if}}\nx\n{{end}}\n", 4, "missing value for if"},
	{"unicode", "中文\né\n{{nosuch .}}\n", 3, `function "nosuch" not defined`},
	{"crlf", "a\r\nb\r\n{{if}}\r\n{{end}}\r\n", 3, "missing value for if"},
	{"define", "{{define \"d\"}}\nline2\n{{if}}\n{{end}}\n", 3, "missing value for if"},
	{"define2", "{{define \"d\"}}ok{{end}}\n\n{{define \"e\"}}\n{{range $x :=}}{{end}}\n", 4, "missing value for range"},
	{"comment", "a\n{{/* c */}}\n{{end}}", 3, "unexpected {{end}}"},
	{"empty", "\n\n{{}}", 3, "missing value for command"},
	{"eof", "a\n{{if .X}}\nbody\n", 4, "unexpected EOF"},
	{"comment2", "a\n{{/* line3\nline4", 2, "unclosed comment"},
}

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

func catchPanic(fn func()) (msg string, panicked bool) {
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

func renderNamed(t *template.Template, name string) (string, error) {
	var sb strings.Builder
	err := t.ExecuteTemplate(&sb, name, nil)
	return sb.String(), err
}

func main() {
	uafSeed = envSeed()
	rng := rand.New(rand.NewSource(uafSeed))

	base := template.Must(template.New("root").Parse(`{{define "good"}}G{{end}}R`))
	execT := template.Must(template.New("exec").Parse("l1\n{{template \"nope\"}}\n"))
	live = append(live, base, execT)

	firstErr := make(map[string]string, len(cases))
	rounds := 2 + int(uafSeed%2)
	for r := 0; r < rounds; r++ {
		for _, idx := range rng.Perm(len(cases)) {
			tc := cases[idx]
			t, err := template.New(tc.name).Parse(tc.src)
			ck(t == nil, fmt.Sprintf("round %d %s: failed parse returned non-nil template", r, tc.name))
			ck(err != nil, fmt.Sprintf("round %d %s: expected parse error, got none", r, tc.name))
			if err == nil {
				continue
			}
			msg := err.Error()
			ck(strings.Contains(msg, "template: "+tc.name+":"),
				fmt.Sprintf("round %d %s: missing template prefix: %q", r, tc.name, msg))
			ck(strings.Contains(msg, fmt.Sprintf("%s:%d:", tc.name, tc.line)),
				fmt.Sprintf("round %d %s: missing line %d: %q", r, tc.name, tc.line, msg))
			ck(strings.Contains(msg, tc.want),
				fmt.Sprintf("round %d %s: error=%q missing %q", r, tc.name, msg, tc.want))
			if r == 0 {
				firstErr[tc.name] = msg
			} else {
				ck(msg == firstErr[tc.name], fmt.Sprintf("round %d %s: error changed: %q", r, tc.name, msg))
			}

			pmsg, panicked := catchPanic(func() {
				template.Must(template.New(tc.name).Parse(tc.src))
			})
			ck(panicked, fmt.Sprintf("round %d %s: template.Must did not panic", r, tc.name))
			ck(pmsg == msg, fmt.Sprintf("round %d %s: Must panic %q != Parse error %q", r, tc.name, pmsg, msg))
		}

		// 失败的 Parse 不破坏同一模板集合中已注册的模板。
		t2, perr := base.Parse("bad\n{{if}}")
		ck(t2 == nil, fmt.Sprintf("round %d failed Parse returned non-nil template", r))
		ck(perr != nil, fmt.Sprintf("round %d failed Parse returned nil error", r))
		if perr != nil {
			ck(strings.Contains(perr.Error(), "root:2:"), fmt.Sprintf("round %d failed Parse line=%q", r, perr.Error()))
		}
		got, e := renderNamed(base, "good")
		ck(e == nil && got == "G", fmt.Sprintf("round %d good template after failed Parse=(%q,%v)", r, got, e))
		got, e = renderNamed(base, "root")
		ck(e == nil && got == "R", fmt.Sprintf("round %d root template after failed Parse=(%q,%v)", r, got, e))

		// 执行期错误带模板名与行号，错误点之前的输出保留。
		var sb strings.Builder
		eExec := execT.Execute(&sb, nil)
		ck(eExec != nil, fmt.Sprintf("round %d missing template execute returned nil error", r))
		if eExec != nil {
			ck(strings.Contains(eExec.Error(), "exec:2:"), fmt.Sprintf("round %d execute error missing line: %q", r, eExec.Error()))
			ck(strings.Contains(eExec.Error(), "not defined"), fmt.Sprintf("round %d execute error=%q", r, eExec.Error()))
		}
		ck(sb.String() == "l1\n", fmt.Sprintf("round %d execute partial output=%q", r, sb.String()))
		if r == 0 && eExec != nil {
			firstErr["/exec"] = eExec.Error()
		}

		gcStress()

		// GC 后错误文本与可执行模板均保持稳定。
		for _, tc := range cases {
			_, err := template.New(tc.name).Parse(tc.src)
			ck(err != nil && err.Error() == firstErr[tc.name],
				fmt.Sprintf("round %d %s: error after GC=%v", r, tc.name, err))
		}
		got, e = renderNamed(base, "good")
		ck(e == nil && got == "G", fmt.Sprintf("round %d good after GC=(%q,%v)", r, got, e))
		sb.Reset()
		eExec = execT.Execute(&sb, nil)
		ck(eExec != nil && firstErr["/exec"] != "" && eExec.Error() == firstErr["/exec"],
			fmt.Sprintf("round %d exec error after GC=%v", r, eExec))
		ck(sb.String() == "l1\n", fmt.Sprintf("round %d exec output after GC=%q", r, sb.String()))
	}

	finish("l16_parse_line_mapping")
}
