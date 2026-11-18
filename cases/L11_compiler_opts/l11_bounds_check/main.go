// L11: 循环边界检查消除不得改变语义——合法下标结果与逐元素参考一致，
// 越界索引/切片必须 panic（recover 校验 runtime.Error），GC 后数据不变。
package main

import (
	"fmt"
	"os"
	"runtime"
	"strconv"
	"sync/atomic"
)

var (
	failed     int32
	gcSink     [][]byte
	boundsSink int64
	seed       uint64 = 1
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

func sumIdx(s []int64) int64 {
	var t int64
	for i := 0; i < len(s); i++ {
		t += s[i]
	}
	return t
}

func sumRange(s []int64) int64 {
	var t int64
	for _, v := range s {
		t += v
	}
	return t
}

func weighted(a, b []int64) int64 {
	n := len(a)
	if len(b) < n {
		n = len(b)
	}
	var t int64
	for i := 0; i < n; i++ {
		t += a[i] * b[i]
	}
	return t
}

func clampCut(s []int64, lo, hi int) []int64 {
	if lo < 0 {
		lo = 0
	}
	if hi > len(s) {
		hi = len(s)
	}
	if hi < lo {
		hi = lo
	}
	return s[lo:hi]
}

func expectBoundsPanic(name string, fn func()) {
	defer func() {
		r := recover()
		if r == nil {
			ck(false, name+": expected bounds panic")
			return
		}
		msg := fmt.Sprint(r)
		if !contains(msg, "out of range") {
			ck(false, name+": unexpected panic text: "+msg)
		}
	}()
	fn()
}

func contains(s, sub string) bool {
	if len(sub) == 0 {
		return true
	}
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

func main() {
	seed = loadSeed()
	n := 64 + int(seed%32)
	s := make([]int64, n)
	var want int64
	for i := range s {
		v := int64(i*3) - 7
		s[i] = v
		want += v
	}

	ck(sumIdx(s) == want, "index-loop sum mismatch")
	ck(sumRange(s) == want, "range-loop sum mismatch")
	ck(sumIdx(s) == sumRange(s), "two loop forms disagree")
	ck(sumIdx(nil) == 0, "nil slice index-loop sum")
	ck(sumRange(nil) == 0, "nil slice range-loop sum")
	ck(sumIdx(s[:0]) == 0, "empty slice index-loop sum")

	ck(s[len(s)-1] == int64((n-1)*3-7), "last element read mismatch")
	asc := s[1 : len(s)-1]
	ck(len(asc) == n-2, "subslice length mismatch")
	ck(sumIdx(asc) == want-s[0]-s[n-1], "subslice sum mismatch")
	ck(s[n-1:][0] == s[n-1], "tail subslice read mismatch")

	b := make([]int64, n+5)
	for i := range b {
		b[i] = int64(i%5) + 1
	}
	var wantW int64
	for i := 0; i < n; i++ {
		wantW += s[i] * b[i]
	}
	ck(weighted(s, b) == wantW, "weighted pair-loop sum mismatch")
	ck(len(clampCut(s, -3, n+10)) == n, "clamped slice length mismatch")
	ck(len(clampCut(s, 3, 2)) == 0, "empty clamped slice length mismatch")

	expectBoundsPanic("index len", func() {
		i := len(s)
		boundsSink = s[i]
	})
	expectBoundsPanic("negative index", func() {
		i := -1
		boundsSink = s[i]
	})
	expectBoundsPanic("reverse slice", func() {
		lo, hi := 5, 3
		_ = s[lo:hi]
	})
	expectBoundsPanic("slice hi over", func() {
		hi := len(s) + 1
		_ = s[:hi]
	})
	expectBoundsPanic("nil slice index", func() {
		var ns []int64
		i := 0
		boundsSink = ns[i]
	})
	expectBoundsPanic("zero-len index", func() {
		z := s[:0]
		i := 0
		boundsSink = z[i]
	})

	wantTotal := int64(16)*int64(n)*int64(n-1)/2 + int64(n)*(16*15/2)
	var total int64
	for r := 0; r < 16; r++ {
		tmp := make([]int64, n)
		for i := range tmp {
			tmp[i] = int64(r + i)
		}
		total += sumIdx(tmp)
		if r%4 == 3 {
			gcStress()
		}
	}
	ck(total == wantTotal, "round-loop sum mismatch")
	ck(len(s) == n && sumIdx(s) == want, "slice corrupted after GC stress")

	finish("l11_bounds_check")
}
