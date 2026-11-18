// L11: 常量除法/取模——常量除数（魔数乘法优化）与运行期除数结果逐项一致；
// 有符号截断朝向零、余数符号随被除数、最小整数除 -1 回绕、无符号与窄宽度同理。
package main

import (
	"fmt"
	"os"
	"runtime"
	"strconv"
	"sync/atomic"
)

var (
	failed int32
	gcSink [][]byte
	seed   uint64 = 1
)

func ck(ok bool, msg string) {
	if !ok {
		atomic.AddInt32(&failed, 1)
		fmt.Printf("FAIL: %s (UAF_SEED=%d)\n", msg, seed)
	}
}

func gcStress() {
	for i := 0; i < 16; i++ {
		b := make([]byte, 4096)
		b[0] = byte(i)
		b[len(b)-1] = byte(255 - i)
		gcSink = append(gcSink[:0], b)
	}
	gcSink = gcSink[:0]
	runtime.GC()
	runtime.GC()
}

func loadSeed() uint64 {
	s := os.Getenv("UAF_SEED")
	if s == "" {
		return 1
	}
	n, err := strconv.ParseUint(s, 10, 64)
	if err != nil {
		return 1
	}
	return n
}

func finish(name string) {
	if n := atomic.LoadInt32(&failed); n != 0 {
		fmt.Printf("FAILED %s (%d failed checks, seed=%d)\n", name, n, seed)
		os.Exit(1)
	}
	fmt.Println("ALL PASS " + name)
}

//go:noinline
func refDiv(x, d int64) int64 { return x / d }

//go:noinline
func refMod(x, d int64) int64 { return x % d }

//go:noinline
func refUDiv(x, d uint64) uint64 { return x / d }

//go:noinline
func refUMod(x, d uint64) uint64 { return x % d }

//go:noinline
func refDiv8(x, d int8) int8 { return x / d }

//go:noinline
func refMod8(x, d int8) int8 { return x % d }

//go:noinline
func refDiv32(x, d int32) int32 { return x / d }

//go:noinline
func refMod32(x, d int32) int32 { return x % d }

type divCase struct {
	name string
	d    int64
	div  func(int64) int64
	mod  func(int64) int64
}

func divCases() []divCase {
	return []divCase{
		{"1", 1, func(x int64) int64 { return x / 1 }, func(x int64) int64 { return x % 1 }},
		{"2", 2, func(x int64) int64 { return x / 2 }, func(x int64) int64 { return x % 2 }},
		{"3", 3, func(x int64) int64 { return x / 3 }, func(x int64) int64 { return x % 3 }},
		{"5", 5, func(x int64) int64 { return x / 5 }, func(x int64) int64 { return x % 5 }},
		{"7", 7, func(x int64) int64 { return x / 7 }, func(x int64) int64 { return x % 7 }},
		{"10", 10, func(x int64) int64 { return x / 10 }, func(x int64) int64 { return x % 10 }},
		{"16", 16, func(x int64) int64 { return x / 16 }, func(x int64) int64 { return x % 16 }},
		{"255", 255, func(x int64) int64 { return x / 255 }, func(x int64) int64 { return x % 255 }},
		{"1024", 1024, func(x int64) int64 { return x / 1024 }, func(x int64) int64 { return x % 1024 }},
		{"-3", -3, func(x int64) int64 { return x / -3 }, func(x int64) int64 { return x % -3 }},
		{"-7", -7, func(x int64) int64 { return x / -7 }, func(x int64) int64 { return x % -7 }},
		{"-16", -16, func(x int64) int64 { return x / -16 }, func(x int64) int64 { return x % -16 }},
		{"-1", -1, func(x int64) int64 { return x / -1 }, func(x int64) int64 { return x % -1 }},
	}
}

func div8_3(x int8) int8     { return x / 3 }
func mod8_3(x int8) int8     { return x % 3 }
func div8_N7(x int8) int8    { return x / -7 }
func mod8_N7(x int8) int8    { return x % -7 }
func div32_10(x int32) int32 { return x / 10 }
func mod32_10(x int32) int32 { return x % 10 }

