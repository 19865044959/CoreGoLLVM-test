// L16: 路径栈式 AST 遍历与 go/types Defs/Uses 联动：为每个标识符记录节点路径、
// 所属函数/函数字面量，校验定义-使用对象身份、type-switch 隐式变量与选择器，
// 剪枝函数字面量后复算；路径与对象跨 GC 稳定。
package main

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"go/types"
	"math/rand"
	"os"
	"runtime"
	"strconv"
	"strings"
	"sync/atomic"
)

const src = `package demo

type T struct{ N int }

func (t *T) Get() int { return t.N }

func total(xs []int) int {
	sum := 0
	for _, x := range xs {
		f := func(y int) int {
			if y > 0 {
				return y * x
			}
			return -y
		}
		sum += f(x)
	}
	switch v := any(sum).(type) {
	case int:
		return v + sum
	}
	return sum
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
	isDef   bool
	isUse   bool
	obj     types.Object
	path    []string
	topFunc string
	inLit   bool
}

type index struct {
	facts    []fact
	maxDepth int
	nodes    int
}

func pathOf(stack []ast.Node) []string {
	out := make([]string, 0, len(stack))
	for _, n := range stack {
		out = append(out, fmt.Sprintf("%T", n))
	}
	return out
}

func hasPath(path []string, kind string) bool {
	for _, p := range path {
		if p == kind {
			return true
		}
	}
	return false
}

// build 单遍路径栈遍历；pruneLit 时不进入函数字面量子树。
func build(fset *token.FileSet, file *ast.File, info *types.Info, pruneLit bool) index {
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
		if pruneLit {
			if _, ok := n.(*ast.FuncLit); ok {
				stack = stack[:len(stack)-1]
				return false
			}
		}
		id, ok := n.(*ast.Ident)
		if !ok {
			return true
		}
		defObj := info.Defs[id]
		useObj := info.Uses[id]
		if id.Name == "_" || (defObj == nil && useObj == nil) {
			return true
		}
		f := fact{name: id.Name, line: fset.Position(id.Pos()).Line, isDef: defObj != nil, isUse: useObj != nil}
		if f.isDef {
			f.obj = defObj
		} else {
			f.obj = useObj
		}
		for i := len(stack) - 1; i >= 0; i-- {
			if fd, ok := stack[i].(*ast.FuncDecl); ok {
				f.topFunc = fd.Name.Name
				break
			}
		}
		f.inLit = hasPath(pathOf(stack), "*ast.FuncLit")
		f.path = pathOf(stack[1 : len(stack)-1])
		out.facts = append(out.facts, f)
		return true
	})
	return out
}

func countName(ix index, name string) int {
	n := 0
	for _, f := range ix.facts {
		if f.name == name {
			n++
		}
	}
	return n
}

func find(ix index, name string, line int, def bool) *fact {
	for i := range ix.facts {
		f := &ix.facts[i]
		if f.name == name && f.line == line && f.isDef == def {
			return f
		}
	}
	return nil
}

func digest(ix index) []string {
	out := make([]string, 0, len(ix.facts))
	for _, f := range ix.facts {
		out = append(out, fmt.Sprintf("%s:%d:%v:%v:%s:%s", f.name, f.line, f.isDef, f.isUse, f.topFunc, strings.Join(f.path, "/")))
	}
	return out
}

func main() {
	uafSeed = envSeed()
	rng := rand.New(rand.NewSource(uafSeed))

	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "demo.go", src, parser.ParseComments)
	ck(err == nil, fmt.Sprintf("parse: %v", err))
	if file == nil {
		finish("l16_ast_path_defuse")
		return
	}
	liveAST = append(liveAST, file)

	info := &types.Info{
		Types:      map[ast.Expr]types.TypeAndValue{},
		Defs:       map[*ast.Ident]types.Object{},
		Uses:       map[*ast.Ident]types.Object{},
		Implicits:  map[ast.Node]types.Object{},
		Selections: map[*ast.SelectorExpr]*types.Selection{},
		Scopes:     map[ast.Node]*types.Scope{},
	}
	pkg, terr := (&types.Config{}).Check("demo", fset, []*ast.File{file}, info)
	ck(terr == nil, fmt.Sprintf("type check: %v", terr))
	if pkg == nil {
		finish("l16_ast_path_defuse")
		return
	}

	ix := build(fset, file, info, false)
	pruned := build(fset, file, info, true)
	ck(ix.nodes > 80, fmt.Sprintf("nodes=%d", ix.nodes))
	ck(ix.maxDepth >= 8, fmt.Sprintf("max depth=%d", ix.maxDepth))
	ck(len(ix.facts) >= 30, fmt.Sprintf("ident facts=%d", len(ix.facts)))

	// 每个标识符要么是定义、要么是使用（type switch 隐式变量只出现在 Uses）。
	for _, f := range ix.facts {
		ck(f.isDef != f.isUse || f.name == "v", fmt.Sprintf("%s line %d def=%v use=%v", f.name, f.line, f.isDef, f.isUse))
		ck(f.obj != nil, fmt.Sprintf("%s line %d nil object", f.name, f.line))
	}

	// sum：1 个定义 + 4 个使用，全部同对象，属于 total。
	ck(countName(ix, "sum") == 5, fmt.Sprintf("sum count=%d", countName(ix, "sum")))
	sumDef := find(ix, "sum", 8, true)
	ck(sumDef != nil, "sum def missing")
	if sumDef != nil {
		for _, f := range ix.facts {
			if f.name == "sum" {
				ck(f.obj == sumDef.obj, "sum object identity")
				ck(f.topFunc == "total", fmt.Sprintf("sum topFunc=%q", f.topFunc))
			}
		}
		ck(!sumDef.inLit, "sum def inside func literal")
	}

	// x：range 定义 + 函数字面量内两处使用；路径必须穿过 FuncLit。
	xDef := find(ix, "x", 9, true)
	ck(xDef != nil, "x range def missing")
	var xUse *fact
	for i := range ix.facts {
		if ix.facts[i].name == "x" && !ix.facts[i].isDef && ix.facts[i].line == 12 {
			xUse = &ix.facts[i]
		}
	}
	ck(xUse != nil, "x use in func literal missing")
	if xDef != nil && xUse != nil {
		ck(xUse.obj == xDef.obj, "x use must resolve range var")
		ck(xUse.inLit, "x use must be inside FuncLit")
		ck(hasPath(xUse.path, "*ast.FuncLit") && hasPath(xUse.path, "*ast.IfStmt") &&
			hasPath(xUse.path, "*ast.ReturnStmt") && hasPath(xUse.path, "*ast.BinaryExpr"),
			fmt.Sprintf("x use path=%v", xUse.path))
		ck(!hasPath(xDef.path, "*ast.FuncLit"), fmt.Sprintf("x def path=%v", xDef.path))
	}
	ck(countName(ix, "x") == 3, fmt.Sprintf("x count=%d", countName(ix, "x")))

	// y：参数定义 + 两处使用，路径含 FuncLit/IfStmt/BinaryExpr。
	yDef := find(ix, "y", 10, true)
	ck(yDef != nil && yDef.inLit, "y def must be inside FuncLit")
	ck(countName(ix, "y") == 4, fmt.Sprintf("y count=%d", countName(ix, "y")))
	for _, f := range ix.facts {
		if f.name == "y" {
			ck(yDef != nil && f.obj == yDef.obj, "y object identity")
			ck(f.inLit, "y must stay inside FuncLit")
		}
	}

	// f：循环体内定义、下一行调用；两处同对象且不在 FuncLit 内。
	fDef := find(ix, "f", 10, true)
	fUse := find(ix, "f", 16, false)
	ck(fDef != nil && fUse != nil, "f def/use missing")
	if fDef != nil && fUse != nil {
		ck(fDef.obj == fUse.obj, "f object identity")
		ck(!fDef.inLit && !fUse.inLit, "f must live outside FuncLit")
		ck(hasPath(fUse.path, "*ast.CallExpr"), fmt.Sprintf("f use path=%v", fUse.path))
	}

	// t.N：选择器字段使用，Selections 为 FieldVal，路径含 SelectorExpr。
	nUse := find(ix, "N", 5, false)
	tUse := find(ix, "t", 5, false)
	ck(nUse != nil && tUse != nil, "t/N use missing")
	if nUse != nil && tUse != nil {
		ck(hasPath(nUse.path, "*ast.SelectorExpr"), fmt.Sprintf("N path=%v", nUse.path))
		ck(nUse.obj == pkg.Scope().Lookup("T").(*types.TypeName).Type().Underlying().(*types.Struct).Field(0),
			"N must resolve struct field object")
		ck(tUse.obj != nil && tUse.obj.Name() == "t", "t receiver object")
	}
	var sel *ast.SelectorExpr
	ast.Inspect(file, func(n ast.Node) bool {
		if s, ok := n.(*ast.SelectorExpr); ok {
			if id, ok := s.X.(*ast.Ident); ok && id.Name == "t" {
				sel = s
			}
		}
		return true
	})
	ck(sel != nil, "t.N selector missing")
	if sel != nil {
		selection := info.Selections[sel]
		ck(selection != nil && selection.Kind() == types.FieldVal, "t.N selection kind")
		if selection != nil {
			ck(selection.Obj().Name() == "N", "t.N selection object")
			ck(selection.Index()[0] == 0, "t.N selection index")
		}
	}

	// type switch 隐式变量：守卫处的 v 既无 Defs 也无 Uses，case 内的 v 指向 Implicits[CaseClause]。
	var vUse *fact
	for i := range ix.facts {
		if ix.facts[i].name == "v" && ix.facts[i].isUse && ix.facts[i].line == 20 {
			vUse = &ix.facts[i]
		}
	}
	ck(vUse != nil, "type switch case v use missing")
	var guardV *ast.Ident
	ast.Inspect(file, func(n ast.Node) bool {
		if id, ok := n.(*ast.Ident); ok && id.Name == "v" && fset.Position(id.Pos()).Line == 18 {
			guardV = id
		}
		return true
	})
	ck(guardV != nil && info.Defs[guardV] == nil && info.Uses[guardV] == nil, "type switch guard v must have no object")
	var implicit types.Object
	for node, obj := range info.Implicits {
		if _, ok := node.(*ast.CaseClause); ok {
			implicit = obj
		}
	}
	ck(implicit != nil, "type switch implicit object missing")
	if vUse != nil && implicit != nil {
		ck(vUse.obj == implicit, "v use must resolve implicit")
		ck(implicit.Parent() != nil && implicit.Parent().Parent() != nil, "implicit parent chain")
	}
	vDefs := info.Defs
	nvDef := 0
	ast.Inspect(file, func(n ast.Node) bool {
		if id, ok := n.(*ast.Ident); ok && id.Name == "v" && vDefs[id] != nil {
			nvDef++
		}
		return true
	})
	ck(nvDef == 0, fmt.Sprintf("type switch v must not have Defs, got %d", nvDef))
	ck(countName(ix, "v") == 1, fmt.Sprintf("v count=%d", countName(ix, "v")))

	// 剪枝函数字面量：差值恰为 y 定义、两个 int 类型、三处 y 使用与一处 x 使用。
	diff := len(ix.facts) - len(pruned.facts)
	ck(diff == 7, fmt.Sprintf("pruned ident diff=%d", diff))
	ck(countName(pruned, "y") == 0, "y must be pruned")
	ck(countName(pruned, "f") == 2, "f must survive pruning")

	// GC 后重建索引：顺序/路径/对象身份一致；种子打乱抽查名字。
	before := digest(ix)
	objSnap := map[string]types.Object{}
	for _, name := range []string{"sum", "x", "y", "f", "N", "t", "v"} {
		for _, f := range ix.facts {
			if f.name == name {
				objSnap[name] = f.obj
				break
			}
		}
	}
	probe := []string{"sum", "x", "y", "f", "N", "t", "v"}
	for round := 0; round < 3; round++ {
		gcStress()
		ix2 := build(fset, file, info, false)
		after := digest(ix2)
		ck(len(after) == len(before), fmt.Sprintf("round %d fact count changed", round))
		for i := 0; i < len(after) && i < len(before); i++ {
			ck(after[i] == before[i], fmt.Sprintf("round %d fact[%d] changed", round, i))
		}
		for _, idx := range rng.Perm(len(probe)) {
			name := probe[idx]
			found := false
			for _, f := range ix2.facts {
				if f.name == name {
					ck(f.obj == objSnap[name], fmt.Sprintf("round %d %s object changed", round, name))
					found = true
					break
				}
			}
			ck(found, fmt.Sprintf("round %d lost %s", round, name))
		}
	}

	finish("l16_ast_path_defuse")
}
