// L01: 约束边界——ordered/integer 约束在 int8/uint8/int16/uint64/float/string
// 极值、零值、溢出回绕与自定义底层类型上的行为；GC 在关键位置。
package main

import (
	"fmt"
	"math"
	"os"
	"runtime"
	"slices"
	"strconv"
	"sync/atomic"
)

type Signed interface {
	~int8 | ~int16 | ~int32 | ~int64
}

type Unsigned interface {
	~uint8 | ~uint16 | ~uint32 | ~uint64
}

type Integer interface {
	Signed | Unsigned
}

type Ordered interface {
	Integer | ~float32 | ~float64 | ~string
}

// clamp 把 v 限制在 [lo, hi]。NaN 与任何值比较均为 false，因此原样返回。
func clamp[T Ordered](v, lo, hi T) T {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// minmax 返回 (min, max)，-0.0/+0.0 时保留入参的位模式。
func minmax[T Ordered](a, b T) (T, T) {
	if b < a {
		return b, a
	}
	return a, b
}

func addWrap[T Integer](a, b T) T { return a + b }

func sumWrap[T Integer](xs []T) T {
	var s T
	for _, x := range xs {
		s += x
	}
	return s
}

func zeroOf[T any]() T {
	var z T
	return z
}

type celsius int16

type level uint8

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

func main() {
	uafSeed = envSeed()

	// int8：上下界、零值、溢出回绕。
	ck(clamp[int8](math.MaxInt8, -10, 100) == 100, "int8 clamp above hi")
	ck(clamp[int8](math.MinInt8, -10, 100) == -10, "int8 clamp below lo")
	ck(clamp[int8](50, 50, 50) == 50, "int8 clamp degenerate bounds")
	ck(clamp[int8](0, math.MinInt8, math.MaxInt8) == 0, "int8 clamp full range")
	lo8, hi8 := minmax[int8](math.MaxInt8, math.MinInt8)
	ck(lo8 == math.MinInt8 && hi8 == math.MaxInt8, "int8 minmax at extremes")
	ck(addWrap[int8](math.MaxInt8, 1) == math.MinInt8, "int8 max+1 must wrap to min")
	ck(addWrap[int8](math.MinInt8, -1) == math.MaxInt8, "int8 min-1 must wrap to max")
	ck(sumWrap([]int8{math.MaxInt8, 1}) == math.MinInt8, "int8 slice sum wrap")
	ck(sumWrap([]int8{100, 27, 1}) == math.MinInt8, "int8 multi-term wrap")
	ck(zeroOf[int8]() == 0, "int8 zero value")

	// uint8：255+1 回绕到 0。
	ck(addWrap[uint8](math.MaxUint8, 1) == 0, "uint8 max+1 must wrap to zero")
	ck(sumWrap([]uint8{250, 5, 1}) == 0, "uint8 multi-term wrap")
	ck(clamp[uint8](0, 10, 20) == 10, "uint8 clamp below lo")
	ck(clamp[uint8](math.MaxUint8, 10, 20) == 20, "uint8 clamp above hi")

	// uint64 / int64：64 位边界。
	ck(addWrap[uint64](math.MaxUint64, 1) == 0, "uint64 max+1 must wrap to zero")
	ck(addWrap[uint64](0, math.MaxUint64) == math.MaxUint64, "uint64 zero add")
	lo64, hi64 := minmax[uint64](math.MaxUint64, 0)
	ck(lo64 == 0 && hi64 == math.MaxUint64, "uint64 minmax at extremes")
	ck(addWrap[int64](math.MaxInt64, 1) == math.MinInt64, "int64 max+1 must wrap to min")
	ck(addWrap[int64](math.MinInt64, -1) == math.MaxInt64, "int64 min-1 must wrap to max")
	ck(sumWrap([]int64{math.MaxInt64, 0}) == math.MaxInt64, "int64 sum identity")

	// float64：无穷、NaN、正负零。
	ck(clamp[float64](math.Inf(1), 0, 1) == 1, "float inf clamp above hi")
	ck(clamp[float64](math.Inf(-1), 0, 1) == 0, "float -inf clamp below lo")
	ck(math.IsNaN(clamp[float64](math.NaN(), 0, 1)), "NaN must pass through clamp")
	fmin, fmax := minmax[float64](math.Copysign(0, -1), 0)
	ck(math.Signbit(fmin) && !math.Signbit(fmax), "float signed zero minmax")
	ck(clamp[float64](1.5, -2.5, 2.5) == 1.5, "float in-range clamp")

	// string：空串/边界字符。
	ck(clamp[string]("", "a", "z") == "a", "string clamp below lo")
	ck(clamp[string]("zz", "a", "z") == "z", "string clamp above hi")
	ck(clamp[string]("m", "a", "z") == "m", "string clamp in range")
	slo, shi := minmax[string]("b", "a")
	ck(slo == "a" && shi == "b", "string minmax order")
	slo, shi = minmax[string]("", "\xff")
	ck(slo == "" && shi == "\xff", "string minmax empty/max byte")

	// 自定义底层类型经 ~ 约束实例化。
	ck(addWrap(celsius(math.MaxInt16), celsius(1)) == celsius(math.MinInt16), "celsius wrap")
	ck(addWrap(level(math.MaxUint8), level(1)) == level(0), "level wrap")
	ck(clamp(celsius(50), celsius(0), celsius(100)) == celsius(50), "celsius clamp")
	ck(clamp(level(255), level(10), level(20)) == level(20), "level clamp")
	ck(zeroOf[celsius]() == 0, "custom type zero value")

	// stdlib 泛型排序/二分在类型边界上的一致性。
	i8 := []int8{math.MaxInt8, math.MinInt8, 0, -1, 1}
	slices.Sort(i8)
	ck(slices.IsSorted(i8), "int8 slice not sorted")
	ck(i8[0] == math.MinInt8 && i8[len(i8)-1] == math.MaxInt8, "int8 sorted extremes")
	pos, found := slices.BinarySearch(i8, int8(math.MinInt8))
	ck(found && pos >= 0 && i8[pos] == math.MinInt8, "int8 binary search min")
	_, found = slices.BinarySearch(i8, int8(126))
	ck(!found, "int8 binary search must miss 126")

	u8 := []uint8{math.MaxUint8, 0, 128, 1}
	slices.Sort(u8)
	ck(u8[0] == 0 && u8[len(u8)-1] == math.MaxUint8, "uint8 sorted extremes")
	u64 := []uint64{math.MaxUint64, 0, 1 << 63, 1<<63 - 1}
	slices.Sort(u64)
	ck(u64[0] == 0 && u64[len(u64)-1] == math.MaxUint64, "uint64 sorted extremes")

	// 空输入的边界：零值/空结果。
	var noI8 []int8
	ck(sumWrap(noI8) == 0, "empty int8 sum must be zero")
	ck(slices.IsSorted(noI8), "empty slice must be sorted")
	_, found = slices.BinarySearch(noI8, int8(1))
	ck(!found, "empty binary search must miss")

	// 边界值指针在 GC 后的内容。
	ptrs := make([]*int8, 256)
	for i := range ptrs {
		var v int8
		if i%2 == 0 {
			v = math.MaxInt8
		} else {
			v = math.MinInt8
		}
		ptrs[i] = &v
	}
	gcStress()
	for i, p := range ptrs {
		if i%2 == 0 {
			ck(p != nil && *p == math.MaxInt8, "max int8 pointer corrupted after GC")
		} else {
			ck(p != nil && *p == math.MinInt8, "min int8 pointer corrupted after GC")
		}
	}

	// 种子只影响重复次数；每次重复都走相同的边界表。
	rounds := 50 + int(uafSeed%10)
	var acc int64
	for r := 0; r < rounds; r++ {
		acc += int64(addWrap[int8](math.MaxInt8, 1))
		acc += int64(addWrap[uint8](math.MaxUint8, 1))
		acc += int64(clamp[int16](math.MaxInt16, -5, 5))
	}
	ck(acc == int64(rounds)*(-128+0+5), "boundary table accumulation mismatch")

	finish("l01_generic_const_bounds")
}
