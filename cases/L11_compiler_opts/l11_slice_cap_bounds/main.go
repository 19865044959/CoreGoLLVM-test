// L11: 带 cap 的切片边界——三索引切片、cap 限制的 append、满容量别名、
// 容量内哨兵与越界 panic 的语义必须与切片模型一致，并跨 GC 保持。
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

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

func expectSlicePanic(name string, fn func()) {
	defer func() {
		r := recover()
		if r == nil {
			ck(false, name+": expected slice panic")
			return
		}
		if !contains(fmt.Sprint(r), "out of range") {
			ck(false, name+": unexpected panic text: "+fmt.Sprint(r))
		}
	}()
	fn()
}

func equalInt64(a, b []int64) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func main() {
	seed = loadSeed()
	if seed == 0 {
		seed = 1
	}
	rounds := 6 * tortureMultiplier()

	buf := make([]int64, 10, 20)
	for i := range buf {
		buf[i] = int64(i)
	}
	t := buf[2:5:6]
	ck(len(t) == 3 && cap(t) == 4, "three-index len/cap")
	ck(&t[0] == &buf[2], "subslice shares backing array")
	ck(cap(buf[3:]) == 17 && cap(buf[3:7]) == 17 && cap(buf[3:7:9]) == 6,
		"cap arithmetic")

	t = append(t, 111)
	ck(len(t) == 4 && cap(t) == 4 && &t[0] == &buf[2], "append within cap")
	ck(buf[5] == 111, "append within cap writes backing array")
	t = append(t, 222)
	ck(len(t) == 5 && cap(t) >= 5, "append beyond cap grows")
	ck(buf[5] == 111, "reallocated append leaves original array")
	ck(t[4] == 222, "grown slice value")

	// 满容量切片 append 必须复制，不得写坏同底层的兄弟切片。
	raw := make([]int64, 8, 16)
	for i := 0; i < len(raw); i++ {
		raw[i] = int64(i)
	}
	left := raw[:4:4]
	right := raw[4:8:8]
	left = append(left, 99)
	ck(raw[4] == 4 && right[0] == 4, "full-slice cap prevents sibling overwrite")
	ck(left[4] == 99 && cap(left) >= 5, "full-slice append result")

	// 容量内哨兵：append 只能在 cap 之内写。
	guard := make([]int64, 16, 16)
	for i := range guard {
		guard[i] = int64(1000 + i)
	}
	g := guard[:8:12]
	g = append(g, 1, 2, 3, 4)
	ck(len(g) == 12 && cap(g) == 12, "guarded append len/cap")
	ck(guard[8] == 1 && guard[11] == 4, "guarded append values")
	ck(guard[12] == 1012 && guard[15] == 1015, "capacity sentinel untouched")
	full := guard[:cap(guard)]
	ck(len(full) == 16 && full[15] == 1015, "reslice to cap allowed")

	// 重叠 copy 的 memmove 语义。
	src := []int64{0, 1, 2, 3, 4, 5}
	dst := append([]int64(nil), src...)
	copy(dst[1:], dst[:len(dst)-1])
	ck(equalInt64(dst, []int64{0, 0, 1, 2, 3, 4}), "overlapping copy")

	// 越界/非法三索引必须 panic。
	lo, hi, mx := 0, 5, 4
	expectSlicePanic("max < high", func() { _ = buf[lo:hi:mx] })
	mx2 := cap(buf) + 1
	expectSlicePanic("max > cap", func() { _ = buf[lo:hi:mx2] })
	lo3, hi3 := 5, 3
	expectSlicePanic("low > high", func() { _ = buf[lo3:hi3] })
	expectSlicePanic("high beyond cap", func() { _ = buf[:cap(buf)+1] })
	neg := -1
	expectSlicePanic("negative low", func() { _ = buf[neg:] })
	ck(len(buf) == 10 && buf[5] == 111, "buffer intact after panic recovery")

	alias := buf[2:5:6]
	gcStress()
	ck(alias[0] == 2 && alias[2] == 4 && cap(alias) == 4, "subslice after GC")
	ck(&alias[0] == &buf[2], "subslice identity after GC")
	ck(guard[12] == 1012 && guard[15] == 1015, "sentinel after GC")
	ck(len(t) == 5 && t[4] == 222, "grown slice after GC")

	for r := 0; r < rounds; r++ {
		total := 16 + int((seed+uint64(r))%16)
		rbuf := make([]int64, total, total*2)
		base := int64(seed % 5)
		for i := range rbuf {
			rbuf[i] = int64(i*7) + base
		}
		cut := 2 + int((seed+uint64(r)*3)%uint64(total-6))
		high := cut + 4
		max := high + 2
		part := rbuf[cut:high:max]
		ck(len(part) == 4 && cap(part) == 6, "random three-index len/cap")
		var sum, want int64
		for _, v := range part {
			sum += v
		}
		for i := cut; i < high; i++ {
			want += int64(i*7) + base
		}
		ck(sum == want, "random subslice sum")
		part = append(part, -1)
		ck(&part[0] == &rbuf[cut], "random append within cap shares")
		ck(rbuf[high] == -1, "random append writes at high index")
		gcStress()
		ck(rbuf[cut] == int64(cut*7)+base, "random backing after GC")
		ck(len(part) == 5 && part[4] == -1, "random part after GC")
	}

	finish("l11_slice_cap_bounds")
}
