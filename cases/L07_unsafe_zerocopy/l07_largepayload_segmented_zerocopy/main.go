// L07: 大 payload 分块零拷贝。把 2MiB+ 缓冲切成 64KiB 段，每段用 unsafe.String
// 直接引用输入；校验和/别名可见性/跨轮保留视图跨显式 GC 全部一致。
package main

import (
	"fmt"
	"os"
	"runtime"
	"strconv"
	"sync/atomic"
	"unsafe"
)

const segSize = 64 << 10

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
		for i := 0; i < 10; i++ {
			b := make([]byte, 16384+i*97)
			b[0] = byte(i)
			b[len(b)-1] = byte(200 - i)
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
			if v > 16 {
				v = 16
			}
			return v
		}
	}
	return def
}

func pat(i int, s int64) byte {
	return byte((uint64(i)*131 + uint64(s)*17 + 7) & 0xff)
}

func buildPayload(n int, s int64) []byte {
	b := make([]byte, n)
	for i := range b {
		b[i] = pat(i, s)
	}
	return b
}

const fnvOff = uint32(2166136261)
const fnvPrime = uint32(16777619)

func fnv32(b []byte) uint32 {
	h := fnvOff
	for _, c := range b {
		h ^= uint32(c)
		h *= fnvPrime
	}
	return h
}

func fnv32s(s string) uint32 {
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

func wantTotal(n int, s int64) uint32 {
	h := fnvOff
	for i := 0; i < n; i++ {
		h ^= uint32(pat(i, s))
		h *= fnvPrime
	}
	return h
}

type segView struct {
	round int
	off   int
	n     int
	view  string
	sum   uint32
}

func makeSegs(buf []byte, s int64, round int) []segView {
	nseg := (len(buf) + segSize - 1) / segSize
	segs := make([]segView, nseg)
	for i := 0; i < nseg; i++ {
		off := i * segSize
		n := segSize
		if off+n > len(buf) {
			n = len(buf) - off
		}
		chunk := buf[off : off+n]
		segs[i] = segView{
			round: round,
			off:   off,
			n:     n,
			view:  unsafe.String(unsafe.SliceData(chunk), len(chunk)),
			sum:   fnv32(chunk),
		}
	}
	return segs
}

func verifySegs(segs []segView, s int64, total int, stage string) {
	ck(len(segs) == (total+segSize-1)/segSize, stage+": segment count")
	for i := range segs {
		sg := &segs[i]
		ck(sg.n > 0, stage+": non-empty segment i="+strconv.Itoa(i))
		ck(len(sg.view) == sg.n, stage+": view length i="+strconv.Itoa(i))
		ck(unsafe.StringData(sg.view) != nil, stage+": view data i="+strconv.Itoa(i))
		ck(fnv32s(sg.view) == sg.sum, stage+": checksum i="+strconv.Itoa(i))
		ck(sg.sum == wantSum(sg.off, sg.n, s), stage+": expected checksum i="+strconv.Itoa(i))
		ck(sg.view[0] == pat(sg.off, s), stage+": first byte i="+strconv.Itoa(i))
		mid := sg.n / 2
		ck(sg.view[mid] == pat(sg.off+mid, s), stage+": middle byte i="+strconv.Itoa(i))
		ck(sg.view[sg.n-1] == pat(sg.off+sg.n-1, s), stage+": last byte i="+strconv.Itoa(i))
		if i > 0 {
			prev := &segs[i-1]
			ck(prev.off+prev.n == sg.off, stage+": segments contiguous i="+strconv.Itoa(i))
			ck(uintptr(unsafe.Pointer(unsafe.StringData(prev.view)))+uintptr(prev.n) ==
				uintptr(unsafe.Pointer(unsafe.StringData(sg.view))),
				stage+": adjacent view addresses i="+strconv.Itoa(i))
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

	total := 2<<20 + int(seed%3)*(256<<10)
	rounds := tortureRounds(4)
	ck(total%segSize == 0, "payload size multiple of segment")
	retained := make([]segView, 0, rounds*((total+segSize-1)/segSize))

	for r := 0; r < rounds; r++ {
		s := seed + int64(r)*101
		buf := buildPayload(total, s)
		segs := makeSegs(buf, s, r)
		verifySegs(segs, s, total, "round "+strconv.Itoa(r))
		ck(fnv32(buf) == wantTotal(total, s), "whole payload checksum round "+strconv.Itoa(r))

		// 零拷贝视图必须指向输入同一地址。
		for i := range segs {
			ck(unsafe.Pointer(unsafe.StringData(segs[i].view)) == unsafe.Pointer(&buf[segs[i].off]),
				"view aliases input round="+strconv.Itoa(r)+" seg="+strconv.Itoa(i))
		}

		// 输入改写必须立刻反映到视图；恢复后一致。
		for _, off := range []int{3, segSize + 5, 2*segSize + 9, total - 1} {
			si := off / segSize
			li := off % segSize
			old := segs[si].view[li]
			buf[off] ^= 0xff
			ck(segs[si].view[li] == old^0xff, "alias write visible round="+strconv.Itoa(r)+" off="+strconv.Itoa(off))
			buf[off] ^= 0xff
			ck(segs[si].view[li] == old, "alias restored round="+strconv.Itoa(r)+" off="+strconv.Itoa(off))
		}
		ck(fnv32(buf) == wantTotal(total, s), "payload intact after alias round "+strconv.Itoa(r))

		retained = append(retained, segs...)
		buf = nil
		gcStress()

		// 之前所有轮次的视图都必须仍然有效。
		for _, sg := range retained {
			sr := seed + int64(sg.round)*101
			ck(len(sg.view) == sg.n, "retained view length round="+strconv.Itoa(sg.round))
			ck(fnv32s(sg.view) == sg.sum, "retained checksum round="+strconv.Itoa(sg.round))
			ck(sg.sum == wantSum(sg.off, sg.n, sr), "retained expected sum round="+strconv.Itoa(sg.round))
			ck(sg.view[0] == pat(sg.off, sr), "retained first byte round="+strconv.Itoa(sg.round))
			ck(sg.view[sg.n-1] == pat(sg.off+sg.n-1, sr), "retained last byte round="+strconv.Itoa(sg.round))
		}
	}

	// 总保留量覆盖所有轮次，且相邻段的地址关系在 GC 后仍成立。
	ck(len(retained) == rounds*((total+segSize-1)/segSize), "retained segment count")
	byRound := make([][]segView, rounds)
	for _, sg := range retained {
		byRound[sg.round] = append(byRound[sg.round], sg)
	}
	for r := 0; r < rounds; r++ {
		verifySegs(byRound[r], seed+int64(r)*101, total, "retained round "+strconv.Itoa(r))
	}

	finish("l07_largepayload_segmented_zerocopy")
}
