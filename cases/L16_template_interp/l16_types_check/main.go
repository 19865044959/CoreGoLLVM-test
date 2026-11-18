// L16: go/types 类型检查合法/非法源码；错误位置可判读，类型对象跨 GC 保持有效。
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

const legalSrc = `package p

type Point struct {
	X, Y int
}

func (p Point) Sum() int { return p.X + p.Y }

type Sumer interface {
	Sum() int
}

var _ Sumer = Point{}

const Base = 3 * 4

var Origin = Point{}

func Twice(n int) int {
	if n < 0 {
		return -n
	}
	return n * 2
}

type MyErr struct{ Code int }

func (e *MyErr) Error() string { return "myerr" }

func SafeDiv(a, b int) (int, error) {
	if b == 0 {
		return 0, &MyErr{Code: 1}
	}
	return a / b, nil
}
`

type checkResult struct {
	pkg   *types.Package
	info  *types.Info
	fset  *token.FileSet
	file  *ast.File
	errs  []string
	lines []int
}

var (
	failed  int32
	gcSink  [][]byte
	liveRes []*checkResult
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
		_ = make([]*types.Var, 128)
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

func checkSource(name, src string) *checkResult {
	res := &checkResult{}
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, name, src, parser.AllErrors)
	if err != nil {
		res.errs = append(res.errs, err.Error())
		return res
	}
	res.fset = fset
	res.file = f
	res.info = &types.Info{
		Defs:  make(map[*ast.Ident]types.Object),
		Uses:  make(map[*ast.Ident]types.Object),
		Types: make(map[ast.Expr]types.TypeAndValue),
	}
	conf := types.Config{
		Error: func(err error) {
			res.errs = append(res.errs, err.Error())
			if te, ok := err.(types.Error); ok {
				res.lines = append(res.lines, te.Fset.Position(te.Pos).Line)
			}
		},
	}
	res.pkg, _ = conf.Check("p", fset, []*ast.File{f}, res.info)
	return res
}

func errLines(res *checkResult) []int {
	return res.lines
}

