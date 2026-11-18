// L11: BCE 提升 × 循环内重绑定/重切片——循环体内 slice 头被重新赋值、
// 收缩、扩展或按条件切换时，边界检查不能被错误提升；结果必须与独立
// 参考实现一致，越界仍 panic，保留数据跨显式 GC 不变。
package main

import (
	"fmt"
	"os"
	"runtime"
	"strconv"
	"sync/atomic"
)

type brSnap struct {
	a, b []int64
	want int64
}

var (
	failed  int32
	gcSink  [][]byte
	seed    uint64 = 1
	brSnaps []brSnap
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

func brShrink(s []int64) int64 {
	var sum int64
	for len(s) > 0 {
		sum += s[len(s)-1] * 2
		s = s[:len(s)-1]
	}
	return sum
}

func brPeel(s []int64) int64 {
	var sum int64
	for len(s) > 1 {
		sum += s[0] + s[len(s)-1]
		s = s[1 : len(s)-1]
	}
	if len(s) == 1 {
		sum += s[0]
	}
	return sum
}

func brRot(base []int64) int64 {
	s := base
	var sum int64
	for i := 0; i < 8; i++ {
		sum += s[0] * int64(i+1)
		s = s[1:]
	}
	return sum
}

func brGrow(pool []int64, n int) (int64, int) {
	s := pool[:0]
	var sum int64
	for i := 0; i < n; i++ {
		s = s[:i+1]
		s[i] = int64(i*3 + 1)
		sum += s[i]
	}
	return sum, cap(s)
}

func brAlternate(a, b []int64) int64 {
	var sum int64
	s := a
	for i := 0; i < len(a); i++ {
		if i%2 == 0 {
			s = a
		} else {
			s = b
		}
		sum += s[i%len(s)] * int64(i+1)
	}
	return sum
}

func brAlternateRef(a, b []int64) int64 {
	var sum int64
	for i := 0; i < len(a); i++ {
		if i%2 == 0 {
			sum += a[i%len(a)] * int64(i+1)
		} else {
			sum += b[i%len(b)] * int64(i+1)
		}
	}
	return sum
}

func brWindows(base []int64) int64 {
	var sum int64
	for w := 1; w <= 6; w++ {
		s := base
		for len(s) >= w {
			sum += s[w-1]
			s = s[1:]
		}
	}
	return sum
}

func brWindowsRef(base []int64) int64 {
	var sum int64
	for w := 1; w <= 6; w++ {
		for start := 0; start+w <= len(base); start++ {
			sum += base[start+w-1]
		}
	}
	return sum
}

func brAppendLoop(n int, salt int64) int64 {
	s := make([]int64, 0, 4)
	var sum int64
	for i := 0; i < n; i++ {
		s = append(s, int64(i)+salt)
		sum += s[len(s)-1] * 2
	}
	return sum
}

func brStaleIndex(s []int64, cut int) int64 {
	n := len(s)
	s = s[:cut]
	return s[n-1]
}

func brPastLen(s []int64, cut int) int64 {
	s = s[:cut]
	return s[cut]
}

func brPastCap(s []int64, cut int) []int64 {
	s = s[:cut:cut]
	return s[:cut+1]
}

func expectPanic(name string, fn func()) {
	defer func() {
		if recover() == nil {
			ck(false, name+": expected panic")
		}
	}()
	fn()
}

func main() {
	seed = loadSeed()
	if seed == 0 {
		seed = 1
	}
	n := 64 + int(seed%31)

	for r := 0; r < 3; r++ {
		a := make([]int64, n)
		b := make([]int64, n+8)
		var sumA int64
		for i := 0; i < n; i++ {
			a[i] = int64((i*13+int(seed)+r*7)%211) - 105
			sumA += a[i]
		}
		for i := range b {
			b[i] = int64((i*7+int(seed)+r*31)%97) - 48
		}

		ck(brShrink(a) == 2*sumA, "shrinking loop sum")
		ck(brPeel(a) == sumA, "peeling loop sum")
		wantRot := int64(0)
		for i := 0; i < 8; i++ {
			wantRot += a[i] * int64(i+1)
		}
		ck(brRot(a) == wantRot, "rotating rebind sum")

		pool := make([]int64, n, n+8)
		gotGrow, capGrow := brGrow(pool, n)
		ck(gotGrow == int64(3*n*(n-1)/2+n), "growing rebind sum")
		ck(capGrow == n+8, "growing rebind keeps capacity")
		growOK := true
		for i := 0; i < n; i++ {
			if pool[i] != int64(i*3+1) {
				growOK = false
				break
			}
		}
		ck(growOK, "growing rebind stores")

		alt := brAlternate(a, b)
		ck(alt == brAlternateRef(a, b), "alternating rebind sum")
		win := brWindows(a)
		ck(win == brWindowsRef(a), "nested window rebind sum")

		salt := int64(seed%29) - 14
		wantAppend := int64(n*(n-1)) + 2*int64(n)*salt
		ck(brAppendLoop(n, salt) == wantAppend, "append growth loop sum")

		brSnaps = append(brSnaps, brSnap{
			a:    append([]int64(nil), a...),
			b:    append([]int64(nil), b...),
			want: brShrink(a) + brPeel(a) + brRot(a) + alt + win,
		})
		gcStress()
	}

	check := make([]int64, 32)
	for i := range check {
		check[i] = int64(i * 2)
	}
	ck(brShrink(check) == 4*int64(31*32/2), "legal shrink boundary")
	ck(brStaleIndex(check, len(check)) == check[len(check)-1], "no-op rebind keeps last index legal")
	expectPanic("stale-length-after-shrink", func() { _ = brStaleIndex(check, 31) })
	expectPanic("stale-length-after-shrink-short", func() { _ = brStaleIndex(check, 16) })
	expectPanic("index-at-new-len", func() { _ = brPastLen(check, 20) })
	expectPanic("reslice-past-rebound-cap", func() { _ = brPastCap(check, 20) })

	gcStress()
	for i := range brSnaps {
		s := brSnaps[i]
		got := brShrink(s.a) + brPeel(s.a) + brRot(s.a) + brAlternate(s.a, s.b) + brWindows(s.a)
		ck(got == s.want, "retained rebind sums after GC")
	}

	finish("l11_bce_loop_rebind")
}
