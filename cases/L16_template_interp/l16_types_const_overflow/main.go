// L16: go/types 常量运算与可表示性诊断：任意精度、移位、转换溢出、除零/负移位，跨 GC。
package main

import (
	"fmt"
	"go/ast"
	"go/constant"
	"go/parser"
	"go/token"
	"go/types"
	"math/big"
	"os"
	"runtime"
	"strconv"
	"strings"
	"sync/atomic"
)

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

type checked struct {
	pkg   *types.Package
	info  *types.Info
	file  *ast.File
	fset  *token.FileSet
	errs  []string
	lines []int
}

func checkSource(name, srcText string) *checked {
	c := &checked{}
	c.fset = token.NewFileSet()
	f, err := parser.ParseFile(c.fset, name, srcText, parser.AllErrors)
	if err != nil {
		c.errs = append(c.errs, err.Error())
		return c
	}
	c.file = f
	c.info = &types.Info{
		Defs:  make(map[*ast.Ident]types.Object),
		Types: make(map[ast.Expr]types.TypeAndValue),
	}
	conf := types.Config{Error: func(e error) {
		c.errs = append(c.errs, e.Error())
		if te, ok := e.(types.Error); ok {
			c.lines = append(c.lines, te.Fset.Position(te.Pos).Line)
		}
	}}
	c.pkg, _ = conf.Check("p", c.fset, []*ast.File{f}, c.info)
	return c
}

func appendBig(sb *strings.Builder, x *big.Int) {
	sb.WriteString(x.String())
}

func rhsOf(f *ast.File, name string) ast.Expr {
	for _, d := range f.Decls {
		gd, ok := d.(*ast.GenDecl)
		if !ok || gd.Tok != token.VAR {
			continue
		}
		for _, sp := range gd.Specs {
			vs := sp.(*ast.ValueSpec)
			for i, n := range vs.Names {
				if n.Name == name && i < len(vs.Values) {
					return vs.Values[i]
				}
			}
		}
	}
	return nil
}

func constOf(c *checked, name string) *types.Const {
	obj := c.pkg.Scope().Lookup(name)
	if obj == nil {
		return nil
	}
	con, _ := obj.(*types.Const)
	return con
}

func legalSource(k int) string {
	return fmt.Sprintf(`package p

const Base = 1 << 100
const Div = Base / (1 << 90)
const Third = 1.0 / 3.0
const Str = "a" + "b"
const Bool = 1 < 2
const Sh = 1 << 63
const Neg = -7 / 2
const Rem = -7 %% 2
const MaxU = ^uint64(0)
const Conv = uint8(255)
const Pow = 1 << %d
const PowMinus = Pow - 1

var A = Div + 24
var B = Pow / 2
`, k)
}

