// L07: 只读窗口的高基数子视图。单个不可变大缓冲上派生大量 string/[]byte 子视图，
// 覆盖零长、末字节、页边界与跨页起点；丢弃缓冲变量后仅由子视图跨 GC 保活。
package main

import (
	"fmt"
	"os"
	"runtime"
	"strconv"
	"sync/atomic"
	"unsafe"
)

const winPage = 4096

var (
	failed int32
	gcSink [][]byte
	seed   int64
)

func ck(ok bool, msg string) {
	if !ok {
		atomic.AddInt32(&failed, 1)
		fmt.Printf("FAIL: %s (seed=%d)\n", msg, seed)
	}
}

func gcStress() {
	for r := 0; r < 2; r++ {
		for i := 0; i < 12; i++ {
			b := make([]byte, 10240+i*29)
			b[0] = byte(i)
			b[len(b)-1] = byte(210 - i)
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

func tortureRounds(def int) int {
	if s := os.Getenv("TORTURE_ROUNDS"); s != "" {
		if v, err := strconv.Atoi(s); err == nil && v > 0 {
			if v > 8 {
				v = 8
			}
			return v
		}
	}
	return def
}

func pat(i int, s int64) byte {
	return byte((uint64(i)*89 + uint64(s)*31 + 5) & 0xff)
}

const (
	fnvOff   = uint32(2166136261)
	fnvPrime = uint32(16777619)
)

func fnvBytes(b []byte) uint32 {
	h := fnvOff
	for _, c := range b {
		h ^= uint32(c)
		h *= fnvPrime
	}
	return h
}

func fnvString(s string) uint32 {
	h := fnvOff
	for i := 0; i < len(s); i++ {
		h ^= uint32(s[i])
		h *= fnvPrime
	}
	return h
}

func wantSum(off, n int, s int64) uint32 {
	h := fnvOff
	for i := off; i < off+n; i++ {
		h ^= uint32(pat(i, s))
		h *= fnvPrime
	}
	return h
}

type winView struct {
	round int
	off   int
	n     int
	str   string
	bs    []byte
	sum   uint32
	cross bool
}

func nextRand(h *uint64) uint64 {
	*h ^= *h << 13
	*h ^= *h >> 7
	*h ^= *h << 17
	return *h
}

func makeViews(win []byte, s int64, round int) []winView {
	total := len(win)
	h := uint64(seed+1)*0x9e3779b97f4a7c15 + uint64(round+7)*0xbf58476d1ce4e5b9
	k := 96 + int(seed%32)
	views := make([]winView, 0, k)
	for j := 0; j < k; j++ {
		var off, n int
		switch j % 9 {
		case 0:
			off, n = 0, 1+int(nextRand(&h)%33)
		case 1:
			n = 1 + int(nextRand(&h)%33)
			off = total - n
		case 2:
			off = winPage - 1
			n = 2 + int(nextRand(&h)%7)
		case 3:
			off, n = int(nextRand(&h)%uint64(total)), 0
		case 4:
			off, n = total, 0
		default:
			off = int(nextRand(&h) % uint64(total))
			rem := total - off
			n = 1 + int(nextRand(&h)%uint64(rem))
			if n > 97 {
				n = 97
			}
		}
		if off < 0 || off > total || n < 0 || n > total-off {
			ck(false, "generated view out of range")
			continue
		}
		bv := win[off : off+n]
		sv := ""
		if n > 0 {
			sv = unsafe.String(unsafe.SliceData(bv), n)
			ck(unsafe.Pointer(unsafe.StringData(sv)) == unsafe.Pointer(&bv[0]), "string/bytes view alias")
			ck(unsafe.Pointer(unsafe.StringData(sv)) == unsafe.Pointer(&win[off]), "view aliases window")
		}
		views = append(views, winView{
			round: round,
			off:   off,
			n:     n,
			str:   sv,
			bs:    bv,
			sum:   wantSum(off, n, s),
			cross: n > 0 && off/winPage != (off+n-1)/winPage,
		})
	}
	return views
}

func verifyViews(views []winView, stage string) {
	for i := range views {
		v := &views[i]
		s := seed + int64(v.round)*77
		ck(len(v.str) == v.n, stage+": string len i="+strconv.Itoa(i))
		ck(len(v.bs) == v.n, stage+": bytes len i="+strconv.Itoa(i))
		ck(fnvString(v.str) == v.sum, stage+": string sum i="+strconv.Itoa(i))
		ck(fnvBytes(v.bs) == v.sum, stage+": bytes sum i="+strconv.Itoa(i))
		ck(v.sum == wantSum(v.off, v.n, s), stage+": expected sum i="+strconv.Itoa(i))
		ck(v.cross == (v.n > 0 && v.off/winPage != (v.off+v.n-1)/winPage), stage+": cross flag i="+strconv.Itoa(i))
		if v.n > 0 {
			ck(v.str[0] == pat(v.off, s), stage+": first byte i="+strconv.Itoa(i))
			ck(v.bs[v.n/2] == pat(v.off+v.n/2, s), stage+": mid byte i="+strconv.Itoa(i))
			ck(v.str[v.n-1] == pat(v.off+v.n-1, s), stage+": last byte i="+strconv.Itoa(i))
		} else {
			ck(v.str == "", stage+": empty string i="+strconv.Itoa(i))
		}
	}
}

func main() {
	if env := os.Getenv("UAF_SEED"); env != "" {
		if v, err := strconv.ParseInt(env, 10, 64); err == nil {
			seed = v
		}
	}
	if seed < 0 {
		seed = -seed
	}

	total := (192 << 10) + int(seed%3)*(32<<10)
	rounds := tortureRounds(2)
	retained := make([]winView, 0, rounds*128)

	for r := 0; r < rounds; r++ {
		s := seed + int64(r)*77
		win := make([]byte, total)
		for i := range win {
			win[i] = pat(i, s)
		}
		sum0 := fnvBytes(win)
		ck(sum0 == wantSum(0, total, s), "window checksum round="+strconv.Itoa(r))

		views := makeViews(win, s, r)
		ck(len(views) >= 96, "view count round="+strconv.Itoa(r))
		crossN := 0
		for i := range views {
			if views[i].cross {
				crossN++
			}
		}
		ck(crossN > 0, "has cross-page views round="+strconv.Itoa(r))
		verifyViews(views, "round "+strconv.Itoa(r))
		ck(fnvBytes(win) == sum0, "window immutable after scan round="+strconv.Itoa(r))

		retained = append(retained, views...)
		win = nil
		gcStress()
		verifyViews(retained, "retained after gc round="+strconv.Itoa(r))
	}

	ck(len(retained) >= rounds*96, "retained view count")
	finish("l07_readonly_window_alias_scan")
}
