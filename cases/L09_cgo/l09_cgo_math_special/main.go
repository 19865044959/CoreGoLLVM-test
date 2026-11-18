// L09: C 数学库特殊值/舍入函数：exp/log/atan2/cbrt 与 Go math 容差对照，
// trunc/round/rint/nearbyint/frexp/modf/copysign/fmin/fmax 与 NaN/Inf 语义
// 精确校验；跨多轮 GC 重复调用。C 不保留 Go 指针（仅同步写栈上出参）。
package main

/*
#cgo LDFLAGS: -lm
#include <math.h>
#include <stdint.h>

static double c_exp(double x) { return exp(x); }
static double c_log(double x) { return log(x); }
static double c_log10(double x) { return log10(x); }
static double c_atan2(double y, double x) { return atan2(y, x); }
static double c_cbrt(double x) { return cbrt(x); }
static double c_trunc(double x) { return trunc(x); }
static double c_round(double x) { return round(x); }
static double c_rint(double x) { return rint(x); }
static double c_nearbyint(double x) { return nearbyint(x); }
static double c_copysign(double x, double y) { return copysign(x, y); }
static double c_fmin(double x, double y) { return fmin(x, y); }
static double c_fmax(double x, double y) { return fmax(x, y); }
static int32_t c_isnan(double x) { return isnan(x) != 0; }
static int32_t c_isinf(double x) { return isinf(x) != 0; }
static int32_t c_signbit(double x) { return signbit(x) != 0; }
static double c_frexp_m(double x, int32_t *e) {
	int ee = 0;
	double m = frexp(x, &ee);
	*e = (int32_t)ee;
	return m;
}
static double c_modf_i(double x, double *ip) { return modf(x, ip); }
*/
import "C"

import (
	"fmt"
	"math"
	"os"
	"runtime"
	"sync/atomic"
)

var (
	failed int32
	gcSink [][]byte
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
	gcSink = gcSink[:0]
	runtime.GC()
	runtime.GC()
}

func finish(name string) {
	if n := atomic.LoadInt32(&failed); n != 0 {
		fmt.Printf("FAILED %s (%d failed checks)\n", name, n)
		os.Exit(1)
	}
	fmt.Println("ALL PASS " + name)
}

func closeTo(a, b, tol float64) bool {
	if math.IsNaN(a) || math.IsNaN(b) {
		return math.IsNaN(a) && math.IsNaN(b)
	}
	if math.IsInf(a, 0) || math.IsInf(b, 0) {
		return a == b
	}
	d := math.Abs(a - b)
	if d <= tol {
		return true
	}
	return d <= tol*math.Max(math.Abs(a), math.Abs(b))
}

