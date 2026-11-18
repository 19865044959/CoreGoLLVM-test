// L16: go/parser 解析源码并用 ast.Walk 统计节点/校验结构；AST 跨 GC 仍可遍历。
package main

import (
	"bytes"
	"fmt"
	"go/ast"
	"go/parser"
	"go/printer"
	"go/token"
	"os"
	"reflect"
	"runtime"
	"sync/atomic"
)

const src = `package demo

// comment
const (
	A = 1
	B = 2
)

var counter int

type pair struct {
	K string
	V int
}

type summer interface {
	Sum() int
}

func (p *pair) Sum() int {
	total := 0
	if p == nil {
		return total
	}
	for i := 0; i < p.V; i++ {
		total += i
	}
	return total
}

func classify(n int) string {
	switch {
	case n < 0:
		return "neg"
	case n == 0:
		return "zero"
	default:
		return "pos"
	}
}

func build(list []int) []int {
	out := make([]int, 0, len(list))
	for _, v := range list {
		if v%2 == 0 {
			out = append(out, v)
		}
	}
	return out
}

func main() {
	p := &pair{K: "x", V: 4}
	_ = p.Sum()
	_ = classify(3)
	_ = build([]int{1, 2, 3, 4})
}
`

var (
	failed int32
	gcSink [][]byte
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
		fmt.Printf("FAILED %s (%d failed checks)\n", name, n)
		os.Exit(1)
	}
	fmt.Println("ALL PASS " + name)
}

type frame struct {
	pos, end token.Pos
	typ      string
}

type counter struct {
	counts     map[string]int
	total      int
	depth      int
	maxDepth   int
	badRange   int
	badNesting int
	stack      []frame
}

func newCounter() *counter {
	return &counter{counts: make(map[string]int)}
}

func (c *counter) Visit(n ast.Node) ast.Visitor {
	if n == nil {
		if len(c.stack) > 0 {
			c.stack = c.stack[:len(c.stack)-1]
		}
		c.depth--
		return nil
	}
	typ := reflect.TypeOf(n).String()
	if n.Pos() > n.End() {
		c.badRange++
	}
	if len(c.stack) > 0 {
		p := c.stack[len(c.stack)-1]
		// 注释节点可以位于其宿主声明之前，不参与包含关系检查。
		isComment := typ == "*ast.CommentGroup" || typ == "*ast.Comment"
		if !isComment && p.typ != "*ast.File" && (n.Pos() < p.pos || n.End() > p.end) {
			c.badNesting++
		}
	}
	c.total++
	c.counts[typ]++
	c.depth++
	if c.depth > c.maxDepth {
		c.maxDepth = c.depth
	}
	c.stack = append(c.stack, frame{pos: n.Pos(), end: n.End(), typ: typ})
	return c
}

func walk(f *ast.File) *counter {
	c := newCounter()
	ast.Walk(c, f)
	return c
}

func findFunc(f *ast.File, name string) *ast.FuncDecl {
	var found *ast.FuncDecl
	ast.Inspect(f, func(n ast.Node) bool {
		if fd, ok := n.(*ast.FuncDecl); ok && fd.Name != nil && fd.Name.Name == name {
			found = fd
		}
		return true
	})
	return found
}

