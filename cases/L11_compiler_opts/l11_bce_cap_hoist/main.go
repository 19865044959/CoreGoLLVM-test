// L11: 带 cap 的边界检查消除——三索引窗口、cap 重切后的循环、
// 相邻多下标与循环内重切片必须在 cap 内安全，越界仍 panic；
// 保留的底层数组跨 GC 后逐字节不变。
package main

import (
	"fmt"
	"os"
	"runtime"
	"strconv"
	"sync/atomic"
)

type bceSnap struct {
	live []byte
	want []byte
}

var (
	failed   int32
	gcSink   [][]byte
	seed     uint64 = 1
	bceSnaps []bceSnap
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
	if err != nil || n < 1 {
		return 1
	}
	if n > 100 {
		n = 100
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

func bceSum(b []byte) int64 {
	var s int64
	for i := 0; i < len(b); i++ {
		s += int64(b[i])
	}
	return s
}

func bceQuad(b []byte) int64 {
	var s int64
	for i := 0; i+3 < len(b); i++ {
		s += int64(b[i])*4 + int64(b[i+1])*3 + int64(b[i+2])*2 + int64(b[i+3])
	}
	return s
}

func bceQuadRef(b []byte) int64 {
	var s int64
	for i := 0; i+3 < len(b); i++ {
		for k := 0; k < 4; k++ {
			s += int64(b[i+k]) * int64(4-k)
		}
	}
	return s
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

func expectPanic(name string, fn func()) {
	defer func() {
		r := recover()
		if r == nil {
			ck(false, name+": expected panic")
			return
		}
		if !contains(fmt.Sprint(r), "out of range") {
			ck(false, name+": unexpected panic text: "+fmt.Sprint(r))
		}
	}()
	fn()
}

func main() {
	seed = loadSeed()
	if seed == 0 {
		seed = 1
	}
	rounds := 6 * tortureMultiplier()

	for r := 0; r < rounds; r++ {
		n := 24 + int((seed+uint64(r)*13)%40)
		extra := 8 + int(((seed>>8)+uint64(r)*3)%32)
		ca := n + extra
		raw := make([]byte, n, ca)
		for i := 0; i < n; i++ {
			raw[i] = byte((i*7 + int(seed) + r*31) & 0xff)
		}

		full := raw[:ca]
		for i := n; i < ca; i++ {
			full[i] = byte(0xA5 ^ byte(i))
		}
		bodySum := bceSum(raw)
		sentSum := bceSum(full[n:])

		w := 4 + int(((seed>>4)+uint64(r))%8)
		off := int(((seed >> 6) + uint64(r)) % uint64(n-w+1))
		view := raw[off : off+w : off+w]
		ck(len(view) == w && cap(view) == w, "three-index window len/cap")
		ck(bceSum(view) == bceSum(raw[off:off+w]), "three-index window contents")

		capAll := raw[:n]
		capAll = capAll[:cap(capAll)]
		ck(len(capAll) == ca && cap(capAll) == ca, "reslice to cap len/cap")
		ck(bceSum(capAll) == bodySum+sentSum, "cap-derived loop sum")

		ck(bceQuad(raw) == bceQuadRef(raw), "adjacent quadruple index sum")

		var segSum int64
		for i := 1; i <= n; i++ {
			seg := raw[:i]
			segSum += int64(seg[i-1])
		}
		ck(segSum == bodySum, "reslice inside loop sum")

		expectPanic("slice beyond cap", func() { _ = raw[:ca+1] })
		ww := w
		expectPanic("window beyond cap", func() { _ = view[:ww+1] })
		lo, hi := 2, 1
		expectPanic("low greater than high", func() { _ = raw[lo:hi] })
		neg := -1
		expectPanic("negative index", func() { _ = raw[neg] })
		idx := ca
		expectPanic("index at capacity", func() { _ = raw[idx] })

		bceSnaps = append(bceSnaps, bceSnap{
			live: capAll,
			want: append([]byte(nil), capAll...),
		})
	}

	// 所有轮次的底层数组保留到最终 GC 后逐字节复核。
	gcStress()
	for _, s := range bceSnaps {
		if len(s.live) != len(s.want) {
			ck(false, "retained capacity view length after GC")
			continue
		}
		for i := range s.want {
			if s.live[i] != s.want[i] {
				ck(false, "retained capacity view corrupted after GC")
				break
			}
		}
	}

	finish("l11_bce_cap_hoist")
}
