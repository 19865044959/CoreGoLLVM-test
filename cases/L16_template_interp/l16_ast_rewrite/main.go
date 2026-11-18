// L16: go/ast 重命名/改字面量/插入声明，go/format 打印后用 go/types 复检，AST 跨 GC 仍可重写。
package main

import (
	"bytes"
	"fmt"
	"go/ast"
	"go/format"
	"go/parser"
	"go/token"
	"go/types"
	"os"
	"runtime"
	"strconv"
	"strings"
	"sync/atomic"
)

const src = `package demo

const limit = 100

func legacyCalc(a, b int) int {
	total := a + b
	if total > limit {
		total = limit
	}
	return total * 2
}

var legacyName = "legacy"
`

var renames = map[string]string{
	"legacyCalc": "modernCalc",
	"legacyName": "modernName",
	"limit":      "maxLimit",
}

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
		b := make([]byte, 4096)
		b[0] = byte(i)
		gcSink = append(gcSink[:0], b)
	}
	for i := 0; i < 32; i++ {
		_ = make([]*ast.Ident, 128)
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

func rewrite(f *ast.File, capValue string) {
	ast.Inspect(f, func(n ast.Node) bool {
		switch x := n.(type) {
		case *ast.Ident:
			if nn, ok := renames[x.Name]; ok {
				x.Name = nn
			}
		case *ast.BasicLit:
			switch x.Kind {
			case token.INT:
				switch x.Value {
				case "100":
					x.Value = capValue
				case "2":
					x.Value = "3"
				}
			case token.STRING:
				if x.Value == `"legacy"` {
					x.Value = `"modern"`
				}
			}
		}
		return true
	})
	// 新增 const Rewritten = true，验证向 Decls 追加节点后打印。
	f.Decls = append(f.Decls, &ast.GenDecl{
		Tok: token.CONST,
		Specs: []ast.Spec{&ast.ValueSpec{
			Names:  []*ast.Ident{ast.NewIdent("Rewritten")},
			Values: []ast.Expr{ast.NewIdent("true")},
		}},
	})
}

func printFile(fset *token.FileSet, f *ast.File) ([]byte, error) {
	var buf bytes.Buffer
	if err := format.Node(&buf, fset, f); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func typeCheck(name string, srcBytes []byte) (*types.Package, error) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, name, srcBytes, parser.AllErrors)
	if err != nil {
		return nil, err
	}
	var firstErr error
	conf := types.Config{Error: func(err error) {
		if firstErr == nil {
			firstErr = err
		}
	}}
	pkg, err := conf.Check("demo", fset, []*ast.File{f}, nil)
	if err != nil {
		return pkg, err
	}
	return pkg, firstErr
}

func main() {
	uafSeed = envSeed()
	rounds := tortureRounds(10 + int(uafSeed%5))

	for r := 0; r < rounds; r++ {
		capValue := strconv.Itoa(200 + int(uafSeed%7)*10 + r)
		fset := token.NewFileSet()
		f, err := parser.ParseFile(fset, "demo.go", src, parser.ParseComments|parser.AllErrors)
		ck(err == nil, fmt.Sprintf("round %d parse: %v", r, err))
		if err != nil {
			continue
		}
		liveAST = append(liveAST[:0], f)

		// 原 AST 的结构在重写前后必须仍可定位。
		var origFunc *ast.FuncDecl
		for _, d := range f.Decls {
			if fd, ok := d.(*ast.FuncDecl); ok {
				origFunc = fd
			}
		}
		ck(origFunc != nil, fmt.Sprintf("round %d original func not found", r))
		if origFunc != nil {
			ck(fset.Position(origFunc.Pos()).Line == 5,
				fmt.Sprintf("round %d legacyCalc line=%d want 5", r, fset.Position(origFunc.Pos()).Line))
		}

		rewrite(f, capValue)
		out, err := printFile(fset, f)
		ck(err == nil, fmt.Sprintf("round %d format: %v", r, err))
		if err != nil {
			continue
		}
		text := string(out)
		for _, want := range []string{
			"func modernCalc(a, b int) int",
			"maxLimit = " + capValue,
			`var modernName = "modern"`,
			"return total * 3",
			"const Rewritten = true",
		} {
			ck(strings.Contains(text, want), fmt.Sprintf("round %d formatted output missing %q\n%s", r, want, text))
		}
		ck(!strings.Contains(text, "legacy"), fmt.Sprintf("round %d output still contains legacy\n%s", r, text))
		ck(!strings.Contains(text, "limit"), fmt.Sprintf("round %d output still contains limit\n%s", r, text))

		pkg, terr := typeCheck("demo.go", out)
		ck(terr == nil, fmt.Sprintf("round %d type check rewritten: %v", r, terr))
		ck(pkg != nil, fmt.Sprintf("round %d rewritten package nil", r))
		if pkg != nil {
			for _, name := range []string{"modernCalc", "modernName", "maxLimit", "Rewritten"} {
				ck(pkg.Scope().Lookup(name) != nil, fmt.Sprintf("round %d scope missing %s", r, name))
			}
			for _, name := range []string{"legacyCalc", "legacyName", "limit"} {
				ck(pkg.Scope().Lookup(name) == nil, fmt.Sprintf("round %d scope still has %s", r, name))
			}
			if c, ok := pkg.Scope().Lookup("maxLimit").(*types.Const); ok {
				ck(c.Val().ExactString() == capValue, fmt.Sprintf("round %d maxLimit=%s want %s", r, c.Val().ExactString(), capValue))
			} else {
				ck(false, fmt.Sprintf("round %d maxLimit is not a constant", r))
			}
		}

		// gofmt 幂等：对打印结果再次 format 应逐字节一致。
		f2, err := parser.ParseFile(token.NewFileSet(), "demo.go", out, parser.AllErrors)
		ck(err == nil, fmt.Sprintf("round %d reparse formatted: %v", r, err))
		if err == nil {
			out2, err := printFile(token.NewFileSet(), f2)
			ck(err == nil, fmt.Sprintf("round %d reformat: %v", r, err))
			ck(string(out2) == text, fmt.Sprintf("round %d formatting not idempotent", r))
		}

		ck(origFunc != nil && origFunc.Name.Name == "modernCalc", fmt.Sprintf("round %d original AST rename lost", r))
		gcStress()
		ck(origFunc != nil && origFunc.Name.Name == "modernCalc", fmt.Sprintf("round %d renamed func after GC", r))
		ck(fset.Position(origFunc.Pos()).Line == 5, fmt.Sprintf("round %d position mapping after GC", r))

		outAfter, err := printFile(fset, f)
		ck(err == nil, fmt.Sprintf("round %d format after GC: %v", r, err))
		ck(string(outAfter) == text, fmt.Sprintf("round %d output changed after GC", r))
		pkg2, terr := typeCheck("demo.go", outAfter)
		ck(terr == nil && pkg2 != nil, fmt.Sprintf("round %d type check after GC: %v", r, terr))
		if pkg2 != nil {
			if c, ok := pkg2.Scope().Lookup("maxLimit").(*types.Const); ok {
				ck(c.Val().ExactString() == capValue, fmt.Sprintf("round %d maxLimit after GC=%s want %s", r, c.Val().ExactString(), capValue))
			}
		}
	}

	finish("l16_ast_rewrite")
}
