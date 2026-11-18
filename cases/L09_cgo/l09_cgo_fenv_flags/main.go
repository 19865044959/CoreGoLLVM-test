// L09: C 浮点环境异常标志边界：用 C 辅助函数在锁定的 OS 线程上
// feclearexcept/fetestexcept/feraiseexcept/fesetround/fegetenv/fesetenv，
// 覆盖 FE_INVALID/FE_DIVBYZERO/FE_OVERFLOW/FE_UNDERFLOW/FE_INEXACT 的
// 触发与累积、四种舍入模式对 1/3 的方向、环境保存/恢复、FE_DFL_ENV
// 复位以及 GC 期间标志稳定性。所有 FP 运算在 volatile C 内存中完成，
// C 不保留 Go 指针。
package main

/*
#cgo LDFLAGS: -lm
#include "fe.h"
*/
import "C"

import (
	"fmt"
	"math"
	"os"
	"runtime"
	"sync/atomic"
)

const caseName = "l09_cgo_fenv_flags"

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

func calc(op int, a, b float64) float64 {
	return float64(C.fe_calc(C.int(op), C.double(a), C.double(b)))
}

func test(mask int32) int32 { return int32(C.fe_test(C.int(mask))) }

func main() {
	// fenv 是线程局部状态；锁定 OS 线程后跨 cgo 调用读到的才是同一个环境。
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	invalid := int32(C.fe_mask_invalid())
	divzero := int32(C.fe_mask_divbyzero())
	overflow := int32(C.fe_mask_overflow())
	underflow := int32(C.fe_mask_underflow())
	inexact := int32(C.fe_mask_inexact())
	all := int32(C.fe_mask_all())
	nearest := int32(C.fe_round_nearest())
	up := int32(C.fe_round_up())
	down := int32(C.fe_round_down())
	toward := int32(C.fe_round_zero())

	ck(all == (invalid|divzero|overflow|underflow|inexact), "FE_ALL_EXCEPT composition")
	ck(invalid != 0 && divzero != 0 && overflow != 0 && underflow != 0 && inexact != 0, "exception masks nonzero")

	ck(int32(C.fe_get_round()) == nearest, "default FE_TONEAREST")
	ck(int32(C.fe_clear()) == 0, "feclearexcept")
	ck(test(all) == 0, "flags clear after feclearexcept")

	// 除零：只有 DIVBYZERO，结果 +Inf。
	C.fe_clear()
	v := calc(0, 1.0, 0.0)
	ck(test(all) == divzero, "1/0 flags == DIVBYZERO")
	ck(math.IsInf(v, 1), "1/0 == +Inf")

	// 0/0 与 sqrt(-1)：INVALID，结果为 NaN。
	C.fe_clear()
	v = calc(0, 0.0, 0.0)
	ck(test(all) == invalid, "0/0 flags == INVALID")
	ck(math.IsNaN(v), "0/0 is NaN")
	C.fe_clear()
	v = calc(1, -1.0, 0.0)
	ck(test(all) == invalid, "sqrt(-1) flags == INVALID")
	ck(math.IsNaN(v), "sqrt(-1) is NaN")

	// 溢出：OVERFLOW|INEXACT（IEEE 规定伴随 inexact）。
	C.fe_clear()
	v = calc(2, float64(C.fe_dbl_max()), 2.0)
	f := test(all)
	ck(f&overflow != 0, "DBL_MAX*2 raises OVERFLOW")
	ck(f&inexact != 0, "DBL_MAX*2 also raises INEXACT")
	ck(math.IsInf(v, 1), "DBL_MAX*2 == +Inf")

	// 下溢：最小次正规数 / 2 → 0，UNDERFLOW|INEXACT。
	C.fe_clear()
	v = calc(0, float64(C.fe_true_min()), 2.0)
	f = test(all)
	ck(f&underflow != 0, "true_min/2 raises UNDERFLOW")
	ck(f&inexact != 0, "true_min/2 also raises INEXACT")
	ck(v == 0.0, "true_min/2 rounds to zero")

	// 仅不精确：1/3。
	C.fe_clear()
	v = calc(0, 1.0, 3.0)
	ck(test(all) == inexact, "1/3 flags == INEXACT")
	ck(v == 1.0/3.0, "1/3 default rounding value")

	// 精确整数运算不置标志。
	C.fe_clear()
	_ = calc(3, 1.0, 1.0)
	_ = calc(2, 3.0, 7.0)
	ck(test(all) == 0, "exact integer ops raise no flags")

	// 标志累积：两次 inexact + 一次 divzero。
	C.fe_clear()
	_ = calc(0, 1.0, 3.0)
	_ = calc(0, 2.0, 3.0)
	_ = calc(0, 1.0, 0.0)
	ck(test(all) == (inexact|divzero), "flags accumulate")

	// feraiseexcept 显式置位。
	C.fe_clear()
	ck(int32(C.fe_raise(C.int(invalid|overflow))) == 0, "feraiseexcept")
	ck(test(all) == (invalid|overflow), "raised flags exact")

	// 四种舍入模式设置/读取，并影响 1/3 的舍入方向。
	exactThird := 1.0 / 3.0
	for _, m := range []int32{nearest, up, down, toward} {
		ck(int32(C.fe_set_round(C.int(m))) == 0, fmt.Sprintf("fesetround(%d)", m))
		ck(int32(C.fe_get_round()) == m, fmt.Sprintf("fegetround(%d)", m))
	}
	C.fe_clear()
	C.fe_set_round(C.int(up))
	v = calc(0, 1.0, 3.0)
	ck(v == math.Nextafter(exactThird, math.Inf(1)), "FE_UPWARD 1/3 rounds up")
	C.fe_clear()
	C.fe_set_round(C.int(down))
	v = calc(0, 1.0, 3.0)
	// nearest 的 1/3 双精度值略小于精确 1/3，向下舍入保持该值。
	ck(v == exactThird, "FE_DOWNWARD 1/3 stays at nearest value below exact")
	C.fe_set_round(C.int(nearest))

	// 保存/恢复：舍入模式与异常标志一起恢复。
	C.fe_default()
	C.fe_clear()
	C.fe_set_round(C.int(up))
	C.fe_raise(C.int(inexact))
	C.fe_save()
	C.fe_set_round(C.int(down))
	C.fe_clear()
	C.fe_raise(C.int(overflow))
	C.fe_restore()
	ck(int32(C.fe_get_round()) == up, "fesetenv restores rounding mode")
	ck(test(all) == inexact, "fesetenv restores flags")

	// FE_DFL_ENV：默认舍入模式、无异常标志。
	C.fe_default()
	ck(int32(C.fe_get_round()) == nearest, "FE_DFL_ENV rounding")
	ck(test(all) == 0, "FE_DFL_ENV flags clear")

	// Go 运行时自身会做浮点运算并扰动异常标志；显式 GC 后重新清零，
	// 标志状态必须重新变得可控（这是绑定层必须遵守的边界）。
	gcStress()
	ck(int32(C.fe_clear()) == 0, "feclearexcept after GC")
	ck(test(all) == 0, "flags clear after GC")
	ck(int32(C.fe_raise(C.int(divzero))) == 0, "feraiseexcept after GC")
	ck(test(all) == divzero, "flags controllable after GC")

	// 重复触发 100 轮，周期性 GC。
	for i := 0; i < 100; i++ {
		C.fe_clear()
		_ = calc(0, 1.0, 3.0)
		ck(test(all) == inexact, fmt.Sprintf("iteration %d: INEXACT", i))
		if i%10 == 0 {
			gcStress()
		}
	}

	C.fe_default()
	finish(caseName)
}