func main() {
	uafSeed = envSeed()

	legal := checkSource("legal.go", legalSrc)
	liveRes = append(liveRes, legal)
	ck(legal.pkg != nil, "legal source: nil package")
	ck(len(legal.errs) == 0, fmt.Sprintf("legal source errors: %v", legal.errs))
	if legal.pkg == nil {
		finish("l16_types_check")
		return
	}

	baseObj, ok := legal.pkg.Scope().Lookup("Base").(*types.Const)
	ck(ok, "const Base not found")
	if ok {
		ck(baseObj.Val().ExactString() == "12", fmt.Sprintf("Base value=%s want 12", baseObj.Val().ExactString()))
		p := legal.fset.Position(baseObj.Pos())
		ck(p.Line == 15, fmt.Sprintf("Base line=%d want 15", p.Line))
	}
	ck(legal.pkg.Scope().Lookup("Origin") != nil, "var Origin not found")
	ck(legal.pkg.Scope().Lookup("MyErr") != nil, "type MyErr not found")

	twice := legal.pkg.Scope().Lookup("Twice")
	ck(twice != nil && twice.Type().String() == "func(n int) int",
		fmt.Sprintf("Twice type=%v", twice))
	safeDiv := legal.pkg.Scope().Lookup("SafeDiv")
	ck(safeDiv != nil && safeDiv.Type().String() == "func(a int, b int) (int, error)",
		fmt.Sprintf("SafeDiv type=%v", safeDiv))

	pointObj := legal.pkg.Scope().Lookup("Point")
	ck(pointObj != nil, "type Point not found")
	var pointNamed *types.Named
	namedOK := false
	if pointObj != nil {
		pointNamed, namedOK = pointObj.Type().(*types.Named)
	}
	ck(namedOK, "Point is not a named type")
	if namedOK {
		ck(types.NewMethodSet(pointNamed).Lookup(nil, "Sum") != nil, "Point method set missing Sum")
		ck(types.NewMethodSet(types.NewPointer(pointNamed)).Lookup(nil, "Sum") != nil,
			"*Point method set missing Sum")
		sumerObj := legal.pkg.Scope().Lookup("Sumer")
		if sumerObj != nil {
			sumerIface, ok := sumerObj.Type().Underlying().(*types.Interface)
			ck(ok && types.Implements(pointNamed, sumerIface), "Point does not implement Sumer")
		} else {
			ck(false, "type Sumer not found")
		}
	}

	errObj := types.Universe.Lookup("error")
	errIface, ok := errObj.Type().Underlying().(*types.Interface)
	ck(ok, "universe error is not an interface")
	myErrObj := legal.pkg.Scope().Lookup("MyErr")
	ck(myErrObj != nil, "type MyErr not found")
	var myErrNamed *types.Named
	ok = false
	if myErrObj != nil {
		myErrNamed, ok = myErrObj.Type().(*types.Named)
	}
	ck(ok, "MyErr is not a named type")
	if ok && errIface != nil {
		ck(types.Implements(types.NewPointer(myErrNamed), errIface), "*MyErr does not implement error")
		ck(!types.Implements(myErrNamed, errIface), "MyErr must not implement error with value receiver")
	}

	// 常量表达式 3*4 由 types.Info.Types 折叠出值。
	foundConstExpr := false
	ast.Inspect(legal.file, func(n ast.Node) bool {
		be, ok := n.(*ast.BinaryExpr)
		if !ok || be.Op != token.MUL {
			return true
		}
		if tv, ok := legal.info.Types[be]; ok && tv.Value != nil {
			foundConstExpr = true
			ck(tv.Value.ExactString() == "12", fmt.Sprintf("3*4 folded value=%s", tv.Value.ExactString()))
		}
		return true
	})
	ck(foundConstExpr, "constant expression 3*4 not in info.Types")

	type badCase struct {
		name string
		src  string
		want string
		line int
	}
	bad := []badCase{
		{"undef.go", "package p\nfunc f() { _ = missing }\n", "undefined: missing", 2},
		{"assign.go", "package p\nvar s string = 1\n", "cannot use 1 (untyped int constant) as string value", 2},
		{"ret.go", "package p\nfunc f() int {\n}\n", "missing return", 3},
		{"redecl.go", "package p\nvar x int\nvar x string\n", "x redeclared in this block", 3},
		{"call.go", "package p\nfunc f(n int) {}\nfunc g() { f(\"s\") }\n", "cannot use \"s\" (untyped string constant) as int value", 3},
		{"op.go", "package p\nvar x = 1 + true\n", "invalid operation: 1 + true", 2},
		{"undeftype.go", "package p\nvar v Missing\n", "undefined: Missing", 2},
		{"retcount.go", "package p\nfunc f() (int, int) { return 1 }\n", "not enough return values", 2},
	}
	perm := rand.New(rand.NewSource(uafSeed)).Perm(len(bad))
	for _, idx := range perm {
		bc := bad[idx]
		res := checkSource(bc.name, bc.src)
		liveRes = append(liveRes, res)
		ck(len(res.errs) > 0, fmt.Sprintf("%s: expected type error, got none", bc.name))
		matched := false
		for _, e := range res.errs {
			ck(strings.HasPrefix(e, bc.name+":"), fmt.Sprintf("%s: error missing file prefix: %q", bc.name, e))
			if strings.Contains(e, bc.want) {
				matched = true
			}
		}
		ck(matched, fmt.Sprintf("%s: no error contains %q; errors=%v", bc.name, bc.want, res.errs))
		lines := errLines(res)
		foundLine := false
		for _, ln := range lines {
			if ln == bc.line {
				foundLine = true
			}
		}
		ck(foundLine, fmt.Sprintf("%s: error lines=%v want line %d (errors=%v)", bc.name, lines, bc.line, res.errs))
	}

	gcStress()

	// GC 后重新查询类型对象/方法集/常量值。
	if legal.pkg != nil {
		base2, ok := legal.pkg.Scope().Lookup("Base").(*types.Const)
		ck(ok && base2.Val().ExactString() == "12", "Base const broken after GC")
		point2, ok := legal.pkg.Scope().Lookup("Point").Type().(*types.Named)
		ck(ok && types.NewMethodSet(types.NewPointer(point2)).Lookup(nil, "Sum") != nil,
			"Point method set broken after GC")
		myErr2, ok := legal.pkg.Scope().Lookup("MyErr").Type().(*types.Named)
		ck(ok && errIface != nil && types.Implements(types.NewPointer(myErr2), errIface),
			"error implementation broken after GC")
		ck(legal.fset.Position(base2.Pos()).Line == 15, "Base position broken after GC")
		ck(len(legal.info.Types) > 0, "types.Info wiped after GC")
	}
	for _, res := range liveRes {
		if res.file != nil {
			ck(res.file.Name != nil && res.file.Name.Name == "p", "ast.File broken after GC")
		}
	}

	finish("l16_types_check")
}