func main() {
	uafSeed = envSeed()
	rounds := 3 + int(uafSeed%3)

	bad := []struct {
		name string
		src  string
		line int
		want string
	}{
		{"u8.go", "package p\nvar x uint8 = 300\n", 2,
			"cannot use 300 (untyped int constant) as uint8 value in variable declaration (overflows)"},
		{"c8.go", "package p\nconst y int8 = 1000\n", 2,
			"in constant declaration (overflows)"},
		{"conv.go", "package p\nvar z = uint8(300)\n", 2,
			"constant 300 overflows uint8"},
		{"f32.go", "package p\nvar f float32 = 1e100\n", 2,
			"as float32 value in variable declaration (overflows)"},
		{"shift.go", "package p\nvar s int64 = 1 << 200\n", 2,
			"as int64 value in variable declaration (overflows)"},
		{"div0.go", "package p\nconst d = 1 / 0\n", 2,
			"invalid operation: division by zero"},
		{"negshift.go", "package p\nconst n = 1 << -1\n", 2,
			"invalid operation: negative shift count -1 (untyped int constant)"},
		{"badadd.go", "package p\nconst b = 1 + true\n", 2,
			"invalid operation: 1 + true (mismatched types untyped int and untyped bool)"},
		{"multiline.go", "package p\n\nvar m uint16 = 70000\n", 3,
			"(overflows)"},
	}
	badFirst := make(map[string]string, len(bad))
	var legal *checked

	for r := 0; r < rounds; r++ {
		k := 60 + int(uafSeed%3) + r%2
		if k > 63 {
			k = 63
		}
		c := checkSource("legal.go", legalSource(k))
		ck(len(c.errs) == 0, fmt.Sprintf("round %d legal errors: %v", r, c.errs))
		ck(c.pkg != nil, fmt.Sprintf("round %d legal package nil", r))
		if c.pkg == nil {
			continue
		}
		legal = c
		pow := new(big.Int).Lsh(big.NewInt(1), uint(k))
		powMinus := new(big.Int).Sub(new(big.Int).Set(pow), big.NewInt(1))
		powHalf := new(big.Int).Rsh(new(big.Int).Set(pow), 1)
		live = []any{c, c.pkg, c.info, c.file, c.fset}

		consts := []struct {
			name  string
			exact string
			kind  constant.Kind
			typ   string
		}{
			{"Base", "1267650600228229401496703205376", constant.Int, "untyped int"},
			{"Div", "1024", constant.Int, "untyped int"},
			{"Third", "1/3", constant.Float, "untyped float"},
			{"Str", `"ab"`, constant.String, "untyped string"},
			{"Bool", "true", constant.Bool, "untyped bool"},
			{"Sh", "9223372036854775808", constant.Int, "untyped int"},
			{"Neg", "-3", constant.Int, "untyped int"},
			{"Rem", "-1", constant.Int, "untyped int"},
			{"MaxU", "18446744073709551615", constant.Int, "uint64"},
			{"Conv", "255", constant.Int, "uint8"},
			{"Pow", pow.String(), constant.Int, "untyped int"},
			{"PowMinus", powMinus.String(), constant.Int, "untyped int"},
		}
		for _, want := range consts {
			con := constOf(c, want.name)
			ck(con != nil, fmt.Sprintf("round %d const %s missing", r, want.name))
			if con == nil {
				continue
			}
			ck(con.Val().ExactString() == want.exact,
				fmt.Sprintf("round %d %s=%s want %s", r, want.name, con.Val().ExactString(), want.exact))
			ck(con.Val().Kind() == want.kind,
				fmt.Sprintf("round %d %s kind=%v want %v", r, want.name, con.Val().Kind(), want.kind))
			ck(con.Type().String() == want.typ,
				fmt.Sprintf("round %d %s type=%v want %s", r, want.name, con.Type(), want.typ))
			ck(c.fset.Position(con.Pos()).Line > 1, fmt.Sprintf("round %d %s position lost", r, want.name))
		}

		aRHS := rhsOf(c.file, "A")
		ck(aRHS != nil, fmt.Sprintf("round %d A rhs missing", r))
		if aRHS != nil {
			tv, ok := c.info.Types[aRHS]
			ck(ok && tv.Value != nil, fmt.Sprintf("round %d A rhs not constant: %v", r, tv))
			if ok && tv.Value != nil {
				ck(tv.Value.ExactString() == "1048", fmt.Sprintf("round %d A=%s want 1048", r, tv.Value.ExactString()))
				ck(tv.Type.String() == "int", fmt.Sprintf("round %d A type=%v want int", r, tv.Type))
			}
		}
		bRHS := rhsOf(c.file, "B")
		ck(bRHS != nil, fmt.Sprintf("round %d B rhs missing", r))
		if bRHS != nil {
			tv, ok := c.info.Types[bRHS]
			ck(ok && tv.Value != nil, fmt.Sprintf("round %d B rhs not constant: %v", r, tv))
			if ok && tv.Value != nil {
				ck(tv.Value.ExactString() == powHalf.String(),
					fmt.Sprintf("round %d B=%s want %s", r, tv.Value.ExactString(), powHalf.String()))
				ck(tv.Type.String() == "int", fmt.Sprintf("round %d B type=%v want int", r, tv.Type))
			}
		}

		for _, bc := range bad {
			res := checkSource(bc.name, bc.src)
			ck(len(res.errs) > 0, fmt.Sprintf("round %d %s: expected type error, got none", r, bc.name))
			matched := false
			for _, msg := range res.errs {
				ck(strings.HasPrefix(msg, bc.name+":"), fmt.Sprintf("round %d %s: error missing file prefix: %q", r, bc.name, msg))
				if strings.Contains(msg, bc.want) {
					matched = true
				}
			}
			ck(matched, fmt.Sprintf("round %d %s: no error contains %q; errors=%v", r, bc.name, bc.want, res.errs))
			foundLine := false
			for _, ln := range res.lines {
				if ln == bc.line {
					foundLine = true
				}
			}
			ck(foundLine, fmt.Sprintf("round %d %s: lines=%v want %d (%v)", r, bc.name, res.lines, bc.line, res.errs))
			if r == 0 {
				badFirst[bc.name] = strings.Join(res.errs, "\n")
			}
		}

		var sb strings.Builder
		appendBig(&sb, pow)
		ck(sb.String() == pow.String(), fmt.Sprintf("round %d independent big.Int mismatch", r))

		gcStress()

		// GC 后常量对象、Info.Types 与错误文本仍可用。
		for _, want := range consts {
			con := constOf(legal, want.name)
			ck(con != nil && con.Val().ExactString() == want.exact,
				fmt.Sprintf("round %d %s after GC", r, want.name))
		}
		if aRHS2 := rhsOf(legal.file, "A"); aRHS2 != nil {
			if tv, ok := legal.info.Types[aRHS2]; ok && tv.Value != nil {
				ck(tv.Value.ExactString() == "1048", fmt.Sprintf("round %d A after GC=%s", r, tv.Value.ExactString()))
			} else {
				ck(false, fmt.Sprintf("round %d A info lost after GC", r))
			}
		}
		ck(legal.fset.Position(legal.file.Package).Line == 1,
			fmt.Sprintf("round %d file position after GC", r))
		for _, bc := range bad {
			res := checkSource(bc.name, bc.src)
			ck(strings.Join(res.errs, "\n") == badFirst[bc.name],
				fmt.Sprintf("round %d %s errors changed after GC: %v", r, bc.name, res.errs))
		}
	}

	finish("l16_types_const_overflow")
}
