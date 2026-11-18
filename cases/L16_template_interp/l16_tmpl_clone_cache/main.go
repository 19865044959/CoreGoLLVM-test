// L16: 模板缓存与 Clone：一次解析多次执行、Clone 命名空间隔离、Clone 后 Funcs/
// 新关联模板/AddParseTree、Option 隔离与并发执行；跨 GC 与 TORTURE_ROUNDS 放大。
package main

import (
	"fmt"
	"math/rand"
	"os"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"text/template"
)

const src = `{{define "layout"}}[{{template "head" .}}|{{template "body" .}}]{{end}}` +
	`{{define "head"}}H:{{.Name}}{{end}}` +
	`{{define "body"}}B:{{upper .Name}}:{{wiggle .N}}{{end}}` +
	`{{define "miss"}}M:{{.Missing}}{{end}}`

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
		_ = &struct {
			S string
			P *int
		}{S: strconv.Itoa(i)}
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

func funcMap() template.FuncMap {
	return template.FuncMap{
		"upper":  strings.ToUpper,
		"wiggle": func(n int) string { return fmt.Sprintf("w%d", n) },
	}
}

func layoutText(name string, n int) string {
	return fmt.Sprintf("[H:%s|B:%s:w%d]", name, strings.ToUpper(name), n)
}

