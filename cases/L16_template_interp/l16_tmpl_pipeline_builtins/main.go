// L16: text/template 管道与内建函数：管道参数顺序、call/index/slice/printf/
// html/js/urlquery、变参 FuncMap、dict 合并、短路求值与错误分类；跨 GC 重放。
package main

import (
	"fmt"
	"math/rand"
	"os"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync/atomic"
	"text/template"
)

type item struct {
	Name  string
	Value int64
}

type view struct {
	List []int
	Map  map[string]string
	Fn   func(int) string
	Join func(string, string) string
	Node *item
	N    int
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
		_ = &item{Name: "g", Value: int64(i)}
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

func panics(f func()) (p bool) {
	defer func() {
		if recover() != nil {
			p = true
		}
	}()
	f()
	return false
}

func dict(args ...any) (map[string]any, error) {
	m := map[string]any{}
	for i := 0; i < len(args); i += 2 {
		if i+1 >= len(args) {
			return nil, fmt.Errorf("dict requires an even number of arguments, got %d", len(args))
		}
		key, ok := args[i].(string)
		if !ok {
			return nil, fmt.Errorf("dict keys must be strings, got %T", args[i])
		}
		if key == "." {
			switch src := args[i+1].(type) {
			case map[string]any:
				for k, v := range src {
					m[k] = v
				}
			case map[string]int:
				for k, v := range src {
					m[k] = v
				}
			case nil:
			default:
				return nil, fmt.Errorf("dict merge value must be a map, got %T", args[i+1])
			}
			continue
		}
		m[key] = args[i+1]
	}
	return m, nil
}

func funcMap() template.FuncMap {
	return template.FuncMap{
		"add": func(a, b int) int { return a + b },
		"sub": func(a, b int) int { return a - b },
		"mul": func(a, b int) int { return a * b },
		"div": func(a, b int) (int, error) {
			if b == 0 {
				return 0, fmt.Errorf("division by zero")
			}
			return a / b, nil
		},
		"wrap":  func(prefix, suffix, s string) string { return prefix + s + suffix },
		"upper": func(s string) string { return strings.ToUpper(s) },
		"joinAny": func(sep string, items ...any) string {
			parts := make([]string, len(items))
			for i, it := range items {
				parts[i] = fmt.Sprint(it)
			}
			return strings.Join(parts, sep)
		},
		"list": func(args ...any) []any { return args },
		"dict": dict,
		"boom": func() string { panic("boom called") },
		"nodeName": func(n *item) string {
			if n == nil {
				return "<nil>"
			}
			return n.Name
		},
	}
}

type tmplCase struct {
	name string
	body string
	want string
}

type errCase struct {
	name string
	body string
	want string
}

func main() {
	uafSeed = envSeed()
	fm := funcMap()
	data := &view{
		List: []int{1, 2, 3, 4},
		Map:  map[string]string{"k": "v"},
		Fn:   func(x int) string { return fmt.Sprintf("fn%d", x) },
		Join: func(a, b string) string { return a + "-" + b },
		Node: &item{Name: "alpha", Value: 41},
		N:    5,
	}

	success := []tmplCase{
		{"pipe-order", `{{ 10 | sub 3 }}|{{ "a" | wrap "<" ">" }}|{{ 2 | add 3 | mul 4 }}|{{ add 1 2 | mul 3 | sub 10 }}`, "-7|<a>|20|1"},
		{"printf", `{{ printf "%d:%d" (add 2 3) 7 }}`, "5:7"},
		{"builtins", `{{len .List}}|{{index .List 1}}|{{index .Map "k"}}|{{slice .List 1 3}}`, "4|2|v|[2 3]"},
		{"call", `{{call .Fn 5}}|{{call .Join "a" "b"}}`, "fn5|a-b"},
		{"escapes", `{{html "<b>&"}}|{{js "'<"}}|{{urlquery "a b&c"}}`, "&lt;b&gt;&amp;|\\'\\u003C|a+b%26c"},
		{"vars", `{{$x := add 1 2}}{{$x}}|{{with $m := dict "a" 1 "b" 2}}{{$m.a}}{{$m.b}}{{end}}`, "3|12"},
		{"range", `{{range $i, $v := list "x" "y"}}{{$i}}={{$v}};{{end}}`, "0=x;1=y;"},
		{"logic", `{{if and true (eq 1 1)}}T{{else}}F{{end}}|{{if or false (lt 1 2)}}T{{else}}F{{end}}|{{not false}}`, "T|T|true"},
		{"short-circuit", `{{and false (boom)}}|{{or true (boom)}}`, "false|true"},
		{"dict-merge", `{{index (dict "a" 1 "." (dict "b" 2)) "b"}}|{{index (list 7 8) 1}}`, "2|8"},
		{"func-error", `{{div 9 3}}|{{div 1 1}}`, "3|1"},
		{"variadic-pipe", `{{index (list "a" "b" "c") 2}}|{{joinAny "," 1 2 3}}`, "c|1,2,3"},
		{"pointer-func", `{{nodeName .Node}}:{{.Node.Value}}`, "alpha:41"},
	}
	errs := []errCase{
		{"index-oob", `{{index .List 9}}`, "index out of range"},
		{"slice-bad", `{{slice .List 3 1}}`, "invalid slice index"},
		{"call-nonfn", `{{call 5}}`, "non-function"},
		{"dict-odd", `{{dict "a" 1 "b"}}`, "even number of arguments"},
		{"boom", `{{boom}}`, "boom called"},
		{"div-zero", `{{div 1 0}}`, "division by zero"},
		{"wrong-type", `{{upper .N}}`, "wrong type for value"},
	}

	// 成功用例：解析、执行、golden 校验；模板保留跨 GC。
	got := map[string]string{}
	for _, c := range success {
		t, err := template.New(c.name).Funcs(fm).Parse(c.body)
		ck(err == nil, fmt.Sprintf("%s parse err=%v", c.name, err))
		if err != nil {
			continue
		}
		var b strings.Builder
		err = t.Execute(&b, data)
		ck(err == nil, fmt.Sprintf("%s execute err=%v", c.name, err))
		got[c.name] = b.String()
		ck(b.String() == c.want, fmt.Sprintf("%s output=%q want %q", c.name, b.String(), c.want))
		live = append(live, t)
	}

	// 错误用例：错误必须带模板名/行号与固定片段。
	for _, c := range errs {
		t, err := template.New(c.name).Funcs(fm).Parse(c.body)
		ck(err == nil, fmt.Sprintf("%s parse err=%v", c.name, err))
		if err != nil {
			continue
		}
		var b strings.Builder
		err = t.Execute(&b, data)
		ck(err != nil, fmt.Sprintf("%s expected error", c.name))
		if err == nil {
			continue
		}
		msg := err.Error()
		ck(strings.Contains(msg, "template: "+c.name+":1:"), fmt.Sprintf("%s missing name/line: %v", c.name, err))
		ck(strings.Contains(msg, c.want), fmt.Sprintf("%s error=%q want %q", c.name, msg, c.want))
	}

	// FuncMap 静态类型检查：非函数值注册必须 panic。
	ck(panics(func() {
		template.New("bad-func").Funcs(template.FuncMap{"bad": 42})
	}), "non-function FuncMap must panic")

	// GC 后按种子顺序重放全部成功模板，输出与首次一致。
	names := make([]string, 0, len(got))
	for _, c := range success {
		names = append(names, c.name)
	}
	for round := 0; round < 3; round++ {
		gcStress()
		rng := rand.New(rand.NewSource(uafSeed + int64(round)))
		order := rng.Perm(len(names))
		for _, idx := range order {
			name := names[idx]
			var body string
			for _, c := range success {
				if c.name == name {
					body = c.body
				}
			}
			t, err := template.New(name).Funcs(fm).Parse(body)
			ck(err == nil, fmt.Sprintf("round %d %s reparse err=%v", round, name, err))
			if err != nil {
				continue
			}
			var b strings.Builder
			err = t.Execute(&b, data)
			ck(err == nil, fmt.Sprintf("round %d %s execute err=%v", round, name, err))
			ck(b.String() == got[name], fmt.Sprintf("round %d %s replay output changed", round, name))
		}
		// 数据指针与 map 在 GC 后仍被 FuncMap/模板读取。
		ck(data.Node.Name == "alpha" && data.Node.Value == 41, fmt.Sprintf("round %d node lost", round))
		keys := make([]string, 0, len(data.Map))
		for k := range data.Map {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		ck(len(keys) == 1 && keys[0] == "k" && data.Map["k"] == "v", fmt.Sprintf("round %d map changed", round))
	}

	finish("l16_tmpl_pipeline_builtins")
}
