// L16: 路径栈式 AST 遍历（ast.Inspect + 剪枝），为每个标识符定位所属顶层函数、
// 区分定义/使用、shadowing，并验证索引跨 GC 稳定。
package main

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"math/rand"
	"os"
	"runtime"
	"strconv"
	"sync/atomic"
)

const src = `package demo

// Limit caps scores.
const Limit = 3

// rec is a record.
type rec struct {
	N int
}

func (r *rec) Score(n int) int {
	if n > Limit {
		return n * 2
	}
	return r.N + n
}

func walk(xs []int) int {
	total := 0
	f := func(v int) int {
		return v + Limit
	}
	for _, v := range xs {
		if v%2 == 0 {
			total += f(v)
		} else {
			total += v
		}
	}
	switch total {
	case 0:
		return 1
	default:
		return total
	}
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
	for i := 0; i < 64; i++ {
		_ = make([]ast.Node, 64)
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

type fact struct {
	name    string
	line    int
	def     bool
	topFunc string
	obj     *ast.Object
	path    []string
	depth   int
}

type index struct {
	idents   []fact
	funcs    []string
	maxDepth int
	nodes    int
}

func kindOf(n ast.Node) string {
	switch n.(type) {
	case *ast.File:
		return "File"
	case *ast.FuncDecl:
		return "FuncDecl"
	case *ast.FuncLit:
		return "FuncLit"
	case *ast.BlockStmt:
		return "BlockStmt"
	case *ast.AssignStmt:
		return "AssignStmt"
	case *ast.RangeStmt:
		return "RangeStmt"
	case *ast.IfStmt:
		return "IfStmt"
	case *ast.ForStmt:
		return "ForStmt"
	case *ast.SwitchStmt:
		return "SwitchStmt"
	case *ast.ReturnStmt:
		return "ReturnStmt"
	case *ast.CallExpr:
		return "CallExpr"
	case *ast.BinaryExpr:
		return "BinaryExpr"
	case *ast.Ident:
		return "Ident"
	case *ast.GenDecl:
		return "GenDecl"
	case *ast.TypeSpec:
		return "TypeSpec"
	case *ast.StructType:
		return "StructType"
	default:
		return "*"
	}
}

// build 单遍路径栈遍历；pruneFuncLit=true 时不进入函数字面量子树。
func build(fset *token.FileSet, file *ast.File, pruneFuncLit bool) index {
	var out index
	var stack []ast.Node
	ast.Inspect(file, func(n ast.Node) bool {
		if n == nil {
			stack = stack[:len(stack)-1]
			return true
		}
		stack = append(stack, n)
		out.nodes++
		if len(stack) > out.maxDepth {
			out.maxDepth = len(stack)
		}
		if pruneFuncLit {
			if _, ok := n.(*ast.FuncLit); ok {
				stack = stack[:len(stack)-1]
				return false
			}
		}
		if fd, ok := n.(*ast.FuncDecl); ok {
			out.funcs = append(out.funcs, fd.Name.Name)
		}
		if id, ok := n.(*ast.Ident); ok {
			f := fact{name: id.Name, depth: len(stack), line: fset.Position(id.Pos()).Line}
			if id.Obj != nil {
				f.obj = id.Obj
				f.def = id.Obj.Pos() == id.Pos()
			}
			for i := len(stack) - 2; i >= 0; i-- {
				if fd, ok := stack[i].(*ast.FuncDecl); ok {
					f.topFunc = fd.Name.Name
					break
				}
			}
			for _, nd := range stack[1 : len(stack)-1] {
				f.path = append(f.path, kindOf(nd))
			}
			out.idents = append(out.idents, f)
		}
		return true
	})
	return out
}

func hasKind(path []string, kind string) bool {
	for _, k := range path {
		if k == kind {
			return true
		}
	}
	return false
}

func countName(ix index, name string) int {
	n := 0
	for _, f := range ix.idents {
		if f.name == name {
			n++
		}
	}
	return n
}

func findLine(ix index, name string, line int) *fact {
	for i := range ix.idents {
		if ix.idents[i].name == name && ix.idents[i].line == line {
			return &ix.idents[i]
		}
	}
	return nil
}

func main() {
	uafSeed = envSeed()
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "demo.go", src, parser.ParseComments)
	ck(err == nil, fmt.Sprintf("parse: %v", err))
	if file == nil {
		finish("l16_ast_inspect_path")
		return
	}
	liveAST = append(liveAST, file)
	ck(len(file.Comments) == 2, fmt.Sprintf("comment groups=%d", len(file.Comments)))

	ix := build(fset, file, false)
	pruned := build(fset, file, true)

	// 顶层函数顺序与节点/深度规模。
	ck(len(ix.funcs) == 2, fmt.Sprintf("top funcs=%v", ix.funcs))
	if len(ix.funcs) == 2 {
		ck(ix.funcs[0] == "Score" && ix.funcs[1] == "walk", fmt.Sprintf("func order=%v", ix.funcs))
	}
	ck(ix.maxDepth >= 8, fmt.Sprintf("max depth=%d", ix.maxDepth))
	ck(ix.nodes > 60, fmt.Sprintf("nodes=%d", ix.nodes))

	// 剪枝函数字面量后，标识符差值应恰为字面量内的 v,int,int,v,Limit 五个。
	diff := len(ix.idents) - len(pruned.idents)
	ck(diff == 5, fmt.Sprintf("pruned ident diff=%d", diff))
	ck(countName(ix, "Limit")-countName(pruned, "Limit") == 1, "Limit inside func literal not pruned")

	// total：一个定义 + 四个使用，全部指向同一 *ast.Object。
	ck(countName(ix, "total") == 5, fmt.Sprintf("total uses=%d", countName(ix, "total")))
	var totalObj *ast.Object
	defs := 0
	for _, f := range ix.idents {
		if f.name != "total" {
			continue
		}
		if f.def {
			defs++
			ck(f.line == 19, fmt.Sprintf("total def line=%d", f.line))
		}
		if totalObj == nil {
			totalObj = f.obj
		}
		ck(f.obj == totalObj && f.obj != nil, "total object identity")
		ck(f.topFunc == "walk", fmt.Sprintf("total topFunc=%q", f.topFunc))
	}
	ck(defs == 1, fmt.Sprintf("total defs=%d", defs))

	// 两处 v：range 变量与函数字面量参数是两个不同对象（shadowing）。
	ck(countName(ix, "v") == 6, fmt.Sprintf("v uses=%d", countName(ix, "v")))
	rangeV := findLine(ix, "v", 23)
	litV := findLine(ix, "v", 20)
	ck(rangeV != nil && litV != nil, "v declarations missing")
	if rangeV != nil && litV != nil {
		ck(rangeV.def && litV.def, "v defs not marked")
		ck(rangeV.obj != litV.obj, "shadowed v objects must differ")
		ck(rangeV.topFunc == "walk" && litV.topFunc == "walk", "v topFunc")
		ck(hasKind(rangeV.path, "RangeStmt"), fmt.Sprintf("range v path=%v", rangeV.path))
		ck(hasKind(litV.path, "FuncLit"), fmt.Sprintf("literal v path=%v", litV.path))
		uses := 0
		for _, f := range ix.idents {
			if f.name == "v" && !f.def {
				uses++
				ck(f.obj == rangeV.obj || f.obj == litV.obj, "v use resolves outside two objects")
			}
		}
		ck(uses == 4, fmt.Sprintf("v use count=%d", uses))
	}

	// Limit：包级定义 + Score 内使用 + 函数字面量内使用，所属函数不同。
	ck(countName(ix, "Limit") == 3, fmt.Sprintf("Limit uses=%d", countName(ix, "Limit")))
	limitDef := findLine(ix, "Limit", 4)
	limitScore := findLine(ix, "Limit", 12)
	limitLit := findLine(ix, "Limit", 21)
	ck(limitDef != nil && limitScore != nil && limitLit != nil, "Limit occurrences missing")
	if limitDef != nil && limitScore != nil && limitLit != nil {
		ck(limitDef.def && !limitScore.def && !limitLit.def, "Limit def/use marks")
		ck(limitDef.topFunc == "", fmt.Sprintf("Limit def topFunc=%q", limitDef.topFunc))
		ck(limitScore.topFunc == "Score", fmt.Sprintf("Limit use topFunc=%q", limitScore.topFunc))
		ck(limitLit.topFunc == "walk", fmt.Sprintf("Limit literal topFunc=%q", limitLit.topFunc))
		ck(!hasKind(limitScore.path, "FuncLit"), "Score Limit must not see FuncLit")
		ck(hasKind(limitLit.path, "FuncLit") && hasKind(limitLit.path, "ReturnStmt"),
			fmt.Sprintf("literal Limit path=%v", limitLit.path))
		ck(limitDef.obj != nil && limitScore.obj == limitDef.obj && limitLit.obj == limitDef.obj, "Limit object identity")
	}

	// 接收者 r、参数 n 与 f 的定义/使用归属。
	for _, name := range []string{"r", "n", "f"} {
		var obj *ast.Object
		defs := 0
		for _, ft := range ix.idents {
			if ft.name != name {
				continue
			}
			if ft.def {
				defs++
			}
			if obj == nil {
				obj = ft.obj
			}
			ck(ft.obj == obj && ft.obj != nil, name+" object identity")
		}
		ck(defs == 1, fmt.Sprintf("%s defs=%d", name, defs))
	}
	ck(countName(ix, "n") == 4, fmt.Sprintf("n uses=%d", countName(ix, "n")))
	ck(countName(ix, "f") == 2, fmt.Sprintf("f uses=%d", countName(ix, "f")))
	fCall := findLine(ix, "f", 25)
	ck(fCall != nil && !fCall.def, "f call not a use")
	if fCall != nil {
		ck(hasKind(fCall.path, "CallExpr") && hasKind(fCall.path, "AssignStmt"), fmt.Sprintf("f call path=%v", fCall.path))
	}

	// GC 后重建索引：规模/行号/所属函数完全一致，对象指针仍有效。
	before := make([]string, 0, len(ix.idents))
	for _, f := range ix.idents {
		before = append(before, fmt.Sprintf("%s:%d:%v:%s", f.name, f.line, f.def, f.topFunc))
	}
	for round := 0; round < 3; round++ {
		gcStress()
		ix2 := build(fset, file, false)
		ck(len(ix2.idents) == len(ix.idents), fmt.Sprintf("round %d ident count after GC", round))
		for i := 0; i < len(ix2.idents) && i < len(before); i++ {
			f := ix2.idents[i]
			got := fmt.Sprintf("%s:%d:%v:%s", f.name, f.line, f.def, f.topFunc)
			ck(got == before[i], fmt.Sprintf("round %d ident[%d]=%s want %s", round, i, got, before[i]))
		}
		t := findLine(ix2, "total", 19)
		ck(t != nil && t.obj == totalObj, fmt.Sprintf("round %d total object changed after GC", round))
		// 用种子打乱待复核的标识符顺序。
		names := []string{"total", "v", "f", "Limit", "n", "r"}
		rng := rand.New(rand.NewSource(uafSeed + int64(round)))
		for _, i := range rng.Perm(len(names)) {
			ck(countName(ix2, names[i]) > 0, fmt.Sprintf("round %d lost %s", round, names[i]))
		}
	}

	finish("l16_ast_inspect_path")
}
