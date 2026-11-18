// L16: go/constant 任意精度整数/有理数/复数/字符串/布尔运算、未知值与转换；Value 跨 GC 可用。
package main

import (
	"fmt"
	"go/constant"
	"go/token"
	"math"
	"math/rand"
	"os"
	"runtime"
	"strconv"
	"sync/atomic"
)

var (
	failed  int32
	gcSink  [][]byte
	keep    []keptValue
	uafSeed int64 = 1
)

type keptValue struct {
	src  string
	val  constant.Value
	want string
	kind constant.Kind
}

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

func tortureRounds(def int) int {
	s := os.Getenv("TORTURE_ROUNDS")
	if s == "" {
		return def
	}
	n, err := strconv.Atoi(s)
	if err != nil || n <= 0 {
		return def
	}
	if n > 200000 {
		n = 200000
	}
	return n
}

func mustPanic(fn func()) (panicked bool) {
	defer func() {
		if recover() != nil {
			panicked = true
		}
	}()
	fn()
	return false
}

func lit(s string, tok token.Token) constant.Value {
	return constant.MakeFromLiteral(s, tok, 0)
}

func checkValue(src string, v constant.Value, want string, kind constant.Kind, label string) {
	ck(v != nil, fmt.Sprintf("%s %s: nil value", label, src))
	if v == nil {
		return
	}
	ck(v.ExactString() == want, fmt.Sprintf("%s %s=%s want %s", label, src, v.ExactString(), want))
	ck(v.Kind() == kind, fmt.Sprintf("%s %s kind=%v want %v", label, src, v.Kind(), kind))
	keep = append(keep, keptValue{src: src, val: v, want: want, kind: kind})
}

