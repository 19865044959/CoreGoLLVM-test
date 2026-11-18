// L16: go/parser 错误恢复边界与 go/types 作用域联动：坏语句后仍保留前后声明，
// 过滤 Bad 节点后打印/重解析/类型检查，Uses 解析到包级对象；跨 GC 复核。
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
	"go/types"
	"math/rand"
	"os"
	"runtime"
	"strconv"
	"sync/atomic"
)

const badSrc = `package demo

const Base = 10
var Total int

func Sum(a, b int) int { return a + b }

func UseBase() int { return Base }

func Broken() int {
	x := 1
	x = = 2
	return x
}

func Double(n int) int { return n * 2 }
`

const fixedSrc = `package demo

const Base = 10
var Total int

func Sum(a, b int) int { return a + b }

func UseBase() int { return Base }

func Broken() int {
	x := 1
	x = 2
	return x
}

func Double(n int) int { return n * 2 }
`

var (
	failed  int32
	gcSink  [][]byte
	liveAST []*ast.File
	livePkg []*types.Package
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
		_ = make([]types.Object, 32)
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

// completeDecl 判定声明中是否混入解析器为恢复插入的 Bad 节点。
func completeDecl(d ast.Decl) bool {
	ok := true
	ast.Inspect(d, func(n ast.Node) bool {
		switch n.(type) {
		case *ast.BadExpr, *ast.BadStmt, *ast.BadDecl:
			ok = false
		}
		return true
	})
	return ok
}

func funcDecl(f *ast.File, name string) *ast.FuncDecl {
	for _, d := range f.Decls {
		if fd, ok := d.(*ast.FuncDecl); ok && fd.Recv == nil && fd.Name.Name == name {
			return fd
		}
	}
	return nil
}

func identAt(file *ast.File, fset *token.FileSet, name string, line int) *ast.Ident {
	var out *ast.Ident
	ast.Inspect(file, func(n ast.Node) bool {
		if id, ok := n.(*ast.Ident); ok && out == nil && id.Name == name && fset.Position(id.Pos()).Line == line {
			out = id
		}
		return true
	})
	return out
}

func typeCheck(fset *token.FileSet, file *ast.File) (*types.Package, *types.Info, error) {
	info := &types.Info{
		Types:  map[ast.Expr]types.TypeAndValue{},
		Defs:   map[*ast.Ident]types.Object{},
		Uses:   map[*ast.Ident]types.Object{},
		Scopes: map[ast.Node]*types.Scope{},
	}
	pkg, err := (&types.Config{}).Check("demo", fset, []*ast.File{file}, info)
	return pkg, info, err
}

func main() {
	uafSeed = envSeed()
	rng := rand.New(rand.NewSource(uafSeed))

	// 1) 坏源码：错误恢复仍产出前后完整声明，保留 Broken 的残缺体。
	fset := token.NewFileSet()
	file, perr := parser.ParseFile(fset, "bad.go", badSrc, parser.AllErrors|parser.ParseComments)
	ck(perr != nil, "bad source must fail to parse")
	ck(file != nil, "bad source must yield partial AST")
	if file == nil {
		finish("l16_parser_recover_scope")
		return
	}
	liveAST = append(liveAST, file)
	ck(file.Name != nil && file.Name.Name == "demo", "partial AST package name")
	for _, name := range []string{"Sum", "UseBase", "Broken", "Double"} {
		ck(funcDecl(file, name) != nil, "partial AST lost "+name)
	}
	broken := funcDecl(file, "Broken")
	ck(broken != nil && broken.Body != nil, "Broken body missing in partial AST")
	if broken != nil {
		pos := fset.Position(broken.Pos())
		ck(pos.Filename == "bad.go" && pos.Line == 10, fmt.Sprintf("Broken position=%s", pos))
	}

	el, ok := errList(perr)
	ck(ok, fmt.Sprintf("bad source error type=%T", perr))
	if ok {
		ck(len(el) >= 2, fmt.Sprintf("error count=%d", len(el)))
		sorted := true
		for i := 1; i < len(el); i++ {
			if el[i-1].Pos.Offset > el[i].Pos.Offset {
				sorted = false
			}
			ck(el[i].Pos.Line > 0, fmt.Sprintf("error[%d] line=%d", i, el[i].Pos.Line))
		}
		ck(sorted, "ErrorList must be sorted by offset")
		ck(el[0].Pos.Offset < el[len(el)-1].Pos.Offset, "ErrorList offsets collapsed")
	}

	// 2) 过滤恢复节点后打印为合法源码，重新解析并做完整类型检查。
	var decls []ast.Decl
	badDecls := 0
	for _, d := range file.Decls {
		if completeDecl(d) {
			decls = append(decls, d)
		} else {
			badDecls++
		}
	}
	ck(badDecls == 1, fmt.Sprintf("incomplete decls=%d", badDecls))
	ck(len(decls) == 5, fmt.Sprintf("complete decls=%d", len(decls)))
	clean := &ast.File{Name: file.Name, Decls: decls}
	var buf bytes.Buffer
	ck(printer.Fprint(&buf, fset, clean) == nil, "print recovered decls")
	ck(buf.Len() > 100 && !bytes.Contains(buf.Bytes(), []byte("= =")), "printed recovery still invalid")

	cfset := token.NewFileSet()
	cleanFile, cerr := parser.ParseFile(cfset, "clean.go", buf.String(), parser.ParseComments)
	ck(cerr == nil, fmt.Sprintf("reparse cleaned source: %v", cerr))
	if cleanFile == nil {
		finish("l16_parser_recover_scope")
		return
	}
	liveAST = append(liveAST, cleanFile)
	pkg, info, terr := typeCheck(cfset, cleanFile)
	ck(terr == nil, fmt.Sprintf("type check recovered source: %v", terr))
	ck(pkg != nil, "type check returned nil package")
	if pkg == nil {
		finish("l16_parser_recover_scope")
		return
	}
	livePkg = append(livePkg, pkg)

	// 3) 作用域与 Uses：恢复后的声明可正常解析到包级对象。
	names := pkg.Scope().Names()
	want := []string{"Base", "Double", "Sum", "Total", "UseBase"}
	ck(fmt.Sprint(names) == fmt.Sprint(want), fmt.Sprintf("package scope=%v", names))
	baseObj := pkg.Scope().Lookup("Base")
	bc, isConst := baseObj.(*types.Const)
	ck(isConst, fmt.Sprintf("Base object=%T", baseObj))
	if isConst {
		ck(bc.Val().ExactString() == "10", fmt.Sprintf("Base value=%v", bc.Val()))
		ck(bc.Parent() == pkg.Scope(), "Base parent scope")
	}
	useFD := funcDecl(cleanFile, "UseBase")
	ck(useFD != nil, "UseBase decl missing after reparse")
	if useFD != nil {
		fn := pkg.Scope().Lookup("UseBase").(*types.Func)
		fs := fn.Scope()
		ck(fs != nil && fs.Parent() != nil && fs.Parent().Parent() == pkg.Scope(), "UseBase scope chain")
		useIdent := identAt(cleanFile, cfset, "Base", 9)
		ck(useIdent != nil, "Base use ident missing")
		if useIdent != nil {
			ck(info.Uses[useIdent] == baseObj, fmt.Sprintf("Base use resolves %v", info.Uses[useIdent]))
		}
		if fs != nil {
			scope, obj := fs.LookupParent("Base", useFD.Pos()+1)
			ck(obj == baseObj && scope == pkg.Scope(), "LookupParent(Base) from UseBase scope")
		}
	}
	sumFD := funcDecl(cleanFile, "Sum")
	if sumFD != nil {
		sumFn := pkg.Scope().Lookup("Sum").(*types.Func)
		sig := sumFn.Type().(*types.Signature)
		ck(sig.Params().Len() == 2 && sig.Results().Len() == 1, "Sum signature arity")
		ck(sumFn.Scope().Lookup("a") != nil && sumFn.Scope().Lookup("b") != nil, "Sum param scope")
	}

	// 4) 修复后的完整源码：所有函数通过检查，Broken 的局部 x 正常定义。
	ffset := token.NewFileSet()
	fixed, ferr := parser.ParseFile(ffset, "fixed.go", fixedSrc, parser.ParseComments)
	ck(ferr == nil, fmt.Sprintf("fixed parse: %v", ferr))
	if fixed != nil {
		liveAST = append(liveAST, fixed)
		fpkg, finfo, fterr := typeCheck(ffset, fixed)
		ck(fterr == nil, fmt.Sprintf("fixed type check: %v", fterr))
		ck(fpkg != nil && fpkg.Scope().Lookup("Double") != nil, "fixed package scope")
		if fpkg != nil {
			livePkg = append(livePkg, fpkg)
		}
		xDef := identAt(fixed, ffset, "x", 11)
		ck(xDef != nil && finfo.Defs[xDef] != nil, "fixed Broken x def")
	}

	// 5) GC 后按种子顺序复核对象身份、错误列表与作用域。
	checkNames := []string{"Base", "Double", "Sum", "Total", "UseBase"}
	for round := 0; round < 3; round++ {
		gcStress()
		for _, idx := range rng.Perm(len(checkNames)) {
			n := checkNames[idx]
			ck(pkg.Scope().Lookup(n) != nil, fmt.Sprintf("round %d lost %s", round, n))
		}
		ck(pkg.Scope().Lookup("Base") == baseObj, fmt.Sprintf("round %d Base identity changed", round))
		el2, ok2 := errList(perr)
		ck(ok2 && len(el2) == len(el), fmt.Sprintf("round %d ErrorList size changed", round))
		if ok2 && len(el2) == len(el) {
			ck(el2[0].Msg == el[0].Msg && el2[0].Pos.Offset == el[0].Pos.Offset,
				fmt.Sprintf("round %d first error changed", round))
		}
		// 保留的 AST 仍可遍历与打印。
		var b bytes.Buffer
		ck(printer.Fprint(&b, cfset, cleanFile) == nil, fmt.Sprintf("round %d reprint failed", round))
		ck(b.Len() > 100, fmt.Sprintf("round %d reprinted length=%d", round, b.Len()))
	}

	finish("l16_parser_recover_scope")
}
