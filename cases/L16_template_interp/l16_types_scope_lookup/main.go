// L16: go/types 作用域与对象解析：包/函数/块/for/if/type-switch 作用域、
// shadowing、Uses/Defs/Implicits/Selections 与跨 GC 对象身份。
package main

import (
	"fmt"
	"go/ast"
	"go/constant"
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

const K = 3

type Pair struct {
	A, B int
}

func (p *Pair) Sum() int {
	return p.A + p.B
}

func use(p *Pair) int {
	return p.Sum()
}

func f(n int) int {
	x := n
	{
		x := x + K
		_ = x
	}
	for i := 0; i < K; i++ {
		x += i
	}
	if v := x * 2; v > K {
		return v
	}
	switch t := any(x).(type) {
	case int:
		return t
	}
	return x
}
`

var (
	failed  int32
	gcSink  [][]byte
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
		_ = make([]types.Object, 64)
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

type nodes struct {
	ifStmt   *ast.IfStmt
	forStmt  *ast.ForStmt
	tswitch  *ast.TypeSwitchStmt
	caseCl   *ast.CaseClause
	innerBlk *ast.BlockStmt
}

func findNodes(fset *token.FileSet, file *ast.File) nodes {
	var out nodes
	ast.Inspect(file, func(n ast.Node) bool {
		switch x := n.(type) {
		case *ast.IfStmt:
			if fset.Position(x.Pos()).Line == 26 {
				out.ifStmt = x
			}
		case *ast.ForStmt:
			if fset.Position(x.Pos()).Line == 23 {
				out.forStmt = x
			}
		case *ast.TypeSwitchStmt:
			out.tswitch = x
		case *ast.CaseClause:
			out.caseCl = x
		case *ast.BlockStmt:
			if fset.Position(x.Pos()).Line == 19 {
				out.innerBlk = x
			}
		}
		return true
	})
	return out
}

func identAt(file *ast.File, fset *token.FileSet, name string, line int) *ast.Ident {
	var out *ast.Ident
	ast.Inspect(file, func(n ast.Node) bool {
		if id, ok := n.(*ast.Ident); ok && id.Name == name && fset.Position(id.Pos()).Line == line {
			if out == nil {
				out = id
			}
		}
		return true
	})
	return out
}

func selectorAt(file *ast.File, fset *token.FileSet, line int, sel string) *ast.SelectorExpr {
	var out *ast.SelectorExpr
	ast.Inspect(file, func(n ast.Node) bool {
		if s, ok := n.(*ast.SelectorExpr); ok && s.Sel.Name == sel && fset.Position(s.Pos()).Line == line {
			if out == nil {
				out = s
			}
		}
		return true
	})
	return out
}

func main() {
	uafSeed = envSeed()
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "demo.go", src, 0)
	ck(err == nil, fmt.Sprintf("parse: %v", err))
	if file == nil {
		finish("l16_types_scope_lookup")
		return
	}

	info := &types.Info{
		Types:      map[ast.Expr]types.TypeAndValue{},
		Defs:       map[*ast.Ident]types.Object{},
		Uses:       map[*ast.Ident]types.Object{},
		Implicits:  map[ast.Node]types.Object{},
		Scopes:     map[ast.Node]*types.Scope{},
		Selections: map[*ast.SelectorExpr]*types.Selection{},
	}
	conf := types.Config{}
	pkg, err := conf.Check("demo", fset, []*ast.File{file}, info)
	ck(err == nil, fmt.Sprintf("type check: %v", err))
	if pkg == nil {
		finish("l16_types_scope_lookup")
		return
	}

	// 包作用域：名字排序、K/Pair/f/use 的类型。
	names := pkg.Scope().Names()
	wantNames := []string{"K", "Pair", "f", "use"}
	ck(strings.Join(names, ",") == strings.Join(wantNames, ","), fmt.Sprintf("package scope names=%v", names))
	kObj := pkg.Scope().Lookup("K")
	kc, ok := kObj.(*types.Const)
	ck(ok, fmt.Sprintf("K object type=%T", kObj))
	if ok {
		ck(kc.Val().Kind() == constant.Int && kc.Val().ExactString() == "3", fmt.Sprintf("K value=%v", kc.Val()))
		ck(kc.Parent() == pkg.Scope(), "K parent scope")
	}
	pairObj := pkg.Scope().Lookup("Pair")
	tn, ok := pairObj.(*types.TypeName)
	ck(ok, fmt.Sprintf("Pair object type=%T", pairObj))
	if ok {
		st, ok := tn.Type().Underlying().(*types.Struct)
		ck(ok && st.NumFields() == 2, "Pair struct fields")
		if ok && st.NumFields() == 2 {
			ck(st.Field(0).Name() == "A" && st.Field(1).Name() == "B", "Pair field order")
		}
	}
	fObj, ok := pkg.Scope().Lookup("f").(*types.Func)
	ck(ok, "f object type")
	if ok {
		sig := fObj.Type().(*types.Signature)
		ck(sig.Params().Len() == 1 && sig.Results().Len() == 1, "f signature arity")
		ck(sig.Params().At(0).Name() == "n", "f param name")
		ck(pkg.Scope().Lookup("use") != nil, "use func missing")
	}

	// universe 作用域。
	ck(types.Universe.Lookup("int") != nil, "universe int missing")
	ck(types.Universe.Lookup("any") != nil, "universe any missing")
	ck(types.Universe.Lookup("len") != nil, "universe len missing")

	// 函数作用域：参数 n 与顶层局部 x。
	var fScope *types.Scope
	if ok {
		fScope = fObj.Scope()
	}
	ck(fScope != nil, "f scope missing")
	if fScope != nil {
		got := fScope.Names()
		ck(strings.Join(got, ",") == "n,x", fmt.Sprintf("f scope names=%v", got))
		ck(fScope.Lookup("n") != nil && fScope.Lookup("x") != nil, "f scope lookups")
		ck(fScope.Parent() != nil && fScope.Parent().Parent() == pkg.Scope(), "f scope parent chain")
	}

	ns := findNodes(fset, file)
	ck(ns.ifStmt != nil && ns.forStmt != nil && ns.tswitch != nil && ns.caseCl != nil && ns.innerBlk != nil,
		"control-flow nodes missing")

	// shadowing：块内 x 与函数级 x 是不同对象、不同父作用域。
	outerX := identAt(file, fset, "x", 18)
	innerX := identAt(file, fset, "x", 20)
	ck(outerX != nil && innerX != nil, "x idents missing")
	if outerX != nil && innerX != nil {
		oObj, iObj := info.Defs[outerX], info.Defs[innerX]
		ck(oObj != nil && iObj != nil, "x defs missing")
		ck(oObj != iObj, "shadowed x must be distinct objects")
		ck(oObj.Parent() != iObj.Parent(), "shadowed x parent scopes equal")
		if fScope != nil {
			ck(oObj.Parent() == fScope, "outer x parent is function scope")
		}
		if ns.innerBlk != nil {
			blk := info.Scopes[ns.innerBlk]
			ck(blk != nil && blk.Lookup("x") == iObj, "inner block scope x lookup")
			if blk != nil {
				ck(blk.Parent() == fScope, "inner block parent")
			}
		}
		// LookupParent 从内层块找到包级 K（在声明位置回查）。
		if blk := info.Scopes[ns.innerBlk]; blk != nil {
			kScope, kFound := blk.LookupParent("K", innerX.Pos()+1)
			ck(kFound != nil && kScope == pkg.Scope() && kFound == kObj, "LookupParent(K) from inner block")
		}
	}
	// 内层 RHS 的 x 使用解析到外层 x。
	innerUse := identAt(file, fset, "x", 20)
	_ = innerUse
	// 行 20 中第二个 x（RHS）单独定位：x := x + K。
	var rhsX *ast.Ident
	seen := 0
	ast.Inspect(file, func(n ast.Node) bool {
		if id, ok := n.(*ast.Ident); ok && id.Name == "x" && fset.Position(id.Pos()).Line == 20 {
			seen++
			if seen == 2 {
				rhsX = id
			}
		}
		return true
	})
	ck(rhsX != nil, "inner RHS x missing")
	if rhsX != nil && outerX != nil {
		ck(info.Uses[rhsX] == info.Defs[outerX], "inner RHS x must resolve outer x")
	}

	// n 参数定义与使用；for 变量 i、if 变量 v 的作用域不泄漏。
	paramN := identAt(file, fset, "n", 17)
	useN := identAt(file, fset, "n", 18)
	ck(paramN != nil && useN != nil, "n idents missing")
	if paramN != nil && useN != nil {
		ck(info.Defs[paramN] != nil && info.Uses[useN] == info.Defs[paramN], "n resolution")
	}
	forObj := identAt(file, fset, "i", 23)
	ck(forObj != nil && info.Defs[forObj] != nil, "for i def missing")
	if ns.forStmt != nil {
		fs := info.Scopes[ns.forStmt]
		ck(fs != nil && fs.Lookup("i") == info.Defs[forObj], "for scope i lookup")
	}
	if fScope != nil {
		ck(fScope.Lookup("i") == nil, "for i leaked into function scope")
		ck(fScope.Lookup("v") == nil, "if v leaked into function scope")
	}
	vObj := identAt(file, fset, "v", 26)
	ck(vObj != nil && info.Defs[vObj] != nil, "if v def missing")
	if ns.ifStmt != nil {
		ifs := info.Scopes[ns.ifStmt]
		ck(ifs != nil && ifs.Lookup("v") == info.Defs[vObj], "if scope v lookup")
	}
	// 内层 x 的 Defs 被记录；空白标识符不产生对象。
	blank := identAt(file, fset, "_", 21)
	ck(blank != nil && info.Defs[blank] == nil && info.Uses[blank] == nil, "blank ident must not be an object")

	// type switch：一个 case，隐式变量 t 记录在 Implicits[CaseClause]。
	caseImplicits := 0
	var implicit types.Object
	for node, obj := range info.Implicits {
		if _, ok := node.(*ast.CaseClause); ok {
			caseImplicits++
			implicit = obj
		}
	}
	ck(caseImplicits == 1, fmt.Sprintf("case implicits=%d", caseImplicits))
	ck(implicit != nil, "type switch implicit var missing")
	if implicit != nil {
		ck(implicit.Name() == "t", fmt.Sprintf("implicit name=%s", implicit.Name()))
		ck(implicit.Type().String() == "int", fmt.Sprintf("implicit type=%s", implicit.Type()))
		if ns.caseCl != nil {
			cs := info.Scopes[ns.caseCl]
			ck(cs != nil && cs.Lookup("t") == implicit, "case scope t lookup")
		}
		tUse := identAt(file, fset, "t", 31)
		ck(tUse != nil && info.Uses[tUse] == implicit, "t use resolves implicit")
		tDef := identAt(file, fset, "t", 29)
		ck(tDef != nil && info.Defs[tDef] == nil, "type switch guard t must be implicit")
	}

	// Selections：字段 A/B 与方法 Sum。
	aSel := selectorAt(file, fset, 10, "A")
	bSel := selectorAt(file, fset, 10, "B")
	sumSel := selectorAt(file, fset, 14, "Sum")
	ck(aSel != nil && bSel != nil && sumSel != nil, "selectors missing")
	if aSel != nil && bSel != nil && sumSel != nil {
		sa, sb, ss := info.Selections[aSel], info.Selections[bSel], info.Selections[sumSel]
		ck(sa != nil && sb != nil && ss != nil, "selections missing")
		if sa != nil && sb != nil && ss != nil {
			ck(sa.Kind() == types.FieldVal && sb.Kind() == types.FieldVal, "field selection kind")
			ck(sa.Obj().Name() == "A" && sb.Obj().Name() == "B", "field selection names")
			ck(sa.Index()[0] == 0 && sb.Index()[0] == 1, "field selection indices")
			ck(ss.Kind() == types.MethodVal, fmt.Sprintf("method selection kind=%v", ss.Kind()))
			ck(ss.Obj().Name() == "Sum", "method selection name")
			ck(ss.Recv().String() == "*demo.Pair", fmt.Sprintf("method recv=%s", ss.Recv()))
			ck(ss.Index()[0] == 0, "method selection index")
		}
	}
	// 表达式类型。
	if aSel != nil {
		if tv, ok := info.Types[aSel]; ok {
			ck(tv.Type.String() == "int", fmt.Sprintf("A expr type=%s", tv.Type))
		} else {
			ck(false, "A expr type missing")
		}
	}

	// GC 后对象/作用域/选择仍有效且身份不变。
	methodSetBefore := types.NewMethodSet(types.NewPointer(tn.Type()))
	ck(methodSetBefore.Len() == 1 && methodSetBefore.At(0).Obj().Name() == "Sum", "Pair method set")
	for round := 0; round < 3; round++ {
		gcStress()
		ck(pkg.Scope().Lookup("K") == kObj, fmt.Sprintf("round %d K object changed", round))
		ck(pkg.Scope().Lookup("f") == fObj, fmt.Sprintf("round %d f object changed", round))
		if fScope != nil {
			ck(fScope.Lookup("x") == info.Defs[outerX], fmt.Sprintf("round %d outer x changed", round))
		}
		if aSel != nil {
			ck(info.Selections[aSel] != nil && info.Selections[aSel].Obj().Name() == "A",
				fmt.Sprintf("round %d selection lost", round))
		}
		ms := types.NewMethodSet(types.NewPointer(tn.Type()))
		ck(ms.Len() == 1 && ms.At(0).Obj() == methodSetBefore.At(0).Obj(), fmt.Sprintf("round %d method set changed", round))
		// 种子打乱抽查顺序。
		names := []string{"K", "Pair", "f", "use"}
		rng := rand.New(rand.NewSource(uafSeed + int64(round)))
		for _, i := range rng.Perm(len(names)) {
			ck(pkg.Scope().Lookup(names[i]) != nil, fmt.Sprintf("round %d lost %s", round, names[i]))
		}
	}

	finish("l16_types_scope_lookup")
}
