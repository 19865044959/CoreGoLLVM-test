// L07: 内存映射式只读窗口。用页表 unsafe.Pointer 描述堆上大页缓冲，派生页窗口与
// 跨页窗口；丢弃缓冲变量后仅由页表/string 视图保活，跨 GC 校验只读内容。
package main

import (
	"fmt"
	"os"
	"runtime"
	"strconv"
	"sync/atomic"
	"unsafe"
)

const pageSize = 4096
const maxPages = 8

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
			b := make([]byte, 12288+i*53)
			b[0] = byte(i)
			b[len(b)-1] = byte(190 - i)
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
			if v > 24 {
				v = 24
			}
			return v
		}
	}
	return def
}

func pat(i int, s int64) byte {
	return byte((uint64(i)*97 + uint64(s)*29 + 3) & 0xff)
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
	for i := off; i < off+n; i++ {
		h ^= uint32(pat(i, s))
		h *= 16777619
	}
	return h
}

type pageEnt struct {
	start unsafe.Pointer
	n     int
	sum   uint32
}

type mapping struct {
	np  int
	s   int64
	ent [maxPages]pageEnt
	hdr string
}

func buildMapping(s int64, np int) *mapping {
	mem := new([maxPages * pageSize]byte)
	for i := 0; i < np*pageSize; i++ {
		mem[i] = pat(i, s)
	}
	base := unsafe.Pointer(&mem[0])
	m := &mapping{np: np, s: s}
	for i := 0; i < np; i++ {
		off := i * pageSize
		m.ent[i] = pageEnt{
			start: unsafe.Add(base, uintptr(off)),
			n:     pageSize,
			sum:   wantSum(off, pageSize, s),
		}
		ck(m.ent[i].start == unsafe.Pointer(&mem[off]), "page start identity i="+strconv.Itoa(i))
		ck(uintptr(m.ent[i].start)%unsafe.Alignof(uint64(0)) == 0, "page start alignment i="+strconv.Itoa(i))
	}
	m.hdr = unsafe.String((*byte)(base), 64)
	// 只保留页表与 header 视图，mem 变量离开作用域。
	return m
}

func (m *mapping) page(i int) []byte {
	return unsafe.Slice((*byte)(m.ent[i].start), m.ent[i].n)
}

func (m *mapping) cross() []byte {
	return unsafe.Slice((*byte)(unsafe.Add(m.ent[0].start, pageSize-24)), 96)
}

func verifyMapping(m *mapping, stage string) {
	ck(m.np >= 6 && m.np <= maxPages, stage+": page count")
	for i := 0; i < m.np; i++ {
		pg := m.page(i)
		ck(len(pg) == pageSize, stage+": page len i="+strconv.Itoa(i))
		ck(fnv32(pg) == m.ent[i].sum, stage+": page checksum i="+strconv.Itoa(i))
		ck(m.ent[i].sum == wantSum(i*pageSize, pageSize, m.s), stage+": expected checksum i="+strconv.Itoa(i))
		ck(uintptr(m.ent[i].start)-uintptr(m.ent[0].start) == uintptr(i*pageSize), stage+": page stride i="+strconv.Itoa(i))
		ck(uintptr(m.ent[i].start)%unsafe.Alignof(uint64(0)) == 0, stage+": page alignment i="+strconv.Itoa(i))
		ck(pg[0] == pat(i*pageSize, m.s), stage+": first byte i="+strconv.Itoa(i))
		ck(pg[pageSize/2] == pat(i*pageSize+pageSize/2, m.s), stage+": mid byte i="+strconv.Itoa(i))
		ck(pg[pageSize-1] == pat(i*pageSize+pageSize-1, m.s), stage+": last byte i="+strconv.Itoa(i))
	}
	ck(len(m.hdr) == 64, stage+": header len")
	for k := 0; k < 64; k++ {
		ck(m.hdr[k] == pat(k, m.s), stage+": header byte k="+strconv.Itoa(k))
	}
	cross := m.cross()
	ck(len(cross) == 96, stage+": cross len")
	for k := 0; k < 96; k++ {
		ck(cross[k] == pat(pageSize-24+k, m.s), stage+": cross byte k="+strconv.Itoa(k))
	}
}

func window(mem *[maxPages * pageSize]byte, off, n int) ([]byte, bool) {
	if off < 0 || n < 0 || off > len(mem) || n > len(mem)-off {
		return nil, false
	}
	return mem[off : off+n], true
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

	// 1) 边界检查：页内、跨页、末字节、零长，越界必须拒绝。
	buf := new([maxPages * pageSize]byte)
	for i := range buf {
		buf[i] = pat(i, seed)
	}
	if w, ok := window(buf, 0, 64); !ok || len(w) != 64 || w[0] != pat(0, seed) {
		ck(false, "page head window")
	}
	if w, ok := window(buf, pageSize-16, 64); !ok || len(w) != 64 || w[0] != pat(pageSize-16, seed) || w[63] != pat(pageSize+47, seed) {
		ck(false, "cross-page window")
	}
	if w, ok := window(buf, len(buf)-1, 1); !ok || w[0] != pat(len(buf)-1, seed) {
		ck(false, "last byte window")
	}
	if _, ok := window(buf, len(buf), 0); !ok {
		ck(false, "zero-length window at end")
	}
	if _, ok := window(buf, -1, 1); ok {
		ck(false, "negative offset must fail")
	}
	if _, ok := window(buf, len(buf), 1); ok {
		ck(false, "past-end window must fail")
	}
	if _, ok := window(buf, len(buf)-1, 2); ok {
		ck(false, "overrun window must fail")
	}
	if _, ok := window(buf, 1, -1); ok {
		ck(false, "negative length must fail")
	}
	ck(fnv32(buf[:]) == wantSum(0, len(buf), seed), "buffer contents after bounds checks")

	// 2) 构建页表并丢弃大缓冲，仅由页表/视图保活。
	np := 6 + int(seed%3)
	rounds := tortureRounds(3)
	retained := make([]*mapping, 0, rounds)
	for r := 0; r < rounds; r++ {
		s := seed + int64(r)*37
		m := buildMapping(s, np)
		verifyMapping(m, "round "+strconv.Itoa(r))
		retained = append(retained, m)
		// 每轮都显式 GC，并复核此前所有轮次的映射。
		gcStress()
		for k, rm := range retained {
			verifyMapping(rm, "retained round "+strconv.Itoa(k))
		}
	}
	ck(len(retained) == rounds, "retained mapping count")
	for _, m := range retained {
		ck(m.np == np, "retained page count")
		ck(m.ent[0].start != nil, "retained page base non-nil")
		ck(m.ent[np-1].start != nil, "retained last page non-nil")
	}

	finish("l07_mmap_window_readonly")
}
