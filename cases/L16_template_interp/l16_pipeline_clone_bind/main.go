// L16: text/template 管道与 Clone 绑定边界：Clone 后的 FuncMap 扩展、关联模板共享、
// Option/命名空间隔离、管道短路与错误传播；TORTURE_ROUNDS 下反复 Clone/覆写/执行。
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

const src = `{{define "row"}}[{{.Name}}:{{add .N 1}}:{{if gt .N 1}}{{mul .N 2}}{{else}}{{upper .Name}}{{end}}]{{end}}` +
	`{{define "list"}}{{range .Items}}{{template "row" .}}{{end}}{{end}}` +
	`{{define "main"}}{{with .Name}}{{. | wrap "<" ">"}}{{end}}|{{template "list" .}}{{end}}` +
	`{{define "pipe"}}{{$x := .N | add 1 | mul 2}}{{$x}}|{{.N | printf "%03d"}}{{end}}` +
	`{{define "miss"}}M:{{.Missing}}{{end}}`

type item struct {
	Name string
	N    int
}

type view struct {
	Name  string
	N     int
	Items []item
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
		_ = &view{Name: "g", Items: []item{{Name: "g", N: i}}}
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

func baseFuncs() template.FuncMap {
	return template.FuncMap{
		"add":   func(a, b int) int { return a + b },
		"mul":   func(a, b int) int { return a * b },
		"upper": strings.ToUpper,
		"wrap":  func(prefix, suffix, s string) string { return prefix + s + suffix },
	}
}

func exec(t *template.Template, name string, data any) (string, error) {
	var b strings.Builder
	err := t.ExecuteTemplate(&b, name, data)
	return b.String(), err
}

func main() {
	uafSeed = envSeed()
	rng := rand.New(rand.NewSource(uafSeed))
	rounds := tortureRounds(20)
	fm := baseFuncs()

	base, err := template.New("base").Funcs(fm).Option("missingkey=error").Parse(src)
	ck(err == nil, fmt.Sprintf("base parse err=%v", err))
	if base == nil {
		finish("l16_pipeline_clone_bind")
		return
	}
	live = append(live, base)

	v := view{Name: "go", N: 5, Items: []item{{"go", 1}, {"tmpl", 5}, {"gc", 7}}}
	wantMain := "<go>|[go:2:GO][tmpl:6:10][gc:8:14]"
	got, err := exec(base, "main", v)
	ck(err == nil && got == wantMain, fmt.Sprintf("base main=%q err=%v", got, err))
	got, err = exec(base, "pipe", v)
	ck(err == nil && got == "12|005", fmt.Sprintf("base pipe=%q err=%v", got, err))

	// 1) Clone 后扩展 FuncMap 与新模板，base 看不到；管道输出不变。
	clone1, err := base.Clone()
	ck(err == nil && clone1 != nil, fmt.Sprintf("clone1 err=%v", err))
	if clone1 == nil {
		finish("l16_pipeline_clone_bind")
		return
	}
	live = append(live, clone1)
	clone1.Funcs(template.FuncMap{"tag": func(s string) string { return "T:" + s }})
	_, err = clone1.New("tagged").Parse(`{{tag .Name}}|{{.N | add 10 | mul 2}}`)
	ck(err == nil, fmt.Sprintf("clone tagged parse err=%v", err))
	got, err = exec(clone1, "tagged", v)
	ck(err == nil && got == "T:go|30", fmt.Sprintf("clone tagged=%q err=%v", got, err))
	ck(base.Lookup("tagged") == nil, "base must not see clone template")
	_, err = base.New("notag").Parse(`{{tag .Name}}`)
	ck(err != nil && strings.Contains(err.Error(), `function "tag" not defined`), fmt.Sprintf("base tag err=%v", err))
	got, err = exec(clone1, "main", v)
	ck(err == nil && got == wantMain, fmt.Sprintf("clone main=%q err=%v", got, err))
	got, err = exec(clone1, "pipe", v)
	ck(err == nil && got == "12|005", fmt.Sprintf("clone pipe=%q err=%v", got, err))

	// 2) 对 Clone 的关联模板调用 Funcs：共享 FuncMap，新模板即可使用。
	clone1.Lookup("row").Funcs(template.FuncMap{"mark": func(s string) string { return "*" + s + "*" }})
	_, err = clone1.New("marked").Parse(`{{.Name | mark | upper}}`)
	ck(err == nil, fmt.Sprintf("marked parse err=%v", err))
	got, err = exec(clone1, "marked", v)
	ck(err == nil && got == "*GO*", fmt.Sprintf("marked output=%q err=%v", got, err))

	// 3) Option 隔离：clone2 默认 missingkey 行为，base 仍报错。
	clone2, err := base.Clone()
	ck(err == nil && clone2 != nil, fmt.Sprintf("clone2 err=%v", err))
	if clone2 != nil {
		live = append(live, clone2)
		got, err = exec(clone2, "miss", map[string]any{})
		ck(err == nil && got == "M:<no value>", fmt.Sprintf("clone2 miss=%q err=%v", got, err))
		clone2.Option("missingkey=error")
		_, err = exec(clone2, "miss", map[string]any{})
		ck(err != nil && strings.Contains(err.Error(), `map has no entry for key "Missing"`), fmt.Sprintf("clone2 option err=%v", err))
		_, err = exec(base, "miss", map[string]any{})
		ck(err != nil, "base missingkey option leaked")
	}

	// 4) base 在 Clone 之后新增的函数不进入已有 Clone。
	base.Funcs(template.FuncMap{"late": func(s string) string { return "L:" + s }})
	_, err = base.New("late").Parse(`{{late .Name}}`)
	ck(err == nil, fmt.Sprintf("base late parse err=%v", err))
	_, err = clone1.New("lateclone").Parse(`{{late .Name}}`)
	ck(err != nil && strings.Contains(err.Error(), `function "late" not defined`), fmt.Sprintf("clone late err=%v", err))

	// 5) 管道错误传播：clone 级 fail 函数返回 error，错误带模板名/行号。
	clone1.Funcs(template.FuncMap{"fail": func(n int) (int, error) {
		return 0, fmt.Errorf("pipeline boom %d", n)
	}})
	_, err = clone1.New("bad").Parse(`{{.N | add 1 | fail}}`)
	ck(err == nil, fmt.Sprintf("bad parse err=%v", err))
	_, err = exec(clone1, "bad", v)
	ck(err != nil && strings.Contains(err.Error(), "pipeline boom 6"), fmt.Sprintf("bad exec err=%v", err))
	ck(err != nil && strings.Contains(err.Error(), "template: bad:1:"), fmt.Sprintf("bad exec location err=%v", err))

	// 6) TORTURE_ROUNDS：每轮 Clone、覆写 row、执行管道；base 不受影响。
	for round := 0; round < rounds; round++ {
		cl, err := base.Clone()
		ck(err == nil, fmt.Sprintf("round %d clone err=%v", round, err))
		if err != nil {
			continue
		}
		cl.Funcs(template.FuncMap{
			"roundTag": func(s string) string { return fmt.Sprintf("%s@%d", s, round) },
		})
		_, err = cl.New("round").Parse(`{{.Name | roundTag | upper}}:{{.N | add 1 | mul 3}}`)
		ck(err == nil, fmt.Sprintf("round %d parse err=%v", round, err))
		perm := rng.Perm(len(v.Items))
		var want strings.Builder
		for _, pi := range perm {
			it := v.Items[pi]
			got, err := exec(cl, "row", it)
			ck(err == nil && got == fmt.Sprintf("[%s:%d:%s]", it.Name, it.N+1, rowTail(it)), "round row output")
			got, err = exec(cl, "round", it)
			ck(err == nil && got == fmt.Sprintf("%s@%d:%d", strings.ToUpper(it.Name), round, (it.N+1)*3),
				fmt.Sprintf("round %d round=%q err=%v", round, got, err))
			want.WriteString(got)
		}
		// 关联模板 Funcs 影响整个 Clone 命名空间。
		cl.Lookup("row").Funcs(template.FuncMap{
			"tail": func(n int) string { return fmt.Sprintf("r%d", n+round) },
		})
		_, err = cl.New("probe").Parse(`{{tail .N}}`)
		ck(err == nil, fmt.Sprintf("round %d probe parse err=%v", round, err))
		got, err := exec(cl, "probe", v.Items[0])
		ck(err == nil && got == fmt.Sprintf("r%d", 1+round), fmt.Sprintf("round %d probe=%q err=%v", round, got, err))
		// base 的 row 保持不变。
		got, err = exec(base, "row", v.Items[0])
		ck(err == nil && got == "[go:2:GO]", fmt.Sprintf("round %d base row changed=%q", round, got))
		if round%4 == 0 {
			gcStress()
		}
		live = append(live, cl)
	}

	// 7) 最终 GC 后复验 base/Clone 管道。
	gcStress()
	got, err = exec(base, "main", v)
	ck(err == nil && got == wantMain, fmt.Sprintf("post-GC base main=%q err=%v", got, err))
	got, err = exec(clone1, "tagged", v)
	ck(err == nil && got == "T:go|30", fmt.Sprintf("post-GC tagged=%q err=%v", got, err))
	got, err = exec(clone1, "marked", v)
	ck(err == nil && got == "*GO*", fmt.Sprintf("post-GC marked=%q err=%v", got, err))

	finish("l16_pipeline_clone_bind")
}

func rowTail(it item) string {
	if it.N > 1 {
		return strconv.Itoa(it.N * 2)
	}
	return strings.ToUpper(it.Name)
}
