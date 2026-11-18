// L16: 基于 ast.CommentMap 的注释保持重写（重命名/改运算符/改字面量/删声明），go/format + go/types 复检，跨 GC。
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

const src = `// Package demo exercises comment-preserving rewrites.
package demo

// legacyAdd is documented and must keep this doc comment.
func legacyAdd(a, b int) int {
	// keep inline note
	return a + b // trailing add note
}

// unused helper to be removed.
var unused = 0 // remove this note

// run is the entry point.
func run() int {
	return legacyAdd(1, 2) // call note
}
`

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

func printFile(fset *token.FileSet, f *ast.File) (string, error) {
	var buf bytes.Buffer
	if err := format.Node(&buf, fset, f); err != nil {
		return "", err
	}
	return buf.String(), nil
}

func typeCheck(name string, srcBytes []byte) (*types.Package, []string) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, name, srcBytes, parser.AllErrors)
	if err != nil {
		return nil, []string{err.Error()}
	}
	var errs []string
	conf := types.Config{Error: func(e error) { errs = append(errs, e.Error()) }}
	pkg, err := conf.Check("demo", fset, []*ast.File{f}, nil)
	if err != nil && len(errs) == 0 {
		errs = append(errs, err.Error())
	}
	return pkg, errs
}

func commentText(f *ast.File) string {
	var sb strings.Builder
	for _, cg := range f.Comments {
		sb.WriteString(cg.Text())
	}
	return sb.String()
}

