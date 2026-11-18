// L16: go/types 常量与 go/constant 舍入边界：编译期常量值、float32/float64 半程舍入、
// 次正规/溢出/整数可表示性，与同一表达式的运行期转换按位对齐；跨 GC。
package main

import (
	"fmt"
	"go/ast"
	"go/constant"
	"go/parser"
	"go/token"
	"go/types"
	"math"
	"math/rand"
	"os"
	"runtime"
	"strconv"
	"sync/atomic"
)

const src = `package demo

const (
	Third = 1.0 / 3.0
	Tiny  = 1e-46
	Huge  = 1e39
	Tie   = 1.000000059604644775390625
	Sub   = 1.4e-45
	MaxI  = 9223372036854775807
	OverI = 9223372036854775808
)
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
		_ = make([]constant.Value, 64)
		_ = make([]any, 32)
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

func bits32(v constant.Value) (uint32, bool) {
	f, exact := constant.Float32Val(v)
	return math.Float32bits(f), exact
}

func bits64(v constant.Value) (uint64, bool) {
	f, exact := constant.Float64Val(v)
	return math.Float64bits(f), exact
}

func valueOf(tv types.TypeAndValue) constant.Value {
	if tv.Value == nil {
		return constant.MakeUnknown()
	}
	return tv.Value
}

func main() {
	uafSeed = envSeed()
	rng := rand.New(rand.NewSource(uafSeed))

	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "const.go", src, 0)
	ck(err == nil, fmt.Sprintf("parse: %v", err))
	if file == nil {
		finish("l16_const_convert_round")
		return
	}
	info := &types.Info{Types: map[ast.Expr]types.TypeAndValue{}}
	pkg, cerr := (&types.Config{}).Check("demo", fset, []*ast.File{file}, info)
	ck(cerr == nil, fmt.Sprintf("type check: %v", cerr))
	ck(pkg != nil, "nil package")
	if pkg == nil {
		finish("l16_const_convert_round")
		return
	}
	live = append(live, pkg)

	// 1) 从 const 声明的类型信息中取出常量值。
	consts := map[string]constant.Value{}
	typesOf := map[string]types.Type{}
	for _, d := range file.Decls {
		gd, ok := d.(*ast.GenDecl)
		if !ok || gd.Tok != token.CONST {
			continue
		}
		for _, spec := range gd.Specs {
			vs := spec.(*ast.ValueSpec)
			for i, name := range vs.Names {
				tv := info.Types[vs.Values[i]]
				consts[name.Name] = valueOf(tv)
				typesOf[name.Name] = tv.Type
			}
		}
	}
	ck(len(consts) == 7, fmt.Sprintf("const count=%d", len(consts)))
	for _, name := range []string{"Third", "Tiny", "Huge", "Tie", "Sub"} {
		v := consts[name]
		ck(v != nil && v.Kind() == constant.Float, fmt.Sprintf("%s kind=%v", name, v.Kind()))
		ck(typesOf[name] == types.Typ[types.UntypedFloat], fmt.Sprintf("%s type=%v", name, typesOf[name]))
	}
	for _, name := range []string{"MaxI", "OverI"} {
		v := consts[name]
		ck(v != nil && v.Kind() == constant.Int, fmt.Sprintf("%s kind=%v", name, v.Kind()))
		ck(typesOf[name] == types.Typ[types.UntypedInt], fmt.Sprintf("%s type=%v", name, typesOf[name]))
	}

	// 2) 精确值：1/3 与整数边界。
	third := consts["Third"]
	ck(third.ExactString() == "1/3", fmt.Sprintf("Third exact=%s", third.ExactString()))
	if v, exact := constant.Int64Val(consts["MaxI"]); v != math.MaxInt64 || !exact {
		ck(false, fmt.Sprintf("MaxI int64=(%d,%v)", v, exact))
	}
	if _, exact := constant.Int64Val(consts["OverI"]); exact {
		ck(false, "OverI must not fit in int64")
	}
	ck(constant.BitLen(consts["OverI"]) == 64, fmt.Sprintf("OverI bitlen=%d", constant.BitLen(consts["OverI"])))
	ck(constant.ToInt(constant.MakeFloat64(2.5)).Kind() == constant.Unknown, "ToInt(2.5) must be Unknown")
	ck(constant.ToInt(consts["Tie"]).Kind() == constant.Unknown, "ToInt(Tie) must be Unknown")

	// 3) float32/float64 舍入：半程 ties-to-even、次正规、溢出。
	if got, exact := bits32(consts["Tie"]); got != 0x3f800000 || exact {
		ck(false, fmt.Sprintf("float32(Tie)=%08x exact=%v", got, exact))
	}
	if got, _ := bits32(consts["Sub"]); got != 1 {
		ck(false, fmt.Sprintf("float32(1.4e-45)=%08x", got))
	}
	if got, _ := bits32(consts["Tiny"]); got != 0 {
		ck(false, fmt.Sprintf("float32(1e-46)=%08x", got))
	}
	if f, exact := constant.Float32Val(consts["Huge"]); !math.IsInf(float64(f), 1) || exact {
		ck(false, fmt.Sprintf("float32(1e39)=%v exact=%v", f, exact))
	}
	if f, _ := constant.Float32Val(constant.MakeInt64(-1)); f != -1 {
		ck(false, fmt.Sprintf("float32(-1)=%v", f))
	}
	if got, exact := bits32(constant.MakeInt64(16777217)); got != 0x4b800000 || exact {
		ck(false, fmt.Sprintf("float32(16777217)=%08x exact=%v", got, exact))
	}
	if got, exact := bits64(constant.MakeInt64(9007199254740993)); got != 0x4340000000000000 || exact {
		ck(false, fmt.Sprintf("float64(2^53+1)=%016x exact=%v", got, exact))
	}
	if got, exact := bits64(third); got != uint64(math.Float64bits(1.0/3.0)) || exact {
		ck(false, fmt.Sprintf("float64(1/3)=%016x exact=%v", got, exact))
	}
	if got, exact := bits32(constant.MakeInt64(16777216)); got != 0x4b800000 || !exact {
		ck(false, fmt.Sprintf("float32(2^24)=%08x exact=%v", got, exact))
	}

	// 4) 与运行期转换按位对齐（避免只看编译期折叠结果）。
	thirdRT := 1.0 / 3.0
	tieRT := 1.000000059604644775390625
	subRT := 1.4e-45
	tinyRT := 1e-46
	hugeRT := 1e39
	ck(math.Float32bits(float32(thirdRT)) == func() uint32 { b, _ := bits32(third); return b }(),
		"runtime float32(1/3) bits mismatch")
	ck(math.Float32bits(float32(tieRT)) == 0x3f800000, fmt.Sprintf("runtime float32(1+2^-24)=%08x", math.Float32bits(float32(tieRT))))
	ck(math.Float32bits(float32(subRT)) == 1, fmt.Sprintf("runtime float32(1.4e-45)=%08x", math.Float32bits(float32(subRT))))
	ck(math.Float32bits(float32(tinyRT)) == 0, fmt.Sprintf("runtime float32(1e-46)=%08x", math.Float32bits(float32(tinyRT))))
	ck(math.IsInf(float64(float32(hugeRT)), 1), "runtime float32(1e39) must be +Inf")
	i24RT := 16777217
	ck(math.Float32bits(float32(i24RT)) == 0x4b800000, fmt.Sprintf("runtime float32(16777217)=%08x", math.Float32bits(float32(i24RT))))
	i53RT := int64(9007199254740993)
	ck(math.Float64bits(float64(i53RT)) == 0x4340000000000000, fmt.Sprintf("runtime float64(2^53+1)=%016x", math.Float64bits(float64(i53RT))))
	ck(math.Float64bits(1.0/3.0) == math.Float64bits(thirdRT), "runtime 1/3 unstable")

	// 5) 全部常量跨 GC 仍可读取，种子打乱复核顺序。
	names := []string{"Third", "Tiny", "Huge", "Tie", "Sub", "MaxI", "OverI"}
	check := func(tag string) {
		ck(consts["Third"].ExactString() == "1/3", tag+" Third")
		ck(consts["Tiny"].Kind() == constant.Float, tag+" Tiny")
		ck(consts["Huge"].Kind() == constant.Float, tag+" Huge")
		if got, _ := bits32(consts["Tie"]); got != 0x3f800000 {
			ck(false, fmt.Sprintf("%s Tie bits=%08x", tag, got))
		}
		if got, _ := bits32(consts["Sub"]); got != 1 {
			ck(false, fmt.Sprintf("%s Sub bits=%08x", tag, got))
		}
		if v, exact := constant.Int64Val(consts["MaxI"]); v != math.MaxInt64 || !exact {
			ck(false, tag+" MaxI")
		}
	}
	check("before GC")
	for round := 0; round < 3; round++ {
		gcStress()
		check(fmt.Sprintf("round %d", round))
		for _, idx := range rng.Perm(len(names)) {
			name := names[idx]
			ck(consts[name] != nil && typesOf[name] != nil, fmt.Sprintf("round %d lost %s", round, name))
		}
		for _, idx := range rng.Perm(len(names)) {
			name := names[idx]
			rebuilt := constant.MakeFromLiteral(strconv.FormatInt(int64(idx+1), 10), token.INT, 0)
			ck(rebuilt.Kind() == constant.Int, fmt.Sprintf("round %d rebuild %s", round, name))
		}
	}

	finish("l16_const_convert_round")
}
