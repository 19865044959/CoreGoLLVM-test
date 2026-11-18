// L16: go/types 泛型作用域边界：类型参数/接收者类型参数/实例化方法集/实例快照，
// 函数作用域同时含类型参数与局部变量，type-switch 隐式变量位于 case 作用域；跨 GC。
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
	"sync/atomic"
)

const src = `package demo

type Box[T any] struct { V T }

func (b *Box[T]) Get() T { return b.V }

type Number interface{ ~int | ~int64 }

func Map[T any, U any](xs []T, f func(T) U) []U {
	out := make([]U, 0, len(xs))
	for _, x := range xs { out = append(out, f(x)) }
	return out
}

func Use() int {
	b := Box[int]{V: 3}
	_ = b.Get()
	ys := Map([]int{1, 2}, func(x int) int { return x + 1 })
	var i any = ys
	switch v := i.(type) {
	case []int:
		return len(v)
	}
	return 0
}

func Sum[T Number](xs []T) T {
	var s T
	for _, x := range xs { s += x }
	return s
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
		b := make([]byte, 8192)
		b[0] = byte(i)
		gcSink = append(gcSink[:0], b)
	}
	for i := 0; i < 64; i++ {
		_ = make([]types.Object, 64)
		_ = make([]*types.Scope, 32)
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

func findFuncDecl(file *ast.File, name string) *ast.FuncDecl {
	for _, d := range file.Decls {
		if fd, ok := d.(*ast.FuncDecl); ok && fd.Name.Name == name {
			return fd
		}
	}
	return nil
}

func main() {
	uafSeed = envSeed()
	rng := rand.New(rand.NewSource(uafSeed))

	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "generic.go", src, 0)
	ck(err == nil, fmt.Sprintf("parse: %v", err))
	if file == nil {
		finish("l16_types_scope_generic")
		return
	}

	info := &types.Info{
		Types:      map[ast.Expr]types.TypeAndValue{},
		Defs:       map[*ast.Ident]types.Object{},
		Uses:       map[*ast.Ident]types.Object{},
		Implicits:  map[ast.Node]types.Object{},
		Scopes:     map[ast.Node]*types.Scope{},
		Selections: map[*ast.SelectorExpr]*types.Selection{},
		Instances:  map[*ast.Ident]types.Instance{},
	}
	pkg, cerr := (&types.Config{}).Check("demo", fset, []*ast.File{file}, info)
	ck(cerr == nil, fmt.Sprintf("type check: %v", cerr))
	if pkg == nil {
		finish("l16_types_scope_generic")
		return
	}
	live = append(live, pkg)

	names := pkg.Scope().Names()
	ck(fmt.Sprint(names) == "[Box Map Number Sum Use]", fmt.Sprintf("package scope=%v", names))
	ck(pkg.Scope().Lookup("T") == nil, "type parameter T leaked into package scope")

	// 泛型类型：TypeParams 与约束。
	boxTN, ok := pkg.Scope().Lookup("Box").(*types.TypeName)
	ck(ok, "Box TypeName missing")
	if !ok {
		finish("l16_types_scope_generic")
		return
	}
	boxNamed := boxTN.Type().(*types.Named)
	ck(boxNamed.TypeParams().Len() == 1, fmt.Sprintf("Box type params=%d", boxNamed.TypeParams().Len()))
	tp := boxNamed.TypeParams().At(0)
	ck(tp.Obj().Name() == "T", fmt.Sprintf("Box type param=%s", tp.Obj().Name()))
	iface, ok := tp.Constraint().Underlying().(*types.Interface)
	ck(ok && iface.NumMethods() == 0, "Box[T] constraint must be any")
	ck(boxNamed.NumMethods() == 1, fmt.Sprintf("Box methods=%d", boxNamed.NumMethods()))
	method := boxNamed.Method(0)
	ck(method.Name() == "Get", fmt.Sprintf("Box method=%s", method.Name()))
	sig := method.Type().(*types.Signature)
	ck(sig.RecvTypeParams().Len() == 1, "Get receiver type params")
	ck(sig.Recv() != nil && sig.Recv().Type().String() == "*demo.Box[T]", fmt.Sprintf("Get recv=%v", sig.Recv()))
	ck(method.Scope() != nil && fmt.Sprint(method.Scope().Names()) == "[T b]",
		fmt.Sprintf("Get scope=%v", method.Scope().Names()))
	if method.Scope() != nil {
		ck(method.Scope().Lookup("T") == sig.RecvTypeParams().At(0).Obj(), "Get scope T object identity")
		ck(method.Scope().Lookup("b") != nil, "Get scope b missing")
	}

	// 普通泛型函数：作用域同时包含类型参数、参数和局部变量。
	mapFD := findFuncDecl(file, "Map")
	ck(mapFD != nil, "Map decl missing")
	mapFn := pkg.Scope().Lookup("Map").(*types.Func)
	ms := mapFn.Scope()
	ck(ms != nil && fmt.Sprint(ms.Names()) == "[T U f out xs]", fmt.Sprintf("Map scope=%v", ms.Names()))
	if ms != nil {
		if mapFD != nil {
			ck(info.Scopes[mapFD.Type] == ms, "Map type node scope must equal func scope")
		}
		tObj := ms.Lookup("T")
		tn, ok := tObj.(*types.TypeName)
		ck(ok && tn.IsAlias() == false && tn.Type().(*types.TypeParam) != nil, fmt.Sprintf("Map T object=%T", tObj))
		uObj := ms.Lookup("U")
		ck(uObj != nil && uObj != tObj, "Map U object missing/distinct")
		ck(ms.Lookup("xs") != nil && ms.Lookup("f") != nil && ms.Lookup("out") != nil, "Map locals missing")
		ck(ms.Lookup("x") == nil, "range x must not be in function scope")
		scope, obj := ms.LookupParent("T", mapFD.Body.Pos()+1)
		ck(scope == ms && obj == tObj, "LookupParent(T) from generic func")
	}

	// Sum 的约束接口 Number 有类型集；方法集为空。
	sumFn := pkg.Scope().Lookup("Sum").(*types.Func)
	ss := sumFn.Scope()
	ck(ss != nil && fmt.Sprint(ss.Names()) == "[T s xs]", fmt.Sprintf("Sum scope=%v", ss.Names()))
	if ss != nil {
		sumTP, ok := ss.Lookup("T").(*types.TypeName)
		ck(ok, "Sum T object")
		if ok {
			num, ok := sumTP.Type().(*types.TypeParam)
			ck(ok && num.Constraint().String() == "demo.Number", fmt.Sprintf("Sum T constraint=%v", sumTP.Type()))
		}
	}

	// Instances：Box[int] 与 Map[int,int] 的 TypeArgs/Type。
	var boxUse, mapUse *ast.Ident
	var getSel *ast.SelectorExpr
	ast.Inspect(file, func(n ast.Node) bool {
		switch x := n.(type) {
		case *ast.Ident:
			if _, ok := info.Instances[x]; ok {
				switch x.Name {
				case "Box":
					boxUse = x
				case "Map":
					mapUse = x
				}
			}
		case *ast.SelectorExpr:
			if x.Sel.Name == "Get" {
				getSel = x
			}
		}
		return true
	})
	ck(boxUse != nil && mapUse != nil, "generic instantiation idents missing")
	if boxUse != nil {
		inst := info.Instances[boxUse]
		ck(inst.TypeArgs.Len() == 1, fmt.Sprintf("Box TypeArgs=%v", inst.TypeArgs))
		if inst.TypeArgs.Len() == 1 {
			ck(inst.TypeArgs.At(0) == types.Typ[types.Int], "Box type arg must be int")
		}
		ck(inst.Type.String() == "demo.Box[int]", fmt.Sprintf("Box instance type=%s", inst.Type))
	}
	if mapUse != nil {
		inst := info.Instances[mapUse]
		ck(inst.TypeArgs.Len() == 2, fmt.Sprintf("Map TypeArgs=%v", inst.TypeArgs))
		ck(inst.Type.String() == "func(xs []int, f func(int) int) []int", fmt.Sprintf("Map instance type=%s", inst.Type))
	}

	// 实例化方法集：Box[int] 的 *Box[int] 方法集含 Get，接收者已替换。
	instType, ierr := types.Instantiate(nil, boxNamed, []types.Type{types.Typ[types.Int]}, false)
	ck(ierr == nil && instType != nil, fmt.Sprintf("Instantiate Box[int]: %v", ierr))
	if ierr == nil {
		mset := types.NewMethodSet(types.NewPointer(instType))
		ck(mset.Len() == 1, fmt.Sprintf("Box[int] method set=%d", mset.Len()))
		if mset.Len() == 1 {
			ck(mset.At(0).Obj().Name() == "Get", "instantiated method name")
			ck(mset.At(0).Type().String() == "func() int", fmt.Sprintf("instantiated method type=%s", mset.At(0).Type()))
		}
		live = append(live, instType)
	}
	_, badErr := types.Instantiate(nil, boxNamed, []types.Type{types.Typ[types.Int], types.Typ[types.String]}, true)
	ck(badErr != nil, "Instantiate with wrong arity must fail")
	ck(getSel != nil && info.Selections[getSel] != nil, "b.Get selection missing")
	if getSel != nil && info.Selections[getSel] != nil {
		sel := info.Selections[getSel]
		ck(sel.Kind() == types.MethodVal, fmt.Sprintf("Get selection kind=%v", sel.Kind()))
		ck(sel.Obj().Name() == "Get", "Get selection object")
		ck(sel.Recv().String() == "demo.Box[int]", fmt.Sprintf("Get selection recv=%s", sel.Recv()))
	}

	// type switch 隐式变量位于 case 作用域。
	var implicit types.Object
	var caseCl *ast.CaseClause
	for node, obj := range info.Implicits {
		if cc, ok := node.(*ast.CaseClause); ok {
			implicit = obj
			caseCl = cc
		}
	}
	ck(implicit != nil && caseCl != nil, "type switch implicit missing")
	if implicit != nil {
		ck(implicit.Name() == "v", fmt.Sprintf("implicit name=%s", implicit.Name()))
		ck(implicit.Type().String() == "[]int", fmt.Sprintf("implicit type=%s", implicit.Type()))
		cs := info.Scopes[caseCl]
		ck(cs != nil && cs.Lookup("v") == implicit, "case scope v lookup")
		var vObj types.Object
		ast.Inspect(file, func(n ast.Node) bool {
			if id, ok := n.(*ast.Ident); ok && id.Name == "v" {
				if u := info.Uses[id]; u != nil {
					vObj = u
				}
			}
			return true
		})
		ck(vObj == implicit, "case v use must resolve implicit")
	}

	// GC 后作用域/对象/实例化结果仍然有效，按种子打乱复核顺序。
	snapshot := []string{"Box", "Map", "Number", "Sum", "Use"}
	for round := 0; round < 3; round++ {
		gcStress()
		for _, idx := range rng.Perm(len(snapshot)) {
			ck(pkg.Scope().Lookup(snapshot[idx]) != nil, fmt.Sprintf("round %d lost %s", round, snapshot[idx]))
		}
		ck(pkg.Scope().Lookup("Box") == boxTN, fmt.Sprintf("round %d Box object changed", round))
		ck(boxNamed.TypeParams().At(0).Obj() == tp.Obj(), fmt.Sprintf("round %d type param changed", round))
		ck(boxNamed.Method(0) == method, fmt.Sprintf("round %d method object changed", round))
		ck(mapFn.Scope().Lookup("T") == ms.Lookup("T"), fmt.Sprintf("round %d Map T changed", round))
		ck(sumFn.Scope().Lookup("T") != nil, fmt.Sprintf("round %d Sum T lost", round))
		it, err := types.Instantiate(nil, boxNamed, []types.Type{types.Typ[types.Int]}, false)
		ck(err == nil && types.NewMethodSet(types.NewPointer(it)).Len() == 1,
			fmt.Sprintf("round %d re-instantiate failed", round))
	}

	finish("l16_types_scope_generic")
}