func main() {
	uafSeed = envSeed()
	rounds := 4 + int(uafSeed%3)

	for r := 0; r < rounds; r++ {
		lit := strconv.Itoa(2 + r%3)
		fset := token.NewFileSet()
		f, err := parser.ParseFile(fset, "demo.go", src, parser.ParseComments|parser.AllErrors)
		ck(err == nil, fmt.Sprintf("round %d parse: %v", r, err))
		if err != nil {
			continue
		}
		cmap := ast.NewCommentMap(fset, f, f.Comments)

		var funcDecl *ast.FuncDecl
		for _, d := range f.Decls {
			if fd, ok := d.(*ast.FuncDecl); ok && fd.Name.Name == "legacyAdd" {
				funcDecl = fd
			}
		}
		ck(funcDecl != nil, fmt.Sprintf("round %d legacyAdd not found", r))
		if funcDecl == nil {
			continue
		}
		ck(fset.Position(funcDecl.Pos()).Line == 5,
			fmt.Sprintf("round %d legacyAdd line=%d want 5", r, fset.Position(funcDecl.Pos()).Line))

		var binExpr *ast.BinaryExpr
		ast.Inspect(funcDecl, func(n ast.Node) bool {
			if be, ok := n.(*ast.BinaryExpr); ok && be.Op == token.ADD {
				binExpr = be
				be.Op = token.MUL
			}
			return true
		})
		ck(binExpr != nil, fmt.Sprintf("round %d add expression not found", r))

		// 同时改写 doc 注释文本，避免留下旧函数名。
		for n, cgs := range cmap {
			if _, ok := n.(*ast.FuncDecl); !ok || n != ast.Node(funcDecl) {
				continue
			}
			for _, cg := range cgs {
				for _, c := range cg.List {
					if strings.Contains(c.Text, "legacyAdd") {
						c.Text = strings.ReplaceAll(c.Text, "legacyAdd", "fastAdd")
					}
				}
			}
		}

		ast.Inspect(f, func(n ast.Node) bool {
			switch x := n.(type) {
			case *ast.Ident:
				if x.Name == "legacyAdd" {
					x.Name = "fastAdd"
				}
			case *ast.BasicLit:
				if x.Kind == token.INT && x.Value == "2" {
					x.Value = lit
				}
			}
			return true
		})

		var decls []ast.Decl
		for _, d := range f.Decls {
			if gd, ok := d.(*ast.GenDecl); ok && gd.Tok == token.VAR {
				continue
			}
			decls = append(decls, d)
		}
		f.Decls = decls
		filtered := cmap.Filter(f)
		f.Comments = filtered.Comments()
		live = []any{fset, f, cmap, filtered, funcDecl, binExpr}

		out, err := printFile(fset, f)
		ck(err == nil, fmt.Sprintf("round %d format: %v", r, err))
		if err != nil {
			continue
		}
		checks := []string{
			"// Package demo exercises comment-preserving rewrites.",
			"package demo",
			"// fastAdd is documented and must keep this doc comment.",
			"func fastAdd(a, b int) int",
			"// keep inline note",
			"return a * b // trailing add note",
			"// run is the entry point.",
			"func run() int",
			"return fastAdd(1, " + lit + ") // call note",
		}
		for _, want := range checks {
			ck(strings.Contains(out, want), fmt.Sprintf("round %d output missing %q\n%s", r, want, out))
		}
		for _, bad := range []string{"legacyAdd", "unused", "remove this note", "a + b"} {
			ck(!strings.Contains(out, bad), fmt.Sprintf("round %d output still contains %q\n%s", r, bad, out))
		}
		ck(len(f.Comments) == 6, fmt.Sprintf("round %d comment groups=%d want 6", r, len(f.Comments)))
		ctext := commentText(f)
		for _, want := range []string{
			"Package demo exercises comment-preserving rewrites.",
			"fastAdd is documented and must keep this doc comment.",
			"keep inline note", "trailing add note", "run is the entry point.", "call note",
		} {
			ck(strings.Contains(ctext, want), fmt.Sprintf("round %d comment text missing %q", r, want))
		}
		for _, bad := range []string{"unused helper", "remove this note"} {
			ck(!strings.Contains(ctext, bad), fmt.Sprintf("round %d removed comment still present: %q", r, bad))
		}

		pkg, terr := typeCheck("demo.go", []byte(out))
		ck(len(terr) == 0, fmt.Sprintf("round %d type check: %v", r, terr))
		ck(pkg != nil, fmt.Sprintf("round %d type check returned nil package", r))
		if pkg != nil {
			for _, name := range []string{"fastAdd", "run"} {
				ck(pkg.Scope().Lookup(name) != nil, fmt.Sprintf("round %d scope missing %s", r, name))
			}
			ck(pkg.Scope().Lookup("legacyAdd") == nil, fmt.Sprintf("round %d scope still has legacyAdd", r))
			ck(pkg.Scope().Lookup("unused") == nil, fmt.Sprintf("round %d scope still has unused", r))
			if obj := pkg.Scope().Lookup("fastAdd"); obj != nil {
				ck(obj.Type().String() == "func(a int, b int) int",
					fmt.Sprintf("round %d fastAdd type=%v", r, obj.Type()))
			}
		}

		// gofmt 幂等。
		fset2 := token.NewFileSet()
		f2, err := parser.ParseFile(fset2, "demo.go", []byte(out), parser.ParseComments|parser.AllErrors)
		ck(err == nil, fmt.Sprintf("round %d reparse formatted: %v", r, err))
		if err == nil {
			out2, ferr := printFile(fset2, f2)
			ck(ferr == nil && out2 == out, fmt.Sprintf("round %d formatting not idempotent: %v", r, ferr))
		}

		gcStress()

		ck(funcDecl.Name.Name == "fastAdd", fmt.Sprintf("round %d renamed func lost after GC", r))
		ck(binExpr != nil && binExpr.Op == token.MUL, fmt.Sprintf("round %d operator rewrite lost after GC", r))
		ck(fset.Position(funcDecl.Pos()).Line == 5, fmt.Sprintf("round %d position mapping after GC", r))
		ck(len(f.Comments) == 6, fmt.Sprintf("round %d comments lost after GC: %d", r, len(f.Comments)))
		ctext2 := commentText(f)
		ck(ctext2 == ctext, fmt.Sprintf("round %d comment text changed after GC", r))
		outAfter, err := printFile(fset, f)
		ck(err == nil, fmt.Sprintf("round %d format after GC: %v", r, err))
		ck(outAfter == out, fmt.Sprintf("round %d output changed after GC", r))
		cmap2 := ast.NewCommentMap(fset, f, f.Comments)
		ck(len(cmap2) > 0, fmt.Sprintf("round %d CommentMap empty after GC", r))
		ck(len(cmap2.Filter(f).Comments()) == 6, fmt.Sprintf("round %d filtered comments after GC=%d", r, len(cmap2.Filter(f).Comments())))
		pkg2, terr2 := typeCheck("demo.go", []byte(outAfter))
		ck(len(terr2) == 0 && pkg2 != nil, fmt.Sprintf("round %d type check after GC: %v", r, terr2))
	}

	finish("l16_ast_comment_rewrite")
}
