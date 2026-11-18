// L09: 通过 cgo 调用 C 标准数学库（libm）：sqrt/hypot/pow/sin/fmod/floor/ceil，
// 与 Go math 包对照（浮点用容差）；跨 GC 重复调用结果稳定。
package main

import (
	"fmt"
	"math"
	"os"
	"runtime"
	"sync/atomic"
)

/*
#cgo LDFLAGS: -lm
#include <math.h>
#include <stdint.h>

static double c_sqrt(double x) { return sqrt(x); }
static double c_hypot(double x, double y) { return hypot(x, y); }
static double c_pow(double x, double y) { return pow(x, y); }
static double c_sin(double x) { return sin(x); }
static double c_cos(double x) { return cos(x); }
static double c_fmod(double x, double y) { return fmod(x, y); }
static double c_floor(double x) { return floor(x); }
static double c_ceil(double x) { return ceil(x); }
static double c_fabs(double x) { return fabs(x); }
static double c_ldexp_pow2(int32_t e) { return ldexp(1.0, (int)e); }
*/
import "C"

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

func close(a, b, tol float64) bool {
	d := math.Abs(a - b)
	if d <= tol {
		return true
	}
	return d <= tol*math.Max(math.Abs(a), math.Abs(b))
}

func main() {
	ck(close(float64(C.c_sqrt(2)), math.Sqrt2, 1e-15), "sqrt(2)")
	ck(close(float64(C.c_sqrt(144)), 12, 1e-15), "sqrt(144)")
	ck(close(float64(C.c_hypot(3, 4)), 5, 1e-15), "hypot(3,4)")
	ck(close(float64(C.c_pow(2, 10)), 1024, 1e-15), "pow(2,10)")
	ck(close(float64(C.c_pow(2, -0.5)), 1/math.Sqrt2, 1e-14), "pow(2,-0.5)")
	ck(close(float64(C.c_sin(math.Pi/2)), 1, 1e-15), "sin(pi/2)")
	ck(close(float64(C.c_cos(0)), 1, 1e-15), "cos(0)")
	ck(close(float64(C.c_fmod(17, 5)), 2, 1e-15), "fmod(17,5)")
	ck(float64(C.c_floor(-2.5)) == -3, "floor(-2.5)")
	ck(float64(C.c_ceil(-2.5)) == -2, "ceil(-2.5)")
	ck(float64(C.c_fabs(-7.25)) == 7.25, "fabs")

	// 2 的幂（ldexp）覆盖指数边界。
	for e := int32(-10); e <= 40; e++ {
		want := math.Ldexp(1, int(e))
		got := float64(C.c_ldexp_pow2(C.int32_t(e)))
		if e >= -10 && e <= 40 {
			ck(close(got, want, 1e-15), fmt.Sprintf("ldexp e=%d", e))
		}
	}

	// 跨 GC 的重复调用。
	vals := []float64{0, 0.5, 1, 2, 3.141592653589793, 1e6, -1e-6, 12345.6789}
	for round := 0; round < 64; round++ {
		for i, v := range vals {
			ck(close(float64(C.c_sqrt(C.double(v*v))), math.Abs(v), 1e-12),
				fmt.Sprintf("sqrt(x*x) round=%d i=%d", round, i))
			ck(close(float64(C.c_sin(C.double(v))), math.Sin(v), 1e-12),
				fmt.Sprintf("sin round=%d i=%d", round, i))
			ck(close(float64(C.c_pow(C.double(v), 2)), v*v, 1e-11),
				fmt.Sprintf("pow2 round=%d i=%d", round, i))
		}
		if round%16 == 15 {
			gcStress()
		}
	}

	gcStress()
	ck(close(float64(C.c_hypot(5, 12)), 13, 1e-15), "hypot after GC")
	ck(float64(C.c_floor(9.99)) == 9 && float64(C.c_ceil(9.01)) == 10, "rounding after GC")

	finish("l09_cgo_libm_math")
}