func expectDivZero(name string, fn func()) {
	defer func() {
		r := recover()
		if r == nil {
			ck(false, name+": expected divide-by-zero panic")
			return
		}
		if _, ok := r.(runtime.Error); !ok {
			ck(false, name+": panic is not runtime.Error")
			return
		}
		if !contains(fmt.Sprint(r), "divide by zero") {
			ck(false, name+": unexpected panic text: "+fmt.Sprint(r))
		}
	}()
	fn()
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

func nextRand(x *uint64) uint64 {
	*x ^= *x << 13
	*x ^= *x >> 7
	*x ^= *x << 17
	return *x
}

func main() {
	seed = loadSeed()
	if seed == 0 {
		seed = 1
	}

	minI64 := int64(-1 << 63)
	maxI64 := int64(1<<63 - 1)
	negOne := int64(-1)
	minI8 := int8(-128)
	negOne8 := int8(-1)

	ck(minI64/negOne == minI64, "MinInt64 / -1 wraps to MinInt64")
	ck(minI64%negOne == 0, "MinInt64 % -1 == 0")
	ck(minI8/negOne8 == minI8, "MinInt8 / -1 wraps to MinInt8")
	ck(minI8%negOne8 == 0, "MinInt8 % -1 == 0")
	ck(maxI64/1 == maxI64 && maxI64%1 == 0, "MaxInt64 identity")

	values := []int64{
		0, 1, -1, 2, -2, 3, -3, 5, -5, 7, -7, 10, -10, 16, -16,
		255, -255, 1024, -1024, 1 << 31, -(1 << 31), 1 << 62, -(1 << 62),
		maxI64, minI64, maxI64 - 1, minI64 + 1,
	}

	cases := divCases()
	for _, tc := range cases {
		for _, x := range values {
			q := tc.div(x)
			r := tc.mod(x)
			ck(q == refDiv(x, tc.d), "const division "+tc.name)
			ck(r == refMod(x, tc.d), "const modulo "+tc.name)
			if !(tc.d == -1 && x == minI64) {
				ck(q*tc.d+r == x, "q*d+r identity "+tc.name)
			}
		}
	}

	// 符号规则。
	ck(refDiv(-7, 3) == -2 && refMod(-7, 3) == -1, "negative dividend division")
	ck(refDiv(7, -3) == -2 && refMod(7, -3) == 1, "negative divisor modulo")
	ck(refDiv(-8, 4) == -2 && refMod(-8, 4) == 0, "exact negative division")

	// 无符号：2 的幂与非 2 的幂共存。
	uvals := []uint64{0, 1, 2, 3, 5, 7, 10, 255, 256, 1023, 1 << 31, 1 << 32, 1 << 63, ^uint64(0)}
	udivs := []struct {
		d   uint64
		div func(uint64) uint64
		mod func(uint64) uint64
	}{
		{2, func(x uint64) uint64 { return x / 2 }, func(x uint64) uint64 { return x % 2 }},
		{3, func(x uint64) uint64 { return x / 3 }, func(x uint64) uint64 { return x % 3 }},
		{5, func(x uint64) uint64 { return x / 5 }, func(x uint64) uint64 { return x % 5 }},
		{10, func(x uint64) uint64 { return x / 10 }, func(x uint64) uint64 { return x % 10 }},
		{16, func(x uint64) uint64 { return x / 16 }, func(x uint64) uint64 { return x % 16 }},
		{255, func(x uint64) uint64 { return x / 255 }, func(x uint64) uint64 { return x % 255 }},
		{1000, func(x uint64) uint64 { return x / 1000 }, func(x uint64) uint64 { return x % 1000 }},
	}
	for _, tc := range udivs {
		for _, x := range uvals {
			q := tc.div(x)
			r := tc.mod(x)
			ck(q == refUDiv(x, tc.d) && r == refUMod(x, tc.d), "unsigned const division")
			ck(q*tc.d+r == x, "unsigned q*d+r identity")
		}
	}

	// 窄宽度：符号扩展/截断必须正确。
	for i := -128; i <= 127; i++ {
		x := int8(i)
		ck(div8_3(x) == refDiv8(x, 3), "int8 /3")
		ck(mod8_3(x) == refMod8(x, 3), "int8 %3")
		ck(div8_N7(x) == refDiv8(x, -7), "int8 /-7")
		ck(mod8_N7(x) == refMod8(x, -7), "int8 %-7")
	}
	for _, x := range []int32{0, 1, -1, 9, 10, -10, 123456789, -123456789, 1<<31 - 1, -1 << 31} {
		ck(div32_10(x) == refDiv32(x, 10), "int32 /10")
		ck(mod32_10(x) == refMod32(x, 10), "int32 %10")
		ck(div32_10(x)*10+mod32_10(x) == x, "int32 q*10+r identity")
	}

	// 随机值：常量除数 vs 运行期除数，并跨 GC 复核。
	rng := seed
	const rn = 128
	randVals := make([]int64, rn)
	storedQ := make([]int64, rn)
	storedR := make([]int64, rn)
	for i := 0; i < rn; i++ {
		v := int64(nextRand(&rng))
		randVals[i] = v
		storedQ[i] = cases[i%len(cases)].div(v)
		storedR[i] = cases[i%len(cases)].mod(v)
	}
	gcStress()
	for i := 0; i < rn; i++ {
		tc := cases[i%len(cases)]
		ck(tc.div(randVals[i]) == storedQ[i] && storedQ[i] == refDiv(randVals[i], tc.d),
			"random const division after GC")
		ck(tc.mod(randVals[i]) == storedR[i] && storedR[i] == refMod(randVals[i], tc.d),
			"random const modulo after GC")
	}

	zero := int64(0)
	one := int64(1)
	expectDivZero("division by zero", func() { _ = one / zero })
	expectDivZero("modulo by zero", func() { _ = one % zero })
	uzero := uint64(0)
	expectDivZero("unsigned division by zero", func() { _ = uint64(1) / uzero })

	finish("l11_const_divmod")
}
