// L16: go/constant 的精确值/舍入语义：ToInt 可表示性、Float32/64 舍入标志、
// 有理数精确比较、位宽边界、复数字面量与跨 GC 稳定性。
package main

import (
	"fmt"
	"go/constant"
	"go/token"
	"math"
	"os"
	"runtime"
	"strconv"
	"sync/atomic"
)

var (
	failed  int32
	gcSink  [][]byte
	live    []constant.Value
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

func literal(s string, tok token.Token) constant.Value {
	return constant.MakeFromLiteral(s, tok, 0)
}

func main() {
	uafSeed = envSeed()

	// 字面量解析与 Kind。
	i42 := literal("42", token.INT)
	ck(i42.Kind() == constant.Int, "42 kind")
	ck(i42.ExactString() == "42", fmt.Sprintf("42 exact=%s", i42.ExactString()))
	if v, exact := constant.Int64Val(i42); v != 42 || !exact {
		ck(false, fmt.Sprintf("42 int64=(%d,%v)", v, exact))
	}
	ck(constant.BitLen(i42) == 6, fmt.Sprintf("42 bitlen=%d", constant.BitLen(i42)))
	ck(constant.Sign(i42) == 1, "42 sign")

	neg := literal("-7", token.INT)
	ck(neg.Kind() == constant.Int && constant.Sign(neg) == -1, "negative literal")
	if v, exact := constant.Int64Val(neg); v != -7 || !exact {
		ck(false, fmt.Sprintf("-7 int64=(%d,%v)", v, exact))
	}

	f30 := literal("3.0", token.FLOAT)
	ck(f30.Kind() == constant.Float, "3.0 kind")
	ck(f30.ExactString() == "3", fmt.Sprintf("3.0 exact=%s", f30.ExactString()))
	ti := constant.ToInt(f30)
	ck(ti.Kind() == constant.Int && ti.ExactString() == "3", fmt.Sprintf("ToInt(3.0)=%v", ti))
	f25 := literal("2.5", token.FLOAT)
	ck(f25.ExactString() == "5/2", fmt.Sprintf("2.5 exact=%s", f25.ExactString()))
	ck(constant.ToInt(f25).Kind() == constant.Unknown, "ToInt(2.5) must be Unknown")
	f00 := literal("-0.0", token.FLOAT)
	z := constant.ToInt(f00)
	ck(z.Kind() == constant.Int && z.ExactString() == "0", fmt.Sprintf("ToInt(-0.0)=%v", z))

	ch := literal("'a'", token.CHAR)
	ck(ch.Kind() == constant.Int && ch.ExactString() == "97", fmt.Sprintf("char=%v", ch))

	hs := literal(`"hé"`, token.STRING)
	ck(hs.Kind() == constant.String && constant.StringVal(hs) == "hé", "string literal")
	ck(constant.MakeString("a\x00b").ExactString() == `"a\x00b"`, "string ExactString quoting")

	hexf := literal("0x1p-2", token.FLOAT)
	ck(hexf.Kind() == constant.Float && hexf.ExactString() == "1/4", fmt.Sprintf("hex float=%v", hexf))

	// 有理数精度与跨 Kind 比较。
	third := constant.BinaryOp(constant.MakeInt64(1), token.QUO, constant.MakeInt64(3))
	ck(third.Kind() == constant.Float, "1/3 kind")
	ck(third.ExactString() == "1/3", fmt.Sprintf("1/3 exact=%s", third.ExactString()))
	ck(constant.Num(third).ExactString() == "1" && constant.Denom(third).ExactString() == "3", "1/3 num/denom")
	ck(constant.Compare(third, token.GTR, constant.MakeFloat64(1.0/3.0)),
		"exact 1/3 must exceed float64(1/3)")
	ck(constant.Compare(constant.MakeInt64(1), token.EQL, constant.MakeFloat64(1)), "1 == 1.0")
	ck(constant.Compare(constant.MakeInt64(1), token.LSS, constant.MakeFloat64(1.5)), "1 < 1.5")
	ck(constant.Compare(constant.MakeString("a"), token.LSS, constant.MakeString("b")), `"a" < "b"`)
	ck(constant.Compare(constant.MakeBool(true), token.NEQ, constant.MakeBool(false)), "true != false")

	// Float32/64 舍入：精确标志。
	fv64, exact64 := constant.Float64Val(third)
	ck(!exact64 && fv64 == 1.0/3.0, fmt.Sprintf("float64(1/3)=(%.17g,%v)", fv64, exact64))
	fv32, exact32 := constant.Float32Val(third)
	ck(!exact32 && fv32 == float32(1.0/3.0), fmt.Sprintf("float32(1/3)=(%.9g,%v)", fv32, exact32))
	if v, ok := constant.Float32Val(constant.MakeFloat64(0.5)); v != 0.5 || !ok {
		ck(false, fmt.Sprintf("float32(0.5)=(%v,%v)", v, ok))
	}
	if v, ok := constant.Float64Val(constant.MakeFloat64(0.5)); v != 0.5 || !ok {
		ck(false, fmt.Sprintf("float64(0.5)=(%v,%v)", v, ok))
	}
	if v, ok := constant.Float64Val(constant.MakeFloat64(math.Pi)); v != math.Pi || !ok {
		ck(false, fmt.Sprintf("float64(pi)=(%.17g,%v)", v, ok))
	}
	p32, pok := constant.Float32Val(constant.MakeFloat64(math.Pi))
	ck(p32 == float32(math.Pi) && !pok, fmt.Sprintf("float32(pi)=(%.9g,%v)", p32, pok))
	// 0.1 作为 float64 常量：精确有理数分母为 2^55。
	tenth := constant.MakeFloat64(0.1)
	ck(tenth.Kind() == constant.Float, "0.1 kind")
	ck(tenth.ExactString() == "3602879701896397/36028797018963968",
		fmt.Sprintf("0.1 exact=%s", tenth.ExactString()))
	if v, ok := constant.Float64Val(tenth); v != 0.1 || !ok {
		ck(false, fmt.Sprintf("float64(0.1)=(%v,%v)", v, ok))
	}
	if _, ok := constant.Float32Val(tenth); ok {
		ck(false, "float32(0.1) must be inexact")
	}

	// ToFloat/ToComplex 与复数分量。
	tf := constant.ToFloat(constant.MakeInt64(7))
	ck(tf.Kind() == constant.Float && tf.ExactString() == "7", fmt.Sprintf("ToFloat(7)=%v", tf))
	tc := constant.ToComplex(constant.MakeInt64(7))
	ck(tc.Kind() == constant.Complex, "ToComplex(7) kind")
	ck(constant.Real(tc).ExactString() == "7" && constant.Imag(tc).ExactString() == "0",
		fmt.Sprintf("ToComplex real/imag=%v/%v", constant.Real(tc), constant.Imag(tc)))
	im := constant.MakeImag(constant.MakeInt64(4))
	ck(im.Kind() == constant.Complex, "MakeImag kind")
	ck(constant.Real(im).ExactString() == "0" && constant.Imag(im).ExactString() == "4", "imag components")
	cx := constant.BinaryOp(constant.MakeInt64(3), token.ADD, im)
	ck(cx.ExactString() == "(3 + 4i)", fmt.Sprintf("3+4i exact=%s", cx.ExactString()))
	ck(constant.Real(cx).Kind() == constant.Int && constant.Imag(cx).Kind() == constant.Int, "complex component kinds")

	// 位宽边界。
	big := constant.Shift(constant.MakeInt64(1), token.SHL, 100)
	ck(constant.BitLen(big) == 101, fmt.Sprintf("1<<100 bitlen=%d", constant.BitLen(big)))
	ck(constant.Shift(big, token.SHR, 100).ExactString() == "1", "1<<100 >> 100 = 1")
	ck(constant.Shift(big, token.SHR, 99).ExactString() == "2", "1<<100 >> 99 = 2")
	b63 := constant.Shift(constant.MakeInt64(1), token.SHL, 63)
	if v, exact := constant.Int64Val(b63); v != math.MinInt64 || exact {
		ck(false, fmt.Sprintf("Int64Val(1<<63)=(%d,%v)", v, exact))
	}
	if v, exact := constant.Uint64Val(b63); v != 1<<63 || !exact {
		ck(false, fmt.Sprintf("Uint64Val(1<<63)=(%d,%v)", v, exact))
	}
	if v, exact := constant.Uint64Val(constant.Shift(constant.MakeInt64(1), token.SHL, 64)); v != 0 || exact {
		ck(false, fmt.Sprintf("Uint64Val(1<<64)=(%d,%v)", v, exact))
	}
	ck(constant.UnaryOp(token.XOR, constant.MakeInt64(1), 0).ExactString() == "-2", "unary ^1")
	ck(constant.UnaryOp(token.XOR, constant.MakeInt64(-1), 0).ExactString() == "0", "unary ^-1")
	ck(constant.UnaryOp(token.SUB, constant.MakeInt64(5), 0).ExactString() == "-5", "unary -5")
	ck(constant.UnaryOp(token.ADD, constant.MakeInt64(5), 0).ExactString() == "5", "unary +5")
	ck(constant.UnaryOp(token.NOT, constant.MakeBool(true), 0).Kind() == constant.Bool &&
		!constant.BoolVal(constant.UnaryOp(token.NOT, constant.MakeBool(true), 0)), "unary !true")

	// Unknown 传播。
	unk := constant.MakeUnknown()
	ck(constant.BinaryOp(unk, token.ADD, constant.MakeInt64(1)).Kind() == constant.Unknown, "unknown add")
	ck(constant.UnaryOp(token.SUB, unk, 0).Kind() == constant.Unknown, "unknown neg")
	ck(constant.ToFloat(unk).Kind() == constant.Unknown, "unknown ToFloat")

	// 全部保留值跨 GC 仍可读取。
	live = []constant.Value{i42, neg, f30, f25, ch, hs, third, tenth, tf, tc, cx, big, b63, unk}
	for i := range live {
		live[i].Kind()
	}
	check := func(tag string) {
		ck(live[0].ExactString() == "42" && live[0].Kind() == constant.Int, tag+" 42")
		ck(live[6].ExactString() == "1/3", tag+" 1/3")
		ck(live[8].Kind() == constant.Float && live[8].ExactString() == "7", tag+" ToFloat")
		ck(live[10].ExactString() == "(3 + 4i)", tag+" complex")
		ck(live[11].ExactString() == "1267650600228229401496703205376", tag+" 1<<100")
		if v, exact := constant.Int64Val(live[12]); v != math.MinInt64 || exact {
			ck(false, tag+" 1<<63")
		}
		ck(constant.StringVal(live[5]) == "hé", tag+" string")
	}
	check("before GC")

	for round := 0; round < 3; round++ {
		gcStress()
		check(fmt.Sprintf("round %d", round))
		// 种子打乱随机复核顺序：重算 1/3 与 float64 比较方向。
		order := []int{0, 1, 2}
		for k := 0; k < len(order); k++ {
			j := (int(uafSeed) + round + k) % (k + 1)
			order[k], order[j] = order[j], order[k]
		}
		for _, which := range order {
			var v constant.Value
			switch which {
			case 0:
				v = constant.BinaryOp(constant.MakeInt64(1), token.QUO, constant.MakeInt64(3))
			case 1:
				v = constant.Shift(constant.MakeInt64(1), token.SHL, uint(100+round))
			default:
				v = constant.MakeFloat64(0.5)
			}
			ck(v.Kind() != constant.Unknown, fmt.Sprintf("round %d rebuilt value", round))
		}
	}

	finish("l16_constant_rounding")
}
