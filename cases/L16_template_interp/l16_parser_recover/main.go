// L16: go/parser 的错误恢复与增量解析：坏源码仍产出部分 AST、ErrorList 有序、
// 不完整表达式可续行；AST/位置表跨 GC 稳定。
package main

import (
	"bytes"
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/printer"
	"go/scanner"
	"go/token"
	"math/rand"
	"os"
	"runtime"
	"strconv"
	"strings"
	"sync/atomic"
)

const validSrc = `package demo

// Add sums two values.
func Add(a, b int) int {
	return a + b
}

// Output: 42
// Error: none
`

const badSrc = `package bad

func broken() int {
	if 1 > 0 {
		return 1
	}
	return "s"
}

func missingParen( {
	return
}

var trailing =
`

const fixedSrc = `package demo

// Mul multiplies two values.
func Mul(a, b int) int {
	return a * b
}

func Sum(a, b int) int {
	return a + b
}
`

var (
	failed  int32
	gcSink  [][]byte
	liveAST []*ast.File
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
	for i := 0; i < 32; i++ {
		_ = make([]ast.Node, 128)
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

func errList(err error) (scanner.ErrorList, bool) {
	var el scanner.ErrorList
	if errors.As(err, &el) {
		return el, true
	}
	if e, ok := err.(scanner.ErrorList); ok {
		return e, true
	}
	return nil, false
}

func hasFunc(f *ast.File, name string) *ast.FuncDecl {
	for _, d := range f.Decls {
		fd, ok := d.(*ast.FuncDecl)
		if ok && fd.Recv == nil && fd.Name.Name == name {
			return fd
		}
	}
	return nil
}

func exprText(fset *token.FileSet, e ast.Expr) string {
	var buf bytes.Buffer
	_ = printer.Fprint(&buf, fset, e)
	return buf.String()
}

func main() {
	uafSeed = envSeed()

	// 1) 带注释的合法源码：ParseComments 保留尾部 Output/Error 约定。
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "valid.go", validSrc, parser.ParseComments)
	ck(err == nil, fmt.Sprintf("valid source parse error=%v", err))
	ck(file != nil && file.Name.Name == "demo", "valid source package name")
	ck(len(file.Comments) == 2, fmt.Sprintf("valid source comment groups=%d", len(file.Comments)))
	gotOutput, gotError := "", ""
	for _, cg := range file.Comments {
		for _, c := range cg.List {
			t := strings.TrimSpace(strings.TrimPrefix(c.Text, "//"))
			if v, ok := strings.CutPrefix(t, "Output:"); ok {
				gotOutput = strings.TrimSpace(v)
			}
			if v, ok := strings.CutPrefix(t, "Error:"); ok {
				gotError = strings.TrimSpace(v)
			}
		}
	}
	ck(gotOutput == "42", fmt.Sprintf("Output comment=%q", gotOutput))
	ck(gotError == "none", fmt.Sprintf("Error comment=%q", gotError))
	ck(len(file.Decls) == 1, fmt.Sprintf("valid source decls=%d", len(file.Decls)))
	liveAST = append(liveAST, file)

	// 2) 坏源码：AllErrors 下仍返回部分 AST，ErrorList 按位置有序。
	bfset := token.NewFileSet()
	badFile, badErr := parser.ParseFile(bfset, "bad.go", badSrc, parser.AllErrors|parser.ParseComments)
	ck(badErr != nil, "bad source must fail to parse")
	ck(badFile != nil, "bad source must yield partial AST")
	if badFile != nil {
		ck(badFile.Name != nil && badFile.Name.Name == "bad", "partial AST package name")
		ck(hasFunc(badFile, "broken") != nil, "partial AST keeps broken()")
		liveAST = append(liveAST, badFile)
	}
	el, ok := errList(badErr)
	ck(ok, fmt.Sprintf("bad source error is ErrorList: %T", badErr))
	if ok {
		ck(len(el) >= 3, fmt.Sprintf("bad source errors=%d", len(el)))
		sorted := true
		for i := 1; i < len(el); i++ {
			if el[i-1].Pos.Offset > el[i].Pos.Offset {
				sorted = false
			}
		}
		ck(sorted, "ErrorList must be sorted by offset")
		expectedHits := 0
		for _, e := range el {
			ck(e.Pos.Line > 0, fmt.Sprintf("error line=%d", e.Pos.Line))
			if strings.Contains(e.Msg, "expected") || strings.Contains(e.Msg, "missing") {
				expectedHits++
			}
		}
		ck(expectedHits >= 1, fmt.Sprintf("expected/missing errors=%d", expectedHits))
		if el[0].Msg == "" {
			ck(false, "first parse error message empty")
		}
	} else if badErr != nil {
		ck(strings.Contains(badErr.Error(), "bad.go"), "bad source error mentions file")
	}
	// 部分 AST 的位置仍指向原文件。
	if badFile != nil {
		fd := hasFunc(badFile, "broken")
		if fd != nil {
			pos := bfset.Position(fd.Pos())
			ck(pos.Filename == "bad.go" && pos.Line >= 3, fmt.Sprintf("broken position=%s", pos))
		}
	}

	// 3) 增量解析：不完整表达式报错，补齐后打印结果确定。
	type incr struct {
		partial string
		suffix  string
		want    string
	}
	pairs := []incr{
		{"1 + 2", "", "1 + 2"},
		{"f(1,", "2)", "f(1, 2)"},
		{"[]int{1,", "2, 3}", "[]int{1, 2, 3}"},
		{`map[string]int{"a": 1,`, `"b": 2}`, `map[string]int{"a": 1, "b": 2}`},
		{"(1 +", "2) * 3", "(1 + 2) * 3"},
		{"[]int{1, 2}[", "0]", "[]int{1, 2}[0]"},
	}
	badExprs := []string{"1 +", `"abc`, "1 /* x", "f(", ") ("}

	rng := rand.New(rand.NewSource(uafSeed))
	order := rng.Perm(len(pairs))
	for _, idx := range order {
		p := pairs[idx]
		if _, err := parser.ParseExpr(p.partial); err == nil && p.suffix != "" {
			ck(false, fmt.Sprintf("partial expr %q should fail", p.partial))
		}
		e, err := parser.ParseExpr(p.partial + p.suffix)
		ck(err == nil, fmt.Sprintf("expr %q parse error=%v", p.partial+p.suffix, err))
		if err == nil {
			got := exprText(token.NewFileSet(), e)
			ck(got == p.want, fmt.Sprintf("expr %q printed %q want %q", p.partial+p.suffix, got, p.want))
		}
	}
	for _, src := range badExprs {
		e, err := parser.ParseExpr(src)
		ck(err != nil, fmt.Sprintf("bad expr %q unexpectedly parsed as %v", src, e))
	}

	// 4) 修复后重新解析：零错误、声明齐全、注释保留。
	rfset := token.NewFileSet()
	fixed, err := parser.ParseFile(rfset, "fixed.go", fixedSrc, parser.ParseComments)
	ck(err == nil, fmt.Sprintf("fixed source parse error=%v", err))
	if fixed != nil {
		ck(len(fixed.Decls) == 2, fmt.Sprintf("fixed decls=%d", len(fixed.Decls)))
		ck(hasFunc(fixed, "Mul") != nil && hasFunc(fixed, "Sum") != nil, "fixed funcs missing")
		ck(len(fixed.Comments) == 1, fmt.Sprintf("fixed comment groups=%d", len(fixed.Comments)))
		liveAST = append(liveAST, fixed)
	}

	// 5) 每轮 GC 后重解析坏源码并复核错误/位置/部分 AST。
	for round := 0; round < 3; round++ {
		gcStress()
		b := make([]byte, 0, len(badSrc))
		b = append(b, badSrc...)
		f2, err2 := parser.ParseFile(token.NewFileSet(), "bad.go", string(b), parser.AllErrors)
		ck(err2 != nil && f2 != nil, fmt.Sprintf("round %d reparse bad source", round))
		el2, ok2 := errList(err2)
		ck(ok2 && len(el2) == len(el), fmt.Sprintf("round %d ErrorList size", round))
		if f2 != nil {
			ck(hasFunc(f2, "broken") != nil, fmt.Sprintf("round %d broken() lost", round))
			liveAST = append(liveAST, f2)
		}
		// 保留的 AST 在 GC 后仍可遍历。
		n := 0
		ast.Inspect(liveAST[0], func(nd ast.Node) bool {
			if nd != nil {
				n++
			}
			return true
		})
		ck(n >= 20, fmt.Sprintf("round %d valid AST nodes=%d", round, n))
	}

	finish("l16_parser_recover")
}