func main() {
	uafSeed = envSeed()
	rounds := tortureRounds(6)

	// 字面量解析：Int/Float/Imag/Char/String 与非法字面量。
	checkValue("42", lit("42", token.INT), "42", constant.Int, "lit")
	checkValue("0x2a", lit("0x2a", token.INT), "42", constant.Int, "lit")
	checkValue("0b1010", lit("0b1010", token.INT), "10", constant.Int, "lit")
	checkValue("1_000", lit("1_000", token.INT), "1000", constant.Int, "lit")
	checkValue("'A'", lit("'A'", token.CHAR), "65", constant.Int, "lit")
	checkValue("3.5", lit("3.5", token.FLOAT), "7/2", constant.Float, "lit")
	checkValue("2i", lit("2i", token.IMAG), "(0 + 2i)", constant.Complex, "lit")
	checkValue(`"hi\n"`, lit(`"hi\n"`, token.STRING), `"hi\n"`, constant.String, "lit")
	checkValue("bad", lit("?", token.INT), "unknown", constant.Unknown, "lit")
	ck(constant.StringVal(lit(`"hi\n"`, token.STRING)) == "hi\n", "StringVal mismatch")

	// Make 只接受文档列出的动态类型；int/uint64/float64 落到 Unknown。
	checkValue("Make(bool)", constant.Make(true), "true", constant.Bool, "make")
	checkValue("Make(string)", constant.Make("hi"), `"hi"`, constant.String, "make")
	checkValue("Make(int64)", constant.Make(int64(42)), "42", constant.Int, "make")
	checkValue("Make(int)", constant.Make(42), "unknown", constant.Unknown, "make")
	checkValue("Make(uint64)", constant.Make(uint64(42)), "unknown", constant.Unknown, "make")
	checkValue("Make(float64)", constant.Make(3.5), "unknown", constant.Unknown, "make")

	// MakeFromBytes 以小端二进制解释为 Int；Bytes 往返。
	raw := []byte{104, 101, 108, 108, 111}
	fromBytes := constant.MakeFromBytes(raw)
	checkValue("MakeFromBytes", fromBytes, "478560413032", constant.Int, "bytes")
	gotBytes := constant.Bytes(fromBytes)
	ck(len(gotBytes) == len(raw), fmt.Sprintf("Bytes len=%d want %d", len(gotBytes), len(raw)))
	for i := range gotBytes {
		ck(gotBytes[i] == raw[i], fmt.Sprintf("Bytes[%d]=%d want %d", i, gotBytes[i], raw[i]))
	}
	checkValue("MakeFromBytes(empty)", constant.MakeFromBytes(nil), "0", constant.Int, "bytes")

	// 任意精度：1<<100、MinInt64 的绝对值、MaxUint64+1。
	big := constant.Shift(constant.MakeInt64(1), token.SHL, 100)
	checkValue("1<<100", big, "1267650600228229401496703205376", constant.Int, "big")
	if _, ok := constant.Int64Val(big); ok {
		ck(false, "Int64Val(big) unexpectedly exact")
	}
	if _, ok := constant.Uint64Val(big); ok {
		ck(false, "Uint64Val(big) unexpectedly exact")
	}
	ck(constant.BitLen(big) == 101, fmt.Sprintf("BitLen(1<<100)=%d want 101", constant.BitLen(big)))
	checkValue("MaxUint64+1", constant.BinaryOp(constant.MakeUint64(math.MaxUint64), token.ADD, constant.MakeUint64(1)),
		"18446744073709551616", constant.Int, "big")
	checkValue("1<<64+1", constant.BinaryOp(constant.Shift(constant.MakeInt64(1), token.SHL, 64), token.ADD, constant.MakeInt64(1)),
		"18446744073709551617", constant.Int, "big")

	// 有理数与转换。
	third := constant.BinaryOp(constant.MakeFloat64(1), token.QUO, constant.MakeFloat64(3))
	checkValue("1/3", third, "1/3", constant.Float, "float")
	checkValue("Num(1/3)", constant.Num(third), "1", constant.Int, "float")
	checkValue("Denom(1/3)", constant.Denom(third), "3", constant.Int, "float")
	if f, ok := constant.Float64Val(constant.MakeFloat64(0.5)); f != 0.5 || !ok {
		ck(false, fmt.Sprintf("Float64Val(0.5)=(%v,%v)", f, ok))
	}
	if f, ok := constant.Float64Val(third); f != 1.0/3.0 || ok {
		ck(false, fmt.Sprintf("Float64Val(1/3)=(%.17g,%v)", f, ok))
	}
	checkValue("ToInt(2.0)", constant.ToInt(constant.MakeFloat64(2.0)), "2", constant.Int, "conv")
	checkValue("ToInt(2.9)", constant.ToInt(constant.MakeFloat64(2.9)), "unknown", constant.Unknown, "conv")
	checkValue("ToFloat(7)", constant.ToFloat(constant.MakeInt64(7)), "7", constant.Float, "conv")
	checkValue("ToComplex(7)", constant.ToComplex(constant.MakeInt64(7)), "(7 + 0i)", constant.Complex, "conv")

	// 一元/算术/比较/字符串/布尔。
	checkValue("^1", constant.UnaryOp(token.XOR, constant.MakeInt64(1), 0), "-2", constant.Int, "unary")
	checkValue("-(-5)", constant.UnaryOp(token.SUB, constant.UnaryOp(token.SUB, constant.MakeInt64(5), 0), 0),
		"5", constant.Int, "unary")
	checkValue("!true", constant.UnaryOp(token.NOT, constant.MakeBool(true), 0), "false", constant.Bool, "unary")
	checkValue("7/=2", constant.BinaryOp(constant.MakeInt64(7), token.QUO_ASSIGN, constant.MakeInt64(2)), "3", constant.Int, "arith")
	checkValue("-7/=2", constant.BinaryOp(constant.MakeInt64(-7), token.QUO_ASSIGN, constant.MakeInt64(2)), "-3", constant.Int, "arith")
	checkValue("-7%2", constant.BinaryOp(constant.MakeInt64(-7), token.REM, constant.MakeInt64(2)), "-1", constant.Int, "arith")
	checkValue("7/2", constant.BinaryOp(constant.MakeInt64(7), token.QUO, constant.MakeInt64(2)), "7/2", constant.Float, "arith")
	checkValue(`"ab"+"cd"`, constant.BinaryOp(constant.MakeString("ab"), token.ADD, constant.MakeString("cd")),
		`"abcd"`, constant.String, "string")
	ck(constant.Compare(lit("1", token.INT), token.LSS, lit("1.5", token.FLOAT)), "mixed numeric compare failed")
	ck(!constant.Compare(constant.MakeString("a"), token.LSS, constant.MakeInt64(1)), "incomparable kinds compare should be false")
	ck(constant.Compare(constant.MakeString("a"), token.LSS, constant.MakeString("b")), "string compare failed")
	ck(constant.Sign(constant.MakeInt64(-4)) == -1, "Sign(-4) != -1")
	ck(constant.Sign(constant.MakeInt64(0)) == 0, "Sign(0) != 0")
	ck(constant.BoolVal(constant.MakeBool(true)), "BoolVal(true) false")

	// Unknown 传播与除零 panic。
	checkValue("unknown+1", constant.BinaryOp(constant.MakeUnknown(), token.ADD, constant.MakeInt64(1)), "unknown", constant.Unknown, "unknown")
	ck(constant.MakeUnknown() == constant.MakeUnknown(), "MakeUnknown values unequal")
	ck(constant.BitLen(constant.MakeUnknown()) == 0, "BitLen(unknown) != 0")
	ck(constant.Sign(constant.MakeUnknown()) == 1, "Sign(unknown) != 1 (avoids div-by-zero diagnostics)")
	ck(mustPanic(func() {
		constant.BinaryOp(constant.MakeInt64(1), token.QUO, constant.MakeInt64(0))
	}), "integer division by zero did not panic")
	ck(mustPanic(func() {
		constant.BinaryOp(constant.MakeInt64(1), token.REM, constant.MakeInt64(0))
	}), "integer remainder by zero did not panic")

	// 随机代数律：交换/结合/除法-余数恒等式/移位等价/比较互补。
	var randomResults []string
	rng := rand.New(rand.NewSource(uafSeed))
	for r := 0; r < rounds; r++ {
		for i := 0; i < 32; i++ {
			a := rng.Int63n(2_000_001) - 1_000_000
			b := rng.Int63n(2_000_001) - 1_000_000
			c := rng.Int63n(2_000_001) - 1_000_000
			if b == 0 {
				b = 3
			}
			va, vb, vc := constant.MakeInt64(a), constant.MakeInt64(b), constant.MakeInt64(c)
			sumAB := constant.BinaryOp(va, token.ADD, vb)
			sumBA := constant.BinaryOp(vb, token.ADD, va)
			ck(sumAB.ExactString() == sumBA.ExactString(), fmt.Sprintf("round %d add not commutative: %d+%d", r, a, b))
			l := constant.BinaryOp(constant.BinaryOp(va, token.ADD, vb), token.ADD, vc)
			rt := constant.BinaryOp(va, token.ADD, constant.BinaryOp(vb, token.ADD, vc))
			ck(l.ExactString() == rt.ExactString(), fmt.Sprintf("round %d add not associative: %d,%d,%d", r, a, b, c))
			ck(constant.BinaryOp(va, token.MUL, vb).ExactString() ==
				constant.BinaryOp(vb, token.MUL, va).ExactString(), fmt.Sprintf("round %d mul not commutative", r))
			diff := constant.BinaryOp(va, token.SUB, vb)
			back := constant.BinaryOp(diff, token.ADD, vb)
			ck(back.ExactString() == va.ExactString(), fmt.Sprintf("round %d subtract/add inverse: %d,%d", r, a, b))
			q := constant.BinaryOp(va, token.QUO_ASSIGN, vb)
			rem := constant.BinaryOp(va, token.REM, vb)
			rebuilt := constant.BinaryOp(constant.BinaryOp(q, token.MUL, vb), token.ADD, rem)
			ck(rebuilt.ExactString() == va.ExactString(),
				fmt.Sprintf("round %d divmod identity: a=%d b=%d q=%s r=%s", r, a, b, q.ExactString(), rem.ExactString()))
			pos := va
			if constant.Sign(pos) < 0 {
				pos = constant.UnaryOp(token.SUB, pos, 0)
			}
			for k := uint(0); k <= 10; k++ {
				shifted := constant.Shift(pos, token.SHL, k)
				mul := constant.BinaryOp(pos, token.MUL, constant.Shift(constant.MakeInt64(1), token.SHL, k))
				ck(shifted.ExactString() == mul.ExactString(), fmt.Sprintf("round %d shift/mul mismatch a=%d k=%d", r, a, k))
			}
			c1 := constant.Compare(va, token.LSS, vb)
			c2 := constant.Compare(va, token.GEQ, vb)
			ck(c1 == !c2, fmt.Sprintf("round %d compare complement: %d,%d", r, a, b))
			randomResults = append(randomResults, sumAB.ExactString()+"/"+rebuilt.ExactString())
		}
		gcStress()
	}

	// GC 后所有保留的 Value 与随机结果必须一致。
	for _, kv := range keep {
		ck(kv.val.ExactString() == kv.want, fmt.Sprintf("after GC %s=%s want %s", kv.src, kv.val.ExactString(), kv.want))
		ck(kv.val.Kind() == kv.kind, fmt.Sprintf("after GC %s kind=%v want %v", kv.src, kv.val.Kind(), kv.kind))
	}
	rng2 := rand.New(rand.NewSource(uafSeed))
	idx := 0
	for r := 0; r < rounds; r++ {
		for i := 0; i < 32; i++ {
			a := rng2.Int63n(2_000_001) - 1_000_000
			b := rng2.Int63n(2_000_001) - 1_000_000
			_ = rng2.Int63n(2_000_001) // c，与上面生成序列保持一致
			if b == 0 {
				b = 3
			}
			va, vb := constant.MakeInt64(a), constant.MakeInt64(b)
			sum := constant.BinaryOp(va, token.ADD, vb).ExactString()
			q := constant.BinaryOp(va, token.QUO_ASSIGN, vb)
			rem := constant.BinaryOp(va, token.REM, vb)
			rebuilt := constant.BinaryOp(constant.BinaryOp(q, token.MUL, vb), token.ADD, rem).ExactString()
			idx++
			ck(randomResults[idx-1] == sum+"/"+rebuilt,
				fmt.Sprintf("after GC random replay mismatch at %d", idx))
		}
	}

	finish("l16_constant_ops")
}
