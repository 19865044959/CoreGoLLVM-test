// L01: 泛型算法边界——空输入、单元素、全相等、极值与 2 万元素规模下
// fold/reduce/minmax/partition/chunk/search/unique/window 的行为；GC 后复算。
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

type Ordered interface {
	~int8 | ~int16 | ~int32 | ~int64 |
		~uint8 | ~uint16 | ~uint32 | ~uint64 |
		~float32 | ~float64 | ~string
}

func fold[T any, A any](xs []T, init A, f func(A, T) A) A {
	acc := init
	for _, x := range xs {
		acc = f(acc, x)
	}
	return acc
}

func reduce[T any](xs []T, f func(T, T) T) (T, bool) {
	var z T
	if len(xs) == 0 {
		return z, false
	}
	acc := xs[0]
	for _, x := range xs[1:] {
		acc = f(acc, x)
	}
	return acc, true
}

func minmax[T Ordered](xs []T) (T, T, bool) {
	var z T
	if len(xs) == 0 {
		return z, z, false
	}
	mn, mx := xs[0], xs[0]
	for _, x := range xs[1:] {
		if x < mn {
			mn = x
		}
		if x > mx {
			mx = x
		}
	}
	return mn, mx, true
}

func partition[T any](xs []T, pred func(T) bool) (yes, no []T) {
	for _, x := range xs {
		if pred(x) {
			yes = append(yes, x)
		} else {
			no = append(no, x)
		}
	}
	return yes, no
}

func chunk[T any](xs []T, size int) [][]T {
	if size <= 0 || len(xs) == 0 {
		return nil
	}
	out := make([][]T, 0, (len(xs)+size-1)/size)
	for start := 0; start < len(xs); start += size {
		end := start + size
		if end > len(xs) {
			end = len(xs)
		}
		out = append(out, xs[start:end])
	}
	return out
}

func search[T Ordered](xs []T, target T) (int, bool) {
	lo, hi := 0, len(xs)
	for lo < hi {
		mid := int(uint(lo+hi) >> 1)
		if xs[mid] < target {
			lo = mid + 1
		} else {
			hi = mid
		}
	}
	if lo < len(xs) && xs[lo] == target {
		return lo, true
	}
	return lo, false
}

func uniqueCount[T comparable](xs []T) int {
	seen := make(map[T]struct{}, len(xs))
	for _, x := range xs {
		seen[x] = struct{}{}
	}
	return len(seen)
}

