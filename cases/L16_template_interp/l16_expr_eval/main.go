// L16: go/parser + go/types 驱动的极简常量表达式求值器；与 types.Config 的常量折叠交叉校验。
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
	"sync/atomic"
)

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
		b := make([]byte, 4096)
		b[0] = byte(i)
		gcSink = append(gcSink[:0], b)
	}
	for i := 0; i < 32; i++ {
		_ = make([]constant.Value, 128)
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

func evalInt(e ast.Expr) (constant.Value, error) {
	v, err := eval(e)
	if err != nil {
		return nil, err
	}
	if v.Kind() != constant.Int {
		return nil, fmt.Errorf("non-integer operand (%s)", v.Kind())
	}
	return v, nil
}

func evalBool(e ast.Expr) (constant.Value, error) {
	v, err := eval(e)
	if err != nil {
		return nil, err
	}
	if v.Kind() != constant.Bool {
		return nil, fmt.Errorf("non-boolean operand (%s)", v.Kind())
	}
	return v, nil
}

// eval 是只覆盖整型/布尔常量的最小求值器，不依赖 go/eval 之类外部包。
func eval(e ast.Expr) (constant.Value, error) {
	switch x := e.(type) {
	case *ast.ParenExpr:
		return eval(x.X)
	case *ast.BasicLit:
		switch x.Kind {
		case token.INT, token.CHAR:
			v := constant.MakeFromLiteral(x.Value, x.Kind, 0)
			if v.Kind() == constant.Unknown {
				return nil, fmt.Errorf("bad integer literal %q", x.Value)
			}
			return v, nil
		default:
			return nil, fmt.Errorf("unsupported literal %q (kind %s)", x.Value, x.Kind)
		}
	case *ast.Ident:
		switch x.Name {
		case "true":
			return constant.MakeBool(true), nil
		case "false":
			return constant.MakeBool(false), nil
		}
		return nil, fmt.Errorf("unknown identifier %q", x.Name)
	case *ast.UnaryExpr:
		v, err := eval(x.X)
		if err != nil {
			return nil, err
		}
		switch x.Op {
		case token.ADD:
			if v.Kind() != constant.Int {
				return nil, fmt.Errorf("non-integer operand (%s)", v.Kind())
			}
			return v, nil
		case token.SUB, token.XOR:
			if v.Kind() != constant.Int {
				return nil, fmt.Errorf("non-integer operand (%s)", v.Kind())
			}
			return constant.UnaryOp(x.Op, v, 0), nil
		case token.NOT:
			if v.Kind() != constant.Bool {
				return nil, fmt.Errorf("non-boolean operand (%s)", v.Kind())
			}
			return constant.UnaryOp(token.NOT, v, 0), nil
		}
		return nil, fmt.Errorf("unsupported unary operator %s", x.Op)
	case *ast.BinaryExpr:
		switch x.Op {
		case token.LAND, token.LOR:
			lv, err := evalBool(x.X)
			if err != nil {
				return nil, err
			}
			if x.Op == token.LAND && !constant.BoolVal(lv) {
				return lv, nil
			}
			if x.Op == token.LOR && constant.BoolVal(lv) {
				return lv, nil
			}
			return evalBool(x.Y)
		case token.EQL, token.NEQ, token.LSS, token.LEQ, token.GTR, token.GEQ:
			lv, err := eval(x.X)
			if err != nil {
				return nil, err
			}
			rv, err := eval(x.Y)
			if err != nil {
				return nil, err
			}
			if lv.Kind() != rv.Kind() {
				return nil, fmt.Errorf("mismatched comparison kinds %s and %s", lv.Kind(), rv.Kind())
			}
			switch lv.Kind() {
			case constant.Int:
			case constant.Bool:
				if x.Op != token.EQL && x.Op != token.NEQ {
					return nil, fmt.Errorf("ordered comparison of booleans")
				}
			default:
				return nil, fmt.Errorf("unsupported comparison kind %s", lv.Kind())
			}
			return constant.MakeBool(constant.Compare(lv, x.Op, rv)), nil
		case token.SHL, token.SHR:
			lv, err := evalInt(x.X)
			if err != nil {
				return nil, err
			}
			rv, err := evalInt(x.Y)
			if err != nil {
				return nil, err
			}
			if constant.Sign(rv) < 0 {
				return nil, fmt.Errorf("negative shift count")
			}
			n, ok := constant.Uint64Val(constant.ToInt(rv))
			if !ok || n > 4096 {
				return nil, fmt.Errorf("shift count too large")
			}
			return constant.Shift(lv, x.Op, uint(n)), nil
		default:
			lv, err := evalInt(x.X)
			if err != nil {
				return nil, err
			}
			rv, err := evalInt(x.Y)
			if err != nil {
				return nil, err
			}
			op := x.Op
			if op == token.QUO || op == token.REM {
				if constant.Sign(rv) == 0 {
					return nil, fmt.Errorf("division by zero")
				}
			}
			if op == token.QUO {
				op = token.QUO_ASSIGN // 整型常量除法截断
			}
			v := constant.BinaryOp(lv, op, rv)
			if v.Kind() == constant.Unknown {
				return nil, fmt.Errorf("invalid operation %s", x.Op)
			}
			return v, nil
		}
	case *ast.CallExpr:
		return nil, fmt.Errorf("function calls are not supported")
	}
	return nil, fmt.Errorf("unsupported expression %T", e)
}

// checkConst 把表达式嵌入 const 声明，用 go/types 独立求值。
func checkConst(src string) (constant.Value, *types.Package, error) {
	fset := token.NewFileSet()
	fileSrc := "package p\nconst C = " + src + "\n"
	f, err := parser.ParseFile(fset, "expr.go", fileSrc, parser.AllErrors)
	if err != nil {
		return nil, nil, err
	}
	var firstErr error
	info := &types.Info{Defs: make(map[*ast.Ident]types.Object)}
	conf := types.Config{Error: func(err error) {
		if firstErr == nil {
			firstErr = err
		}
	}}
	pkg, err := conf.Check("p", fset, []*ast.File{f}, info)
	if err != nil {
		return nil, pkg, err
	}
	gd, ok := f.Decls[0].(*ast.GenDecl)
	if !ok || len(gd.Specs) != 1 {
		return nil, pkg, fmt.Errorf("unexpected decl shape")
	}
	vs, ok := gd.Specs[0].(*ast.ValueSpec)
	if !ok || len(vs.Names) != 1 {
		return nil, pkg, fmt.Errorf("unexpected const spec shape")
	}
	c, ok := info.Defs[vs.Names[0]].(*types.Const)
	if !ok {
		return nil, pkg, fmt.Errorf("C is not a constant")
	}
	if c.Val().Kind() == constant.Unknown {
		return nil, pkg, fmt.Errorf("constant value is unknown")
	}
	return c.Val(), pkg, nil
}

type goodCase struct {
	src  string
	want string
}

type survivor struct {
	src  string
	expr ast.Expr
	val  constant.Value
	pkg  *types.Package
	want string
}

func main() {
	uafSeed = envSeed()
	good := []goodCase{
		{"1 + 2*3", "7"},
		{"(1 << 10) - 24", "1000"},
		{"0x2a ^ 0x0f", "37"},
		{"7 / 2", "3"},
		{"-7 / 2", "-3"},
		{"7 % 3", "1"},
		{"1 << 63", "9223372036854775808"},
		{"1<<64 + 1", "18446744073709551617"},
		{"^1", "-2"},
		{"'a' + 1", "98"},
		{"0b1010 | 0b0101", "15"},
		{"1_000_000 + 24", "1000024"},
		{"3 < 4 && 2 == 2", "true"},
		{"10 % 3 == 1", "true"},
		{"-(-42)", "42"},
		{"7 & 3", "3"},
		{"7 | 8", "15"},
		{"7 ^ 3", "4"},
		{"7 &^ 3", "4"},
		{"1<<5 - 17", "15"},
		{"true && false", "false"},
		{"false || true", "true"},
		{"false && (1 == 2)", "false"},
		{"true || (1 == 2)", "true"},
	}
	bad := []struct {
		src  string
		want string // 期望求值器错误片段；空表示语法错误
	}{
		{"1 / 0", "division by zero"},
		{"7 % 0", "division by zero"},
		{"1 + true", "non-integer"},
		{"true && 1", "non-boolean"},
		{"1 << -1", "negative shift"},
		{"unknown", "unknown identifier"},
		{"1 +", ""},
		{"(1", ""},
		{"'ab'", ""},
	}

	rounds := 3 + int(uafSeed%3)
	var survivors []survivor
	for r := 0; r < rounds; r++ {
		survivors = survivors[:0]
		rng := rand.New(rand.NewSource(uafSeed*1000 + int64(r)*17))
		for _, idx := range rng.Perm(len(good)) {
			gc := good[idx]
			expr, perr := parser.ParseExpr(gc.src)
			ck(perr == nil, fmt.Sprintf("round %d parse %q: %v", r, gc.src, perr))
			if perr != nil {
				continue
			}
			v, err := eval(expr)
			ck(err == nil, fmt.Sprintf("round %d eval %q: %v", r, gc.src, err))
			if err == nil {
				ck(v.ExactString() == gc.want, fmt.Sprintf("round %d eval %q=%s want %s", r, gc.src, v.ExactString(), gc.want))
			}
			tv, pkg, terr := checkConst(gc.src)
			ck(terr == nil, fmt.Sprintf("round %d types check %q: %v", r, gc.src, terr))
			if terr == nil {
				ck(tv.ExactString() == gc.want, fmt.Sprintf("round %d types %q=%s want %s", r, gc.src, tv.ExactString(), gc.want))
				if err == nil {
					ck(v.ExactString() == tv.ExactString(), fmt.Sprintf("round %d evaluator/types disagree on %q: %s vs %s",
						r, gc.src, v.ExactString(), tv.ExactString()))
				}
			}
			survivors = append(survivors, survivor{src: gc.src, expr: expr, val: v, pkg: pkg, want: gc.want})
		}
		gcStress()
	}

	// GC 后重新使用保存的 AST、constant.Value 与 *types.Package。
	for _, s := range survivors {
		if s.expr == nil {
			continue
		}
		v2, err := eval(s.expr)
		ck(err == nil, fmt.Sprintf("after GC eval %q: %v", s.src, err))
		if err == nil {
			ck(v2.ExactString() == s.want, fmt.Sprintf("after GC eval %q=%s want %s", s.src, v2.ExactString(), s.want))
		}
		if s.val != nil {
			ck(s.val.ExactString() == s.want, fmt.Sprintf("after GC stored value %q=%s want %s", s.src, s.val.ExactString(), s.want))
		}
		if s.pkg != nil {
			obj := s.pkg.Scope().Lookup("C")
			c, ok := obj.(*types.Const)
			ck(ok, fmt.Sprintf("after GC %q: const object lost", s.src))
			if ok {
				ck(c.Val().ExactString() == s.want, fmt.Sprintf("after GC const %q=%s want %s", s.src, c.Val().ExactString(), s.want))
			}
		}
	}

	badPerm := rand.New(rand.NewSource(uafSeed*31 + 7)).Perm(len(bad))
	for _, idx := range badPerm {
		bc := bad[idx]
		expr, perr := parser.ParseExpr(bc.src)
		if bc.want == "" {
			ck(perr != nil, fmt.Sprintf("bad %q: expected parse error, got none", bc.src))
			_, _, terr := checkConst(bc.src)
			ck(terr != nil, fmt.Sprintf("bad %q: types accepted invalid syntax", bc.src))
			continue
		}
		ck(perr == nil, fmt.Sprintf("bad %q: unexpected parse error %v", bc.src, perr))
		if perr == nil {
			_, err := eval(expr)
			ck(err != nil, fmt.Sprintf("bad %q: evaluator accepted invalid expression", bc.src))
			if err != nil {
				ck(containsStr(err.Error(), bc.want), fmt.Sprintf("bad %q: error %q missing %q", bc.src, err.Error(), bc.want))
			}
		}
		_, _, terr := checkConst(bc.src)
		ck(terr != nil, fmt.Sprintf("bad %q: go/types accepted invalid expression", bc.src))
	}

	finish("l16_expr_eval")
}

func containsStr(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