func main() {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "demo.go", src, parser.ParseComments|parser.AllErrors)
	ck(err == nil, fmt.Sprintf("parse: %v", err))
	if err != nil {
		finish("l16_ast_walk")
		return
	}

	want := map[string]int{
		"*ast.FuncDecl":      4,
		"*ast.GenDecl":       4,
		"*ast.IfStmt":        2,
		"*ast.ForStmt":       1,
		"*ast.RangeStmt":     1,
		"*ast.SwitchStmt":    1,
		"*ast.CaseClause":    3,
		"*ast.ReturnStmt":    6,
		"*ast.CallExpr":      6,
		"*ast.CompositeLit":  2,
		"*ast.BinaryExpr":    6,
		"*ast.Ident":         66,
		"*ast.BasicLit":      19,
		"*ast.StarExpr":      1,
		"*ast.InterfaceType": 1,
		"*ast.StructType":    1,
		"*ast.SelectorExpr":  2,
	}

	c := walk(f)
	ck(c.badRange == 0, fmt.Sprintf("nodes with Pos>End: %d", c.badRange))
	ck(c.badNesting == 0, fmt.Sprintf("nodes outside parent range: %d", c.badNesting))
	ck(c.total == 187, fmt.Sprintf("total nodes=%d want 187", c.total))
	ck(c.maxDepth == 10, fmt.Sprintf("max walk depth=%d want 10", c.maxDepth))
	for typ, n := range want {
		ck(c.counts[typ] == n, fmt.Sprintf("count %s=%d want %d", typ, c.counts[typ], n))
	}
	ck(len(f.Comments) == 1, fmt.Sprintf("comment groups=%d want 1", len(f.Comments)))
	ck(len(f.Decls) == 8, fmt.Sprintf("top-level decls=%d want 8", len(f.Decls)))

	var declKinds []token.Token
	var funcNames []string
	for _, d := range f.Decls {
		switch d := d.(type) {
		case *ast.GenDecl:
			declKinds = append(declKinds, d.Tok)
		case *ast.FuncDecl:
			declKinds = append(declKinds, token.FUNC)
			funcNames = append(funcNames, d.Name.Name)
		}
	}
	wantKinds := []token.Token{token.CONST, token.VAR, token.TYPE, token.TYPE, token.FUNC, token.FUNC, token.FUNC, token.FUNC}
	if len(declKinds) == len(wantKinds) {
		for i := range wantKinds {
			ck(declKinds[i] == wantKinds[i], fmt.Sprintf("decl %d kind=%v want %v", i, declKinds[i], wantKinds[i]))
		}
	} else {
		ck(false, fmt.Sprintf("decl kind count=%d want %d", len(declKinds), len(wantKinds)))
	}
	wantFuncs := []string{"Sum", "classify", "build", "main"}
	if len(funcNames) == len(wantFuncs) {
		for i := range wantFuncs {
			ck(funcNames[i] == wantFuncs[i], fmt.Sprintf("func %d=%q want %q", i, funcNames[i], wantFuncs[i]))
		}
	} else {
		ck(false, fmt.Sprintf("func count=%d want %d", len(funcNames), len(wantFuncs)))
	}

	sum := findFunc(f, "Sum")
	ck(sum != nil, "Sum func not found")
	if sum != nil {
		ck(sum.Recv != nil && len(sum.Recv.List) == 1, "Sum must have one receiver")
		if sum.Recv != nil && len(sum.Recv.List) == 1 {
			_, isStar := sum.Recv.List[0].Type.(*ast.StarExpr)
			ck(isStar, "Sum receiver must be *pair")
		}
		p := fset.Position(sum.Pos())
		np := fset.Position(sum.Name.Pos())
		ck(p.Line == 20 && p.Column == 1, fmt.Sprintf("Sum pos=%d:%d want 20:1", p.Line, p.Column))
		ck(np.Line == 20 && np.Column == 16, fmt.Sprintf("Sum name pos=%d:%d want 20:16", np.Line, np.Column))
	}
	ck(fset.Position(f.Package).Line == 1, fmt.Sprintf("package line=%d want 1", fset.Position(f.Package).Line))
	ck(f.Comments[0].List[0].Text == "// comment", "comment text changed")

	gcStress()

	c2 := walk(f)
	ck(c2.badRange == 0 && c2.badNesting == 0, "walk after GC reported invalid ranges")
	ck(c2.total == c.total && c2.maxDepth == c.maxDepth, fmt.Sprintf("walk after GC total=%d depth=%d want %d/%d",
		c2.total, c2.maxDepth, c.total, c.maxDepth))
	for typ, n := range want {
		ck(c2.counts[typ] == n, fmt.Sprintf("after GC count %s=%d want %d", typ, c2.counts[typ], n))
	}
	sum2 := findFunc(f, "Sum")
	ck(sum2 != nil && sum2.Name.Name == "Sum", "Sum func lost after GC")
	ck(f.Comments[0].List[0].Text == "// comment", "comment text lost after GC")
	ck(fset.Position(f.Package).Line == 1, "fset line mapping broken after GC")

	var buf bytes.Buffer
	ck(printer.Fprint(&buf, fset, f) == nil, "printer.Fprint failed")
	f2, err := parser.ParseFile(token.NewFileSet(), "demo.go", buf.Bytes(), parser.ParseComments|parser.AllErrors)
	ck(err == nil, fmt.Sprintf("reparse printed AST: %v", err))
	if err == nil {
		c3 := walk(f2)
		ck(c3.total == c.total, fmt.Sprintf("reparsed total=%d want %d", c3.total, c.total))
		ck(c3.maxDepth == c.maxDepth, fmt.Sprintf("reparsed depth=%d want %d", c3.maxDepth, c.maxDepth))
		for typ, n := range want {
			ck(c3.counts[typ] == n, fmt.Sprintf("reparsed count %s=%d want %d", typ, c3.counts[typ], n))
		}
		ck(findFunc(f2, "main") != nil, "reparsed AST missing main")
	}

	finish("l16_ast_walk")
}
