// L11: 多索引边界检查消除——相邻下标、反向配对、三元窗口、同一切片的
// 双索引与平移拷贝结果必须与独立参考/闭式公式一致；越界仍 panic，跨 GC 不变。
package main

import (
	"fmt"
	"os"
	"runtime"
	"strconv"
	"strings"
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

func tortureMultiplier() int {
	s := os.Getenv("TORTURE_ROUNDS")
	if s == "" {
		return 1
	}
	n, err := strconv.Atoi(s)
	if err != nil || n <= 0 {
		return 1
	}
	if n > 50 {
		n = 50
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

func makeVec(n int, salt int64) []int64 {
	v := make([]int64, n)
	for i := range v {
		v[i] = (int64(i)*13+salt)%1009 - 504
	}
	return v
}

func sumAll(s []int64) int64 {
	var sum int64
	for _, v := range s {
		sum += v
	}
	return sum
}

func mixAll(s []int64) int64 {
	h := int64(17)
	for _, v := range s {
		h = h*31 + v
	}
	return h
}

// pairAdjacent 在一次循环里访问 a[i] 与 b[i+1]。
func pairAdjacent(a, b []int64) int64 {
	var sum int64
	for i := 0; i < len(a)-1; i++ {
		sum += a[i]*2 + b[i+1]
	}
	return sum
}

func pairAdjacentRef(a, b []int64) int64 {
	var sum int64
	for i := 1; i < len(a); i++ {
		sum += a[i-1]*2 + b[i]
	}
	return sum
}

// reversePair 正向索引 a、反向索引 b。
func reversePair(a, b []int64) int64 {
	n := len(a)
	var sum int64
	for i := 0; i < n; i++ {
		sum += a[i]*3 - b[n-1-i]
	}
	return sum
}

func reversePairRef(a, b []int64) int64 {
	var sum int64
	j := len(b) - 1
	for i := 0; i < len(a); i++ {
		sum += a[i]*3 - b[j-i]
	}
	return sum
}

// adjacentTriple 每轮读 s[i-1]、s[i]、s[i+1]。
func adjacentTriple(s []int64) int64 {
	var sum int64
	for i := 1; i+1 < len(s); i++ {
		sum += s[i-1]*3 + s[i]*5 + s[i+1]*7
	}
	return sum
}

func adjacentTripleRef(s []int64) int64 {
	var sum int64
	for i := 0; i+2 < len(s); i++ {
		sum += s[i]*3 + s[i+1]*5 + s[i+2]*7
	}
	return sum
}

// strided4 每轮连续性读四个下标。
func strided4(s []int64) int64 {
	var sum int64
	for i := 0; i+3 < len(s); i += 4 {
		sum += s[i] + s[i+1]*2 + s[i+2]*3 + s[i+3]*4
	}
	return sum
}

func strided4Ref(s []int64) int64 {
	var sum int64
	for i := 0; i < len(s); i++ {
		switch i & 3 {
		case 0:
			sum += s[i]
		case 1:
			sum += s[i] * 2
		case 2:
			sum += s[i] * 3
		case 3:
			sum += s[i] * 4
		}
	}
	return sum
}

// sameSlicePair 同一切片双索引。
func sameSlicePair(s []int64, k int) int64 {
	n := len(s)
	var sum int64
	for i := 0; i < n; i++ {
		j := (i*k + 7) % n
		sum += s[i] ^ s[j]
	}
	return sum
}

func sameSlicePairRef(s []int64, k int) int64 {
	n := len(s)
	var sum int64
	j := 7 % n
	for i := 0; i < n; i++ {
		sum += s[i] ^ s[j]
		j = (j + k) % n
	}
	return sum
}

// windowEdges 访问窗口首尾两个下标。
func windowEdges(s []int64, w int) int64 {
	var sum int64
	for i := 0; i+w <= len(s); i++ {
		sum += s[i] * s[i+w-1]
	}
	return sum
}

func windowEdgesRef(s []int64, w int) int64 {
	var sum int64
	for i := w - 1; i < len(s); i++ {
		sum += s[i-(w-1)] * s[i]
	}
	return sum
}

func copyShift(dst, src []int64, k int) int {
	return copy(dst, src[k:])
}

func copyShiftLoop(dst, src []int64, k int) {
	for i := 0; i < len(dst) && i+k < len(src); i++ {
		dst[i] = src[i+k]
	}
}

// threeWay 在一次循环里同时写 dst 并读 a、b 的同一下标。
func threeWay(dst, a, b []int64) int64 {
	for i := 0; i < len(a); i++ {
		dst[i] = a[i]*2 + b[i]*2 - a[i]
	}
	var sum int64
	for i := range dst {
		sum += dst[i]
	}
	return sum
}

func bceIndex(s []int64, i int) int64 { return s[i] }

func bcePairNext(a, b []int64) int64 {
	var sum int64
	for i := range a {
		sum += b[i+1]
	}
	return sum
}

func bceSlice(s []int64, i, w int) []int64 { return s[i : i+w] }

func expectPanic(name string, fn func()) {
	defer func() {
		r := recover()
		if r == nil {
			ck(false, name+": expected panic")
			return
		}
		if !strings.Contains(fmt.Sprint(r), "out of range") {
			ck(false, name+": unexpected panic text")
		}
	}()
	fn()
}

type bceRound struct {
	a, b, c []int64
	want    [7]int64
}

func main() {
	seed = loadSeed()
	rounds := 8 * tortureMultiplier()
	kept := make([]bceRound, 0, rounds)

	for r := 0; r < rounds; r++ {
		n := 320 + int((seed+uint64(r)*17)%64)
		n -= n % 4
		if n < 32 {
			n = 32
		}
		k := 1 + int((seed+uint64(r)*3)%5)
		w := 3 + int((seed+uint64(r)*11)%5)
		a := makeVec(n, int64(seed%97)+int64(r*7)-50)
		b := makeVec(n, int64(seed%89)-int64(r*5)+11)
		c := makeVec(n, int64(seed%83)+int64(r*3))
		sumA, sumB := sumAll(a), sumAll(b)

		pair := pairAdjacent(a, b)
		ck(pair == pairAdjacentRef(a, b), "pair adjacent vs reference mismatch")
		ck(pair == 2*(sumA-a[n-1])+(sumB-b[0]), "pair adjacent vs closed form mismatch")

		rev := reversePair(a, b)
		ck(rev == reversePairRef(a, b), "reverse pair vs reference mismatch")
		ck(rev == 3*sumA-sumB, "reverse pair vs closed form mismatch")

		triple := adjacentTriple(c)
		ck(triple == adjacentTripleRef(c), "adjacent triple vs reference mismatch")

		strided := strided4(c)
		ck(strided == strided4Ref(c), "strided four-way index mismatch")

		same := sameSlicePair(a, k)
		ck(same == sameSlicePairRef(a, k), "same-slice double index mismatch")

		window := windowEdges(b, w)
		ck(window == windowEdgesRef(b, w), "window edge index mismatch")

		dst := make([]int64, n)
		three := threeWay(dst, a, b)
		ck(three == sumA+2*sumB, "three-way multi-slice write mismatch")

		dst1 := make([]int64, n-k)
		dst2 := make([]int64, n-k)
		cn := copyShift(dst1, a, k)
		copyShiftLoop(dst2, a, k)
		ck(cn == n-k, "shifted copy length mismatch")
		bad := false
		for i := range dst1 {
			if dst1[i] != dst2[i] {
				bad = true
				break
			}
		}
		ck(!bad, "shifted copy vs loop mismatch")
		ck(mixAll(dst1) == mixAll(a[k:]), "shifted copy checksum mismatch")

		kept = append(kept, bceRound{a: a, b: b, c: c, want: [7]int64{pair, rev, triple, strided, same, window, three}})
		gcStress()
	}

	// 越界访问仍须 panic，合法边界仍可读。
	check := makeVec(64, 5)
	ck(bceIndex(check, len(check)-1) == check[len(check)-1], "legal last index mismatch")
	ck(len(bceSlice(check, len(check)-4, 4)) == 4, "legal tail slice mismatch")
	expectPanic("index-at-len", func() { _ = bceIndex(check, len(check)) })
	expectPanic("index-negative", func() { _ = bceIndex(check, -1) })
	expectPanic("pair-next-overrun", func() { _ = bcePairNext(check, check) })
	expectPanic("slice-overrun", func() { _ = bceSlice(check, len(check)-2, 5) })

	// 所有轮次的数据保留到最终 GC 后复核。
	gcStress()
	for i := range kept {
		a, b, c := kept[i].a, kept[i].b, kept[i].c
		sumA, sumB := sumAll(a), sumAll(b)
		ck(pairAdjacent(a, b) == kept[i].want[0], "retained pair sum corrupted after GC")
		ck(reversePair(a, b) == kept[i].want[1], "retained reverse sum corrupted after GC")
		ck(adjacentTriple(c) == kept[i].want[2], "retained triple sum corrupted after GC")
		ck(strided4(c) == kept[i].want[3], "retained strided sum corrupted after GC")
		ck(sameSlicePair(a, 1+int((seed+uint64(i)*3)%5)) == kept[i].want[4], "retained same-slice sum corrupted after GC")
		ck(windowEdges(b, 3+int((seed+uint64(i)*11)%5)) == kept[i].want[5], "retained window sum corrupted after GC")
		ck(sumA+2*sumB == kept[i].want[6], "retained three-way sum corrupted after GC")
	}

	finish("l11_bce_multi_index")
}
