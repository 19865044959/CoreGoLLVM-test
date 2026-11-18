// L09: C 数学精度与浮点环境：通过 C 辅助函数切换 FE_TONEAREST/UPWARD/
// DOWNWARD 舍入模式，验证 double/float 除法与 1+2^-53 这类半 ULP 边界，
// 并探测 long double 扩展尾数（x87 80-bit）与 IEEE-754 位模式。
// 所有出参都是标量，C 不保留 Go 指针。
package main

/*
#cgo LDFLAGS: -lm
#include <stdint.h>
#include "mp.h"
*/
import "C"

import (
	"fmt"
	"math"
	"os"
	"runtime"
	"sync/atomic"
	"unsafe"
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

func main() {
	const cname = "l09_cgo_math_precision"

	ck(int32(C.mp_dbl_mant()) == 53, "DBL_MANT_DIG=53")
	ck(int32(C.mp_flt_mant()) == 24, "FLT_MANT_DIG=24")
	ck(int32(C.mp_ldbl_mant()) >= 64, "long double mantissa beyond double")
	ck(int32(C.mp_ldbl_size()) >= 10, "long double size")
	ck(unsafe.Sizeof(C.double(0)) == 8 && unsafe.Sizeof(C.float(0)) == 4, "Go double/float size")

	// 默认舍入模式为最近偶数。
	ck(int32(C.mp_get_round()) == int32(C.mp_round_nearest()), "default rounding is nearest")

	// 1 + 2^-53 恰为 1.0 与下一位的中点。
	var half = math.Ldexp(1, -53)
	var upper = 1 + math.Ldexp(1, -52)

	ck(int32(C.mp_set_round(C.mp_round_up())) == 0, "set FE_UPWARD")
	ck(int32(C.mp_get_round()) == int32(C.mp_round_up()), "get FE_UPWARD")
	ck(float64(C.mp_add(1.0, C.double(half))) == upper, "upward: 1+2^-53 rounds up")
	ck(float64(C.mp_add(-1.0, C.double(-half))) == -1.0, "upward: -1-2^-53 stays -1")

	ck(int32(C.mp_set_round(C.mp_round_down())) == 0, "set FE_DOWNWARD")
	ck(int32(C.mp_get_round()) == int32(C.mp_round_down()), "get FE_DOWNWARD")
	ck(float64(C.mp_add(1.0, C.double(half))) == 1.0, "downward: 1+2^-53 rounds down")
	ck(float64(C.mp_add(-1.0, C.double(-half))) == -upper, "downward: -1-2^-53 rounds away")

	ck(int32(C.mp_set_round(C.mp_round_nearest())) == 0, "restore FE_TONEAREST")
	ck(float64(C.mp_add(1.0, C.double(half))) == 1.0, "nearest: tie to even")
	ck(float64(C.mp_add(-1.0, C.double(-half))) == -1.0, "nearest: negative tie to even")

	// 1/3 的精确值位于相邻两个 double 之间：向上/向下各取一侧。
	baseDiv := float64(C.mp_div(1.0, 3.0))
	ck(int32(C.mp_set_round(C.mp_round_up())) == 0, "set upward for div")
	upDiv := float64(C.mp_div(1.0, 3.0))
	ck(upDiv == math.Nextafter(baseDiv, math.Inf(1)), "upward division is next double")
	ck(int32(C.mp_set_round(C.mp_round_down())) == 0, "set downward for div")
	downDiv := float64(C.mp_div(1.0, 3.0))
	ck(downDiv == baseDiv, "downward division matches nearest")
	ck(float64(C.mp_mul(C.double(downDiv), 3.0)) < 1.0, "rounded 1/3 times 3 below 1")
	ck(int32(C.mp_set_round(C.mp_round_nearest())) == 0, "restore nearest after div")
	ck(float64(C.mp_div(1.0, 3.0)) == baseDiv, "nearest division stable")

	// IEEE-754 位模式（linux/amd64 小端、IEEE 双精度/单精度）。
	ck(uint32(C.mp_float_bits(C.float(0.1))) == 0x3dcccccd, "float32 bits of 0.1")
	ck(uint64(C.mp_double_bits(0.1)) == 0x3fb999999999999a, "float64 bits of 0.1")
	ck(uint32(C.mp_float_div_bits(1.0, 3.0)) == 0x3eaaaaab, "float32 1/3 bits")
	ck(uint64(C.mp_double_div_bits(1.0, 3.0)) == 0x3fd5555555555555, "float64 1/3 bits")

	// long double 扩展精度探针：double 会丢失 1e-19。
	ldProbe := float64(C.mp_ld_probe())
	ck(ldProbe > 5e-20 && ldProbe < 2e-19, "long double retains 1e-19 scale")
	ldEps := float64(C.mp_ld_epsilon())
	ck(ldEps > 0 && ldEps < 1e-18, "long double epsilon range")
	ck(ldEps <= math.Ldexp(1, -63)*1.0000001, "long double epsilon at x87 precision")

	// 显式 GC 后浮点环境与位模式不变（关闭/恢复模式均再验证）。
	gcStress()
	ck(int32(C.mp_get_round()) == int32(C.mp_round_nearest()), "rounding mode stable across GC")
	ck(uint64(C.mp_double_div_bits(1.0, 3.0)) == 0x3fd5555555555555, "bits stable across GC")
	ck(math.Abs(float64(C.mp_ld_probe())-ldProbe) <= 1e-25, "long double probe stable across GC")

	finish(cname)
}
