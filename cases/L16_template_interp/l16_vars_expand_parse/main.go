// L16: 变量展开与模板解析两阶段边界：{var} 替换保留 {{...}} 动作，动作内的未加引号
// 变量可展开、引号/注释内不展开；展开值注入动作后由 text/template 解析/执行，错误定位稳定。
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
	"unicode"
)

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
		b := make([]byte, 8192)
		b[0] = byte(i)
		gcSink = append(gcSink[:0], b)
	}
	for i := 0; i < 128; i++ {
		_ = strings.Repeat("x", 256)
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

func validName(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '_' {
			return false
		}
	}
	return true
}

// expand 展开 {name}，同时保留 {{...}} 动作：动作内引号/注释原样保留，
// 动作内未加引号的 {name} 仍展开；非法名字原样保留，未定义变量记录首个错误。
func expand(src string, vars map[string]string) (string, error) {
	var b strings.Builder
	var firstErr error
	setErr := func(format string, args ...any) {
		if firstErr == nil {
			firstErr = fmt.Errorf(format, args...)
		}
	}
	expandOne := func(i int) (int, bool) {
		rel := strings.IndexByte(src[i+1:], '}')
		if rel < 0 {
			setErr("unclosed variable at byte %d", i)
			return -1, false
		}
		j := i + 1 + rel
		name := src[i+1 : j]
		if !validName(name) {
			b.WriteString(src[i : j+1])
			return j + 1, true
		}
		if v, ok := vars[name]; ok {
			b.WriteString(v)
		} else {
			b.WriteString(src[i : j+1])
			setErr("undefined variable %q at byte %d", name, i)
		}
		return j + 1, true
	}

	inAction := false
	inComment := false
	var quote byte
	for i := 0; i < len(src); {
		c := src[i]
		if inAction {
			if inComment {
				if strings.HasPrefix(src[i:], "*/") {
					b.WriteString("*/")
					inComment = false
					i += 2
					continue
				}
				b.WriteByte(c)
				i++
				continue
			}
			if quote != 0 {
				b.WriteByte(c)
				if c == '\\' && quote != '`' && i+1 < len(src) {
					b.WriteByte(src[i+1])
					i += 2
					continue
				}
				if c == quote {
					quote = 0
				}
				i++
				continue
			}
			switch {
			case strings.HasPrefix(src[i:], "/*"):
				b.WriteString("/*")
				inComment = true
				i += 2
			case c == '"' || c == '\'' || c == '`':
				quote = c
				b.WriteByte(c)
				i++
			case strings.HasPrefix(src[i:], "}}"):
				b.WriteString("}}")
				inAction = false
				i += 2
			case strings.HasPrefix(src[i:], "{{"):
				b.WriteString("{{")
				i += 2
			case c == '{':
				next, ok := expandOne(i)
				if !ok {
					return b.String(), firstErr
				}
				i = next
			default:
				b.WriteByte(c)
				i++
			}
			continue
		}
		switch {
		case strings.HasPrefix(src[i:], "{{"):
			b.WriteString("{{")
			inAction = true
			i += 2
		case c == '{':
			next, ok := expandOne(i)
			if !ok {
				return b.String(), firstErr
			}
			i = next
		case c == '}':
			setErr("unmatched %q at byte %d", "}", i)
			b.WriteByte('}')
			i++
		default:
			b.WriteByte(c)
			i++
		}
	}
	if inAction {
		setErr("unclosed template action")
	}
	return b.String(), firstErr
}

type tmplCase struct {
	name    string
	src     string
	want    string
	parseOK bool
}