func main() {
	ck(float64(C.c_exp(0)) == 1, "exp(0)")
	ck(closeTo(float64(C.c_exp(1)), math.E, 1e-15), "exp(1)")
	ck(float64(C.c_log(1)) == 0, "log(1)")
	ck(closeTo(float64(C.c_log(math.E)), 1, 1e-15), "log(e)")
	ck(closeTo(float64(C.c_log10(1000)), 3, 1e-15), "log10(1000)")
	ck(closeTo(float64(C.c_atan2(1, 1)), math.Pi/4, 1e-15), "atan2(1,1)")
	ck(closeTo(float64(C.c_atan2(0, -1)), math.Pi, 1e-15), "atan2(0,-1)")
	ck(closeTo(float64(C.c_cbrt(-27)), -3, 1e-15), "cbrt(-27)")
	ck(closeTo(float64(C.c_cbrt(1e-9)), 1e-3, 1e-14), "cbrt(1e-9)")

	// 精确舍入函数逐值与 Go math 对照。
	vals := []float64{0, 0.5, 1.5, 2.4, 2.5, 2.6, -0.5, -1.5, -2.5, -2.6, 1e18, -1e18}
	for _, v := range vals {
		ck(float64(C.c_trunc(C.double(v))) == math.Trunc(v), fmt.Sprintf("trunc(%v)", v))
		ck(float64(C.c_round(C.double(v))) == math.Round(v), fmt.Sprintf("round(%v)", v))
		ck(float64(C.c_rint(C.double(v))) == math.RoundToEven(v), fmt.Sprintf("rint(%v)", v))
		ck(float64(C.c_nearbyint(C.double(v))) == math.RoundToEven(v), fmt.Sprintf("nearbyint(%v)", v))
	}

	// frexp/modf 的输出（尾数、指数、整数部分）逐位对照。
	frexpVals := []float64{0, 1, -1, 0.5, 3.5, -1024, 1e-300, math.MaxFloat64}
	for _, v := range frexpVals {
		var e C.int32_t
		m := float64(C.c_frexp_m(C.double(v), &e))
		wm, we := math.Frexp(v)
		ck(m == wm && int32(e) == int32(we), fmt.Sprintf("frexp(%v)", v))
	}
	modfVals := []float64{3.75, -3.75, 0, -0.5, 1e20, -1e20}
	for _, v := range modfVals {
		var ip C.double
		fr := float64(C.c_modf_i(C.double(v), &ip))
		wip, wfr := math.Modf(v)
		ck(fr == wfr && float64(ip) == wip, fmt.Sprintf("modf(%v)", v))
	}

	// NaN / Inf / 负零 / fmin/fmax 语义。
	nan := math.NaN()
	inf := math.Inf(1)
	ninf := math.Inf(-1)
	ck(int32(C.c_isnan(C.double(nan))) == 1, "isnan(NaN)")
	ck(int32(C.c_isnan(C.double(1))) == 0, "isnan(1)")
	ck(int32(C.c_isinf(C.double(inf))) == 1, "isinf(+Inf)")
	ck(int32(C.c_isinf(C.double(ninf))) == 1, "isinf(-Inf)")
	ck(int32(C.c_isinf(C.double(1))) == 0, "isinf(1)")
	ck(int32(C.c_signbit(C.double(ninf))) == 1, "signbit(-Inf)")
	negzero := float64(C.c_copysign(0, -1))
	ck(negzero == 0 && int32(C.c_signbit(C.double(negzero))) == 1, "copysign negative zero")
	ck(math.IsInf(1/negzero, -1), "1/-0 is -Inf")
	ck(math.Float64bits(negzero) == math.Float64bits(math.Copysign(0, -1)), "copysign bits")
	ck(float64(C.c_fmin(C.double(nan), 3)) == 3, "fmin(NaN,3)")
	ck(float64(C.c_fmax(C.double(nan), 3)) == 3, "fmax(NaN,3)")
	ck(float64(C.c_fmin(2, 3)) == 2 && float64(C.c_fmax(2, 3)) == 3, "fmin/fmax normal")
	ck(float64(C.c_fmin(-5, -7)) == -7 && float64(C.c_fmax(-5, -7)) == -5, "fmin/fmax negative")

	// 跨多轮 GC 重复调用，验证结果稳定且 C 不持有 Go 内存。
	trans := []float64{0.25, 1, 2, 3.141592653589793, 1e6, -1e-6, 12345.6789}
	for round := 0; round < 64; round++ {
		for i, v := range trans {
			ck(closeTo(float64(C.c_exp(C.double(v))), math.Exp(v), 1e-12),
				fmt.Sprintf("exp round=%d i=%d", round, i))
			ck(closeTo(float64(C.c_log(C.double(v))), math.Log(v), 1e-12),
				fmt.Sprintf("log round=%d i=%d", round, i))
			ck(closeTo(float64(C.c_atan2(C.double(v), 1)), math.Atan2(v, 1), 1e-12),
				fmt.Sprintf("atan2 round=%d i=%d", round, i))
			ck(closeTo(float64(C.c_cbrt(C.double(v))), math.Cbrt(v), 1e-14),
				fmt.Sprintf("cbrt round=%d i=%d", round, i))
			ck(float64(C.c_round(C.double(v))) == math.Round(v),
				fmt.Sprintf("round round=%d i=%d", round, i))
		}
		if round%16 == 15 {
			gcStress()
		}
	}

	gcStress()
	ck(closeTo(float64(C.c_exp(2)), math.Exp(2), 1e-15), "exp after GC")
	ck(float64(C.c_round(2.5)) == 3 && float64(C.c_round(-2.5)) == -3, "round halves after GC")
	finish("l09_cgo_math_special")
}
