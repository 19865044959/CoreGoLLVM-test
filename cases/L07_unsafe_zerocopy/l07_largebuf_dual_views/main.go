// L07: 大缓冲上的 slice/string header 视图：手工 header 指定 data/len，
// 视图单独保活 1MiB 级堆缓冲，跨多轮分配与显式 GC 校验内容与别名。
package main

import (
	"fmt"
	"os"
	"runtime"
	"strconv"
	"sync/atomic"
	"unsafe"
)

type sliceHeader struct {
	data unsafe.Pointer
	len  int
	cap  int
}

type strHeader struct {
	data unsafe.Pointer
	len  int
}

type viewPair struct {
	sl  []byte
	s   string
	off int
	n   int
	sum uint32
}

var (
	failed    int32
	gcSink    [][]byte
	seed      int64
	keepPairs []viewPair
)

func ck(ok bool, msg string) {
	if !ok {
		atomic.AddInt32(&failed, 1)
		fmt.Printf("FAIL: %s (seed=%d)\n", msg, seed)
	}
}

func gcStress() {
	for r := 0; r < 2; r++ {
		for i := 0; i < 8; i++ {
			b := make([]byte, 16384)
			b[0] = byte(i)
			b[len(b)-1] = byte(255 - i)
			gcSink = append(gcSink[:0], b)
		}
		gcSink = gcSink[:0]
		runtime.GC()
	}
}

func finish(name string) {
	if n := atomic.LoadInt32(&failed); n != 0 {
		fmt.Printf("FAILED %s (%d failed checks)\n", name, n)
		os.Exit(1)
	}
	fmt.Println("ALL PASS " + name)
}

func pat(i int, s int64) byte {
	return byte((int64(i)*131 + s*17 + int64(i>>9)*7) & 0xff)
}

func fnv32(b []byte) uint32 {
	h := uint32(2166136261)
	for _, c := range b {
		h ^= uint32(c)
		h *= 16777619
	}
	return h
}

func wantSum(off, n int, s int64) uint32 {
	h := uint32(2166136261)
	for i := 0; i < n; i++ {
		h ^= uint32(pat(off+i, s))
		h *= 16777619
	}
	return h
}

// mkSliceView 用与运行时切片头相同的布局手工构造切片。
//
//go:noinline
func mkSliceView(base unsafe.Pointer, off, n int) []byte {
	h := sliceHeader{data: unsafe.Add(base, off), len: n, cap: n}
	return *(*[]byte)(unsafe.Pointer(&h))
}

// mkStringView 用 {data,len} 头手工构造 string。
//
//go:noinline
func mkStringView(base unsafe.Pointer, off, n int) string {
	h := strHeader{data: unsafe.Add(base, off), len: n}
	return *(*string)(unsafe.Pointer(&h))
}

// deriveRound 在大缓冲上派生多个 slice/string 头视图；返回后缓冲只由这些视图保活。
//
//go:noinline
func deriveRound(n int, s int64) []viewPair {
	buf := make([]byte, n)
	for i := range buf {
		buf[i] = pat(i, s)
	}
	base := unsafe.Pointer(&buf[0])
	all := unsafe.Slice(&buf[0], len(buf))
	ck(len(all) == n, "whole view length")

	offs := []int{0, n / 7, n / 2, n - 257, n - 1}
	lens := []int{128, 96, 512, 256, 1}
	out := make([]viewPair, 0, len(offs))
	for j := range offs {
		off, l := offs[j], lens[j]
		ck(off >= 0 && l >= 0 && off+l <= n, "view bounds j="+strconv.Itoa(j))

		sv := mkSliceView(base, off, l)
		s1 := mkStringView(base, off, l)
		s2 := unsafe.String(unsafe.SliceData(buf[off:]), l)
		ck(len(sv) == l && cap(sv) == l, "slice header shape j="+strconv.Itoa(j))
		ck(unsafe.SliceData(sv) == &buf[off], "slice header data alias j="+strconv.Itoa(j))
		ck(unsafe.StringData(s1) == &buf[off], "string header data alias j="+strconv.Itoa(j))
		ck(unsafe.StringData(s2) == &buf[off], "unsafe.String data alias j="+strconv.Itoa(j))
		ck(s1 == s2 && s1 == string(sv), "view contents agree j="+strconv.Itoa(j))

		sum := fnv32(sv)
		ck(sum == wantSum(off, l, s), "view checksum j="+strconv.Itoa(j))
		gh := *(*sliceHeader)(unsafe.Pointer(&sv))
		ck(gh.len == l && gh.cap == l && gh.data == unsafe.Pointer(&buf[off]), "header read back j="+strconv.Itoa(j))
		out = append(out, viewPair{sl: sv, s: s1, off: off, n: l, sum: sum})
	}

	// 尾部零长视图：header 合法，不应触碰数据。
	z := mkSliceView(base, n, 0)
	zs := mkStringView(base, n, 0)
	ck(len(z) == 0 && len(zs) == 0, "zero-length tail views")

	return out
}

func verifyPairs(pairs []viewPair, stage string) {
	for i := range pairs {
		p := &pairs[i]
		ck(len(p.sl) == p.n && len(p.s) == p.n, stage+": view length i="+strconv.Itoa(i))
		ck(fnv32(p.sl) == p.sum, stage+": slice checksum i="+strconv.Itoa(i))
		ck(p.s == string(p.sl), stage+": string/slice agree i="+strconv.Itoa(i))
		ck(unsafe.StringData(p.s) == unsafe.SliceData(p.sl), stage+": dual header same base i="+strconv.Itoa(i))
		ck(fnv32([]byte(p.s)) == p.sum, stage+": string checksum i="+strconv.Itoa(i))
	}
}

func main() {
	if s := os.Getenv("UAF_SEED"); s != "" {
		if v, err := strconv.ParseInt(s, 10, 64); err == nil {
			seed = v
		}
	}
	if seed < 0 {
		seed = -seed
	}
	rounds := 6
	if s := os.Getenv("TORTURE_ROUNDS"); s != "" {
		if v, err := strconv.Atoi(s); err == nil && v > 0 {
			rounds = v
		}
	}

	for r := 0; r < rounds; r++ {
		n := (1 << 20) + int((seed+int64(r)*37)%4096)
		pairs := deriveRound(n, seed+int64(r))
		verifyPairs(pairs, "round "+strconv.Itoa(r)+" pre-GC")
		if r < 8 || r == rounds-1 {
			keepPairs = append(keepPairs, pairs...)
		}
		gcStress()
		verifyPairs(pairs, "round "+strconv.Itoa(r)+" post-GC")
		if r < 8 || r == rounds-1 {
			verifyPairs(keepPairs, "retained after round "+strconv.Itoa(r))
		}
	}

	// 不同轮次的同偏移视图必须来自不同底层缓冲（旧缓冲由保留视图保活）。
	for i := 0; i < len(keepPairs); i++ {
		for j := i + 1; j < len(keepPairs); j++ {
			if keepPairs[i].off != keepPairs[j].off || keepPairs[i].n != keepPairs[j].n {
				continue
			}
			pi := unsafe.Pointer(unsafe.SliceData(keepPairs[i].sl))
			pj := unsafe.Pointer(unsafe.SliceData(keepPairs[j].sl))
			ck(pi != pj, "retained views from different rounds do not alias")
		}
	}

	runtime.KeepAlive(keepPairs)
	finish("l07_largebuf_dual_views")
}