func main() {
	uafSeed = envSeed()
	rng := rand.New(rand.NewSource(uafSeed))
	vars := map[string]string{
		"name": "world",
		"n":    "7",
		"cond": "true",
		"expr": "{{.N}}",
		"ok":   "{{if true}}ok{{end}}",
		"q":    `"`,
		"bad":  "{{",
		"one":  "11",
		"二":    "22",
		"br":   "a{b}",
	}
	data := map[string]any{"N": 7, "M": map[string]string{"a{b}": "a{b}"}}

	good := []tmplCase{
		{"text", "Hello {name}, n={n}", "Hello world, n=7", true},
		{"action", "{{if {cond}}}{name}{{else}}no{{end}}", "world", true},
		{"expr-inject", "Value={expr}", "Value=7", true},
		{"ok-inject", "R=[{ok}]", "R=[ok]", true},
		{"quoted", `{{index .M "a{b}"}}`, "a{b}", true},
		{"quoted-var", `{{"{name}"}}`, "{name}", true},
		{"comment", `{{/* keep {name} inside action */}}X`, "X", true},
		{"unicode", "中文{二}/{one}", "中文22/11", true},
		{"value-braces", "v={br}", "v=a{b}", true},
		{"multiline", "a\n{name}\nb", "a\nworld\nb", true},
	}

	// 1) 成功路径：展开 -> 解析 -> 执行，结果确定。
	type result struct {
		out     string
		expErr  string
		parseEG string
	}
	results := map[string]result{}
	run := func(c tmplCase) result {
		expanded, eerr := expand(c.src, vars)
		if eerr != nil {
			return result{expErr: eerr.Error()}
		}
		t, perr := template.New(c.name).Parse(expanded)
		if perr != nil {
			return result{expErr: eerrString(eerr), parseEG: perr.Error()}
		}
		var b strings.Builder
		exErr := t.Execute(&b, data)
		r := result{out: b.String(), expErr: eerrString(eerr)}
		if exErr != nil {
			r.parseEG = exErr.Error()
		}
		return r
	}
	for _, c := range good {
		r := run(c)
		ck(r.expErr == "" && r.parseEG == "" && r.out == c.want,
			fmt.Sprintf("%s: out=%q expErr=%q execErr=%q want=%q", c.name, r.out, r.expErr, r.parseEG, c.want))
		results[c.name] = r
		if r.out != "" {
			live = append(live, r.out)
		}
	}

	// 2) 未定义变量：展开阶段报错并保留原文，不进入模板解析。
	exp, eerr := expand("x {missing} y", vars)
	ck(eerr != nil && strings.Contains(eerr.Error(), `undefined variable "missing"`),
		fmt.Sprintf("undefined var err=%v out=%q", eerr, exp))
	ck(exp == "x {missing} y", fmt.Sprintf("undefined var output=%q", exp))

	// 3) 多行定位：第 2 行的未定义变量报告正确字节位置。
	multi := "line1\n{missing}\nline3"
	_, merr := expand(multi, vars)
	ck(merr != nil && strings.Contains(merr.Error(), "at byte 6"), fmt.Sprintf("multiline err=%v", merr))

	// 4) 展开值注入损坏动作：引号注入导致模板解析错误，错误行稳定。
	qSrc := "first\n{{printf \"%s\" {q}}}"
	qExp, qErr := expand(qSrc, vars)
	ck(qErr == nil, fmt.Sprintf("quote expand err=%v", qErr))
	_, perr := template.New("q").Parse(qExp)
	ck(perr != nil && strings.Contains(perr.Error(), "unterminated quoted string"),
		fmt.Sprintf("quote parse err=%v expanded=%q", perr, qExp))
	ck(perr != nil && strings.Contains(perr.Error(), "q:2:"), fmt.Sprintf("quote parse location=%v", perr))

	// bad 值注入未闭合动作，解析错误落在第 2 行。
	bSrc := "first\n{bad}x"
	bExp, bErr := expand(bSrc, vars)
	ck(bErr == nil && bExp == "first\n{{x", fmt.Sprintf("bad expand out=%q err=%v", bExp, bErr))
	_, perr = template.New("b").Parse(bExp)
	ck(perr != nil && strings.Contains(perr.Error(), "b:2:"), fmt.Sprintf("bad action parse err=%v", perr))

	// 5) 未闭合动作/变量在展开阶段报错。
	_, uerr := expand("start {{if .N", vars)
	ck(uerr != nil && strings.Contains(uerr.Error(), "unclosed template action"), fmt.Sprintf("unclosed action err=%v", uerr))
	_, verr := expand("start {name", vars)
	ck(verr != nil && strings.Contains(verr.Error(), "unclosed variable"), fmt.Sprintf("unclosed var err=%v", verr))

	// 6) 两阶段：展开值不会在同一次调用中再展开，第二次才生效。
	two := map[string]string{"a": "{name}", "name": "world"}
	o1, e1 := expand("{a}", two)
	o2, e2 := expand(o1, two)
	ck(e1 == nil && o1 == "{name}", fmt.Sprintf("pass1=%q err=%v", o1, e1))
	ck(e2 == nil && o2 == "world", fmt.Sprintf("pass2=%q err=%v", o2, e2))

	// 7) GC 后按种子顺序重放全部成功模板，输出与首次一致。
	names := make([]string, 0, len(good))
	for _, c := range good {
		names = append(names, c.name)
	}
	for round := 0; round < 3; round++ {
		gcStress()
		for _, idx := range rng.Perm(len(good)) {
			c := good[idx]
			r := run(c)
			ck(r.expErr == "" && r.parseEG == "" && r.out == results[c.name].out,
				fmt.Sprintf("round %d %s out=%q err=%q/%q", round, c.name, r.out, r.expErr, r.parseEG))
		}
		ck(len(live) > 0, fmt.Sprintf("round %d retained outputs lost", round))
		ck(vars["二"] == "22", fmt.Sprintf("round %d vars changed", round))
	}

	finish("l16_vars_expand_parse")
}

func eerrString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}