func main() {
	uafSeed = envSeed()
	rounds := tortureRounds(24)
	fm := funcMap()

	base, err := template.New("base").Funcs(fm).Option("missingkey=error").Parse(src)
	ck(err == nil, fmt.Sprintf("base parse err=%v", err))
	if base == nil {
		finish("l16_tmpl_clone_cache")
		return
	}
	live = append(live, base)

	exec := func(t *template.Template, name string, data any) (string, error) {
		var b strings.Builder
		err := t.ExecuteTemplate(&b, name, data)
		return b.String(), err
	}

	// 1) 一次解析多次执行与基本查找。
	for i, v := range []struct {
		name string
		n    int
	}{{"go", 1}, {"tmpl", 7}, {"gc", 42}} {
		got, err := exec(base, "layout", map[string]any{"Name": v.name, "N": v.n})
		ck(err == nil, fmt.Sprintf("exec %d err=%v", i, err))
		ck(got == layoutText(v.name, v.n), fmt.Sprintf("exec %d output=%q", i, got))
	}
	ck(base.Lookup("head") != nil && base.Lookup("body") != nil && base.Lookup("layout") != nil, "lookup defined templates")
	ck(base.Lookup("nope") == nil, "lookup missing template must be nil")
	names := []string{}
	for _, t := range base.Templates() {
		names = append(names, t.Name())
	}
	sort.Strings(names)
	ck(strings.Join(names, ",") == "base,body,head,layout,miss", fmt.Sprintf("templates=%v", names))

	// 2) Clone：命名空间独立、FuncMap 可扩展、base 不受影响。
	clone, err := base.Clone()
	ck(err == nil && clone != nil, fmt.Sprintf("clone err=%v", err))
	if clone != nil {
		live = append(live, clone)
		ck(clone.Name() == "base", fmt.Sprintf("clone name=%q", clone.Name()))
		got, err := exec(clone, "layout", map[string]any{"Name": "go", "N": 1})
		ck(err == nil && got == layoutText("go", 1), fmt.Sprintf("clone exec=%q err=%v", got, err))
		ck(clone.Lookup("nope") == nil, "clone lookup missing")

		// Clone 后追加 FuncMap 与新关联模板。
		clone.Funcs(template.FuncMap{"bang": func(s string) string { return "!" + s }})
		_, err = clone.New("extra").Parse("E:{{bang .Name}}")
		ck(err == nil, fmt.Sprintf("clone extra parse err=%v", err))
		got, err = exec(clone, "extra", map[string]any{"Name": "go"})
		ck(err == nil && got == "E:!go", fmt.Sprintf("clone extra=%q err=%v", got, err))
		ck(base.Lookup("extra") == nil, "base must not see clone's extra template")
		_, err = exec(base, "extra", map[string]any{"Name": "go"})
		ck(err != nil && strings.Contains(err.Error(), "no template \"extra\""), fmt.Sprintf("base extra err=%v", err))
		_, err = base.New("extra2").Parse("E:{{bang .Name}}")
		ck(err != nil && strings.Contains(err.Error(), `function "bang" not defined`), fmt.Sprintf("base bang parse err=%v", err))
	}

	// 3) Option 隔离：Clone 不继承 base 的 missingkey=error，显式 Option 只影响 clone。
	clone2, err := base.Clone()
	ck(err == nil && clone2 != nil, fmt.Sprintf("clone2 err=%v", err))
	if clone2 != nil {
		live = append(live, clone2)
		got, err := exec(clone2, "miss", map[string]any{})
		ck(err == nil && got == "M:<no value>", fmt.Sprintf("clone2 default output=%q err=%v", got, err))
		clone2.Option("missingkey=error")
		_, err = exec(clone2, "miss", map[string]any{})
		ck(err != nil && strings.Contains(err.Error(), `map has no entry for key "Missing"`), fmt.Sprintf("clone2 missing error=%v", err))
		_, err = exec(base, "miss", map[string]any{})
		ck(err != nil && strings.Contains(err.Error(), `map has no entry for key "Missing"`), fmt.Sprintf("base option leaked from clone: %v", err))
	}

	// 4) Clone 上的 AddParseTree 与 base 隔离。
	part := template.Must(template.New("part").Funcs(fm).Parse("P:{{.N}}"))
	clone3, err := base.Clone()
	ck(err == nil && clone3 != nil, fmt.Sprintf("clone3 err=%v", err))
	if clone3 != nil {
		live = append(live, clone3)
		_, err = clone3.AddParseTree("part", part.Tree)
		ck(err == nil, fmt.Sprintf("AddParseTree err=%v", err))
		got, err := exec(clone3, "part", map[string]any{"N": 7})
		ck(err == nil && got == "P:7", fmt.Sprintf("part output=%q err=%v", got, err))
		ck(base.Lookup("part") == nil, "base must not see AddParseTree")
	}

	// 5) 缓存：同名模板只解析一次，命中返回同一指针。
	cache := map[string]*template.Template{}
	hits, misses := 0, 0
	get := func(name string) *template.Template {
		if t, ok := cache[name]; ok {
			hits++
			return t
		}
		misses++
		t := base.Lookup(name)
		if t != nil {
			cache[name] = t
		}
		return t
	}
	firstHead := get("head")
	for i := 0; i < 50; i++ {
		for _, name := range []string{"head", "body", "layout"} {
			t := get(name)
			ck(t != nil && t == get(name), "cache pointer identity")
		}
	}
	ck(firstHead == base.Lookup("head"), "cache stored wrong pointer")
	ck(hits == 298 && misses == 3, fmt.Sprintf("cache hits/misses=%d/%d", hits, misses))

	// 6) 并发执行共享模板：各 goroutine 独立 buffer，输出按数据逐字校验。
	variants := []struct {
		name string
		n    int
	}{{"go", 1}, {"tmpl", 7}, {"gc", 42}, {"r7", 100}}
	var wg sync.WaitGroup
	for w := 0; w < 4; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < 20; i++ {
				v := variants[(w+i)%len(variants)]
				var b strings.Builder
				err := base.ExecuteTemplate(&b, "layout", map[string]any{"Name": v.name, "N": v.n})
				if err != nil || b.String() != layoutText(v.name, v.n) {
					ck(false, fmt.Sprintf("concurrent shared exec w=%d i=%d out=%q err=%v", w, i, b.String(), err))
				}
			}
		}(w)
	}
	wg.Wait()

	// 7) 并发执行独立 Clone（各自标签函数），验证隔离与闭包跨 GC。
	clones := make([]*template.Template, 4)
	for i := range clones {
		cl, err := base.Clone()
		ck(err == nil, fmt.Sprintf("preclone %d err=%v", i, err))
		if err != nil {
			continue
		}
		tag := fmt.Sprintf("c%d", i)
		cl.Funcs(template.FuncMap{"tag": func() string { return tag }})
		_, err = cl.New("tagged").Parse("T:{{tag}}:{{template \"head\" .}}")
		ck(err == nil, fmt.Sprintf("tagged %d parse err=%v", i, err))
		clones[i] = cl
		live = append(live, cl)
	}
	for w := 0; w < 4; w++ {
		if clones[w] == nil {
			continue
		}
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			want := fmt.Sprintf("T:c%d:H:go", w)
			for i := 0; i < 20; i++ {
				var b strings.Builder
				err := clones[w].ExecuteTemplate(&b, "tagged", map[string]any{"Name": "go"})
				if err != nil || b.String() != want {
					ck(false, fmt.Sprintf("concurrent clone w=%d out=%q err=%v", w, b.String(), err))
				}
			}
		}(w)
	}
	wg.Wait()
	ck(base.Lookup("tagged") == nil, "base must not see clone tagged template")

	// 8) TORTURE_ROUNDS：每轮随机顺序 Clone/扩展/执行/缓存命中。
	rng := rand.New(rand.NewSource(uafSeed))
	for round := 0; round < rounds; round++ {
		cl, err := base.Clone()
		ck(err == nil, fmt.Sprintf("round %d clone err=%v", round, err))
		if err != nil {
			continue
		}
		cl.Funcs(template.FuncMap{
			"roundTag": func(s string) string { return fmt.Sprintf("%s@%d", s, round) },
		})
		_, err = cl.New("round").Parse(`R:{{roundTag .Name}}:{{.N}}`)
		ck(err == nil, fmt.Sprintf("round %d parse err=%v", round, err))
		perm := rng.Perm(len(variants))
		for _, vi := range perm {
			v := variants[vi]
			got, err := exec(cl, "layout", map[string]any{"Name": v.name, "N": v.n})
			ck(err == nil && got == layoutText(v.name, v.n), fmt.Sprintf("round %d layout %s=%q err=%v", round, v.name, got, err))
			ck(get("head") != nil && get("body") == base.Lookup("body"), fmt.Sprintf("round %d cache miss", round))
			got, err = exec(cl, "round", map[string]any{"Name": v.name, "N": v.n})
			ck(err == nil && got == fmt.Sprintf("R:%s@%d:%d", v.name, round, v.n),
				fmt.Sprintf("round %d roundTag=%q err=%v", round, got, err))
		}
		if round%4 == 0 {
			gcStress()
		}
		live = append(live, cl)
	}

	// 9) GC 后复验 base/缓存/Clone 闭包。
	gcStress()
	for _, v := range variants {
		got, err := exec(base, "layout", map[string]any{"Name": v.name, "N": v.n})
		ck(err == nil && got == layoutText(v.name, v.n), fmt.Sprintf("post-GC base %s=%q err=%v", v.name, got, err))
	}
	if clone != nil {
		got, err := exec(clone, "extra", map[string]any{"Name": "gc"})
		ck(err == nil && got == "E:!gc", fmt.Sprintf("post-GC clone func=%q err=%v", got, err))
	}
	if clones[2] != nil {
		got, err := exec(clones[2], "tagged", map[string]any{"Name": "go"})
		ck(err == nil && got == "T:c2:H:go", fmt.Sprintf("post-GC clone tag=%q err=%v", got, err))
	}
	ck(get("layout") == base.Lookup("layout"), "post-GC cache identity")

	finish("l16_tmpl_clone_cache")
}