func windows[T any](xs []T, size int) [][]T {
	if size <= 0 || len(xs) < size {
		return nil
	}
	out := make([][]T, 0, len(xs)-size+1)
	for start := 0; start+size <= len(xs); start++ {
		out = append(out, xs[start:start+size])
	}
	return out
}

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

	// 空输入：所有算法都必须返回零值/空结果，不 panic。
	var empty []int64
	ck(fold(empty, int64(0), func(a, v int64) int64 { return a + v }) == 0, "fold empty must be init")
	rv, ok := reduce(empty, func(a, b int64) int64 { return a + b })
	ck(!ok && rv == 0, "reduce empty must report not-ok")
	mn, mx, ok := minmax(empty)
	ck(!ok && mn == 0 && mx == 0, "minmax empty must report not-ok")
	yes, no := partition(empty, func(v int64) bool { return v > 0 })
	ck(len(yes) == 0 && len(no) == 0, "partition empty must be empty")
	ck(chunk(empty, 3) == nil, "chunk empty must be nil")
	if _, found := search(empty, int64(1)); found {
		ck(false, "search empty must miss")
	}
	ck(uniqueCount(empty) == 0, "uniqueCount empty must be zero")
	ck(windows(empty, 3) == nil, "windows empty must be nil")

	// 单元素：所有聚合都返回该元素。
	one := []int64{math.MaxInt64}
	rv, ok = reduce(one, func(a, b int64) int64 { return a + b })
	ck(ok && rv == math.MaxInt64, "reduce single mismatch")
	mn, mx, ok = minmax(one)
	ck(ok && mn == math.MaxInt64 && mx == math.MaxInt64, "minmax single mismatch")
	yes, no = partition(one, func(v int64) bool { return true })
	ck(len(yes) == 1 && len(no) == 0 && yes[0] == math.MaxInt64, "partition single mismatch")
	ch1 := chunk(one, 3)
	ck(len(ch1) == 1 && len(ch1[0]) == 1 && ch1[0][0] == math.MaxInt64, "chunk single mismatch")
	pos, found := search(one, math.MaxInt64)
	ck(found && pos == 0, "search single mismatch")
	ck(uniqueCount(one) == 1, "uniqueCount single mismatch")
	w1 := windows(one, 1)
	ck(len(w1) == 1 && len(w1[0]) == 1, "windows single mismatch")
	ck(windows(one, 2) == nil, "windows larger than input must be nil")
	ck(chunk(one, 0) == nil, "chunk size 0 must be nil")

	// 全相等：min==max，去重后为 1。
	eq := make([]int8, 777)
	for i := range eq {
		eq[i] = 42
	}
	mn8, mx8, ok8 := minmax(eq)
	ck(ok8 && mn8 == 42 && mx8 == 42, "minmax all-equal mismatch")
	ck(uniqueCount(eq) == 1, "uniqueCount all-equal mismatch")
	ey, en := partition(eq, func(v int8) bool { return v%2 == 0 })
	ck(len(ey) == 777 && len(en) == 0, "partition all-equal mismatch")
	ech := chunk(eq, 100)
	ck(len(ech) == 8 && len(ech[len(ech)-1]) == 77, "chunk all-equal tail mismatch")

	// 排序类型边界。
	i8 := []int8{math.MaxInt8, math.MinInt8, 0, -1, 1}
	slices.Sort(i8)
	ck(slices.IsSorted(i8), "int8 sort failed")
	ck(i8[0] == math.MinInt8 && i8[len(i8)-1] == math.MaxInt8, "int8 sort extremes mismatch")
	pos, found = search(i8, int8(math.MaxInt8))
	ck(found && i8[pos] == math.MaxInt8, "int8 search max mismatch")
	if _, found = search(i8, int8(126)); found {
		ck(false, "int8 search must miss 126")
	}

	u64 := []uint64{math.MaxUint64, 0, 1 << 63, 1<<63 - 1}
	slices.Sort(u64)
	pos, found = search(u64, math.MaxUint64)
	ck(found && u64[pos] == math.MaxUint64, "uint64 search max mismatch")
	if _, found = search(u64, math.MaxUint64-1); found {
		ck(false, "uint64 search must miss max-1")
	}

	strs := []string{"", "a", "zz", "\xff"}
	slices.Sort(strs)
	smin, smax, sok := minmax(strs)
	ck(sok && smin == "" && smax == "\xff", "string minmax mismatch")
	pos, found = search(strs, "\xff")
	ck(found && strs[pos] == "\xff", "string search max byte mismatch")
	if _, found = search(strs, "b"); found {
		ck(false, "string search must miss b")
	}

	// 大量元素：排序后逐项复算，GC 后结果不变。
	n := 20000 + int(uafSeed%1000)
	data := make([]int64, n)
	var preSum int64
	for i := 0; i < n; i++ {
		v := int64((i*37)%10007) - 5000
		data[i] = v
		preSum += v
	}
	slices.Sort(data)
	gcStress()
	ck(slices.IsSorted(data), "large slice not sorted")
	ck(fold(data, int64(0), func(a, v int64) int64 { return a + v }) == preSum,
		"large fold sum mismatch after GC")
	mn, mx, ok = minmax(data)
	ck(ok && mn == data[0] && mx == data[n-1], "large minmax mismatch")
	pos, found = search(data, data[n/2])
	ck(found && data[pos] == data[n/2], "large search present value mismatch")
	if _, found = search(data, math.MinInt64); found {
		ck(false, "large search must miss math.MinInt64")
	}
	if _, found = search(data, 999999); found {
		ck(false, "large search must miss out-of-range value")
	}

	wantEven := 0
	for _, v := range data {
		if v%2 == 0 {
			wantEven++
		}
	}
	ly, ln := partition(data, func(v int64) bool { return v%2 == 0 })
	ck(len(ly) == wantEven && len(ln) == n-wantEven, "large partition count mismatch")
	var partSum int64
	for _, v := range ly {
		partSum += v
	}
	for _, v := range ln {
		partSum += v
	}
	ck(partSum == preSum, "large partition lost elements")

	chunks := chunk(data, 1000)
	ck(len(chunks) == (n+999)/1000, "large chunk count mismatch")
	totalChunk := 0
	for i, c := range chunks {
		wantLen := 1000
		if i == len(chunks)-1 {
			wantLen = n - (len(chunks)-1)*1000
		}
		ck(len(c) == wantLen, "large chunk length mismatch")
		totalChunk += len(c)
	}
	ck(totalChunk == n, "large chunk lost elements")

	wins := windows(data, 8)
	ck(len(wins) == n-7, "large window count mismatch")
	ck(len(wins[0]) == 8 && wins[0][0] == data[0] && wins[0][7] == data[7], "first window mismatch")
	last := wins[len(wins)-1]
	ck(len(last) == 8 && last[0] == data[n-8] && last[7] == data[n-1], "last window mismatch")

	refUnique := 0
	for i := 0; i < n; i++ {
		if i == 0 || data[i] != data[i-1] {
			refUnique++
		}
	}
	ck(uniqueCount(data) == refUnique, "large unique count mismatch")
	ck(fold(data, int64(0), func(a, v int64) int64 { return a + v }) == preSum,
		"large fold changed after algorithm passes")

	finish("l01_generic_alg_edges")
}
