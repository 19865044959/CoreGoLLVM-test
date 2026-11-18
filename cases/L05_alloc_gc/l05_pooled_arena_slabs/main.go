// L05: 块式 bump arena 与 sync.Pool 组合。池中缓存 32KB arena 块，每次租用
// 重置 bump 游标后按 8B 对齐切分变长记录；记录内容快照跨后续复用/GC 校验。
// 池可能被 GC 清空，New 块与旧块都必须从偏移 0 重新开始，且同一块不能同时
// 租给两个租约。最后保留若干未归还的块跨 GC 校验，验证租用中的 arena 不被回收。
package main

import (
	"fmt"
	"hash/crc32"
	"os"
	"runtime"
	"strconv"
	"sync"
	"sync/atomic"
)

const (
	pasSlabSize = 32 << 10
	pasLeases   = 240
	pasRecs     = 12
	pasPins     = 4
)

var (
	failed  int32
	gcSink  [][]byte
	pasPool = sync.Pool{New: func() interface{} {
		return &pasSlab{buf: make([]byte, pasSlabSize)}
	}}
)

type pasSlab struct {
	buf    []byte
	cursor int
	uses   int
}

type pasRec struct {
	id  int
	off int
	n   int
	crc uint32
}

type pasSnap struct {
	lease int
	idx   int
	data  []byte
	crc   uint32
}

func ck(ok bool, msg string) {
	if !ok {
		atomic.AddInt32(&failed, 1)
		fmt.Println("FAIL:", msg)
	}
}

func ckf(ok bool, format string, args ...interface{}) {
	if !ok {
		atomic.AddInt32(&failed, 1)
		fmt.Printf("FAIL: "+format+"\n", args...)
	}
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

func finish(name string, seed uint64) {
	if n := atomic.LoadInt32(&failed); n != 0 {
		fmt.Printf("FAILED %s (%d failed checks, seed=%d)\n", name, n, seed)
		os.Exit(1)
	}
	fmt.Println("ALL PASS " + name)
}

func gcStress() {
	for i := 0; i < 20; i++ {
		b := make([]byte, 16<<10)
		b[0] = byte(i)
		gcSink = append(gcSink[:0], b)
	}
	gcSink = gcSink[:0]
	runtime.GC()
	runtime.GC()
}

func pasByte(lease, idx, j int, seed uint64) byte {
	return byte((seed*13 + uint64(lease)*29 + uint64(idx)*17 + uint64(j)*7) % 251)
}

// pasAlloc 从 arena 块中按 8B 对齐切出 n 字节；空间不足返回 false。
func pasAlloc(s *pasSlab, n int) (int, bool) {
	off := (s.cursor + 7) &^ 7
	if off+n > len(s.buf) {
		return 0, false
	}
	s.cursor = off + n
	return off, true
}

func pasCheckRec(s *pasSlab, r pasRec, lease, idx int) {
	if r.off < 0 || r.off+r.n > len(s.buf) {
		ckf(false, "lease=%d rec=%d outside slab off=%d n=%d", lease, idx, r.off, r.n)
		return
	}
	b := s.buf[r.off : r.off+r.n]
	ckf(len(b) == r.n, "lease=%d rec=%d length changed", lease, idx)
	ckf(crc32.ChecksumIEEE(b) == r.crc, "lease=%d rec=%d crc changed", lease, idx)
	ckf(b[0] == pasByte(lease, idx, 0, pasSeed), "lease=%d rec=%d first byte", lease, idx)
	ckf(b[len(b)-1] == pasByte(lease, idx, len(b)-1, pasSeed), "lease=%d rec=%d last byte", lease, idx)
}

var pasSeed uint64

func main() {
	seed := loadSeed()
	pasSeed = seed
	checkedOut := make(map[*pasSlab]bool)
	snaps := make([]pasSnap, 0, pasLeases*pasRecs)

	for lease := 0; lease < pasLeases; lease++ {
		s := pasPool.Get().(*pasSlab)
		ckf(!checkedOut[s], "lease=%d slab checked out twice", lease)
		checkedOut[s] = true
		ckf(s.cursor == 0, "lease=%d slab returned with cursor=%d", lease, s.cursor)
		ckf(len(s.buf) == pasSlabSize, "lease=%d slab size=%d want=%d", lease, len(s.buf), pasSlabSize)
		s.cursor = 0
		s.uses++

		recs := make([]pasRec, 0, pasRecs)
		lastEnd := 0
		for idx := 0; idx < pasRecs; idx++ {
			n := 16 + int((seed+uint64(lease)*7+uint64(idx)*13)%480)
			off, ok := pasAlloc(s, n)
			if !ok {
				ckf(false, "lease=%d rec=%d allocation overflow", lease, idx)
				break
			}
			ckf(off >= lastEnd, "lease=%d rec=%d overlaps previous: off=%d lastEnd=%d", lease, idx, off, lastEnd)
			ckf(off%8 == 0, "lease=%d rec=%d misaligned off=%d", lease, idx, off)
			b := s.buf[off : off+n]
			for j := range b {
				b[j] = pasByte(lease, idx, j, seed)
			}
			r := pasRec{id: lease*1000 + idx, off: off, n: n, crc: crc32.ChecksumIEEE(b)}
			recs = append(recs, r)
			snaps = append(snaps, pasSnap{
				lease: lease,
				idx:   idx,
				data:  append([]byte(nil), b...),
				crc:   r.crc,
			})
			lastEnd = off + n
		}

		ckf(len(recs) == pasRecs, "lease=%d built %d recs want=%d", lease, len(recs), pasRecs)
		if len(recs) > 0 {
			ckf(recs[0].off == 0, "lease=%d first record off=%d want=0", lease, recs[0].off)
		}
		ckf(lastEnd <= pasSlabSize, "lease=%d arena overflow end=%d", lease, lastEnd)

		if lease%40 == 39 {
			gcStress() // 租用中的 arena 跨 GC 必须保持有效
		}
		for i, r := range recs {
			pasCheckRec(s, r, lease, i)
		}

		// 记录视图必须与 arena 块共享同一底层存储：改写一个字节立即在另一视图可见。
		if len(recs) > 3 {
			r := recs[3]
			old := s.buf[r.off]
			s.buf[r.off] ^= 0xff
			ckf(s.buf[r.off:r.off+r.n][0] == old^0xff, "lease=%d rec=3 alias broken", lease)
			ckf(s.buf[r.off:r.off+r.n][0] != old, "lease=%d rec=3 mutation not visible", lease)
			s.buf[r.off] = old
		}

		s.cursor = 0
		delete(checkedOut, s)
		pasPool.Put(s)
	}

	gcStress()
	for k, sn := range snaps {
		c := crc32.ChecksumIEEE(sn.data)
		ckf(c == sn.crc, "snapshot=%d lease=%d rec=%d crc=%08x want=%08x", k, sn.lease, sn.idx, c, sn.crc)
		ckf(len(sn.data) > 0, "snapshot=%d empty", k)
		if len(sn.data) > 0 {
			ckf(sn.data[0] == pasByte(sn.lease, sn.idx, 0, seed), "snapshot=%d first byte", k)
		}
	}

	// 未归还的 arena 块跨 GC 保活；每块都写入独立记录。
	pins := make([]*pasSlab, 0, pasPins)
	pinRecs := make([][]pasRec, 0, pasPins)
	for p := 0; p < pasPins; p++ {
		s := pasPool.Get().(*pasSlab)
		ckf(!checkedOut[s], "pin=%d slab checked out twice", p)
		checkedOut[s] = true
		s.cursor = 0
		s.uses++
		recs := make([]pasRec, 0, 6)
		for i := 0; i < 6; i++ {
			n := 24 + int((seed+uint64(p)*31+uint64(i)*19)%200)
			off, ok := pasAlloc(s, n)
			if !ok {
				ckf(false, "pin=%d rec=%d overflow", p, i)
				break
			}
			b := s.buf[off : off+n]
			for j := range b {
				b[j] = pasByte(9000+p, i, j, seed)
			}
			recs = append(recs, pasRec{id: p*100 + i, off: off, n: n, crc: crc32.ChecksumIEEE(b)})
		}
		pins = append(pins, s)
		pinRecs = append(pinRecs, recs)
		gcStress()
	}

	gcStress()
	for p, s := range pins {
		for i, r := range pinRecs[p] {
			b := s.buf[r.off : r.off+r.n]
			ckf(crc32.ChecksumIEEE(b) == r.crc, "pin=%d rec=%d crc after GC", p, i)
			ckf(b[0] == pasByte(9000+p, i, 0, seed), "pin=%d rec=%d byte after GC", p, i)
		}
		ckf(s.uses >= 1, "pin=%d use counter lost", p)
	}

	// 归还 pin 后重新租用：cursor 归零，第一条记录必须从偏移 0 开始。
	for _, s := range pins {
		s.cursor = 0
		delete(checkedOut, s)
		pasPool.Put(s)
	}
	reused := pasPool.Get().(*pasSlab)
	reused.cursor = 0
	off, ok := pasAlloc(reused, 64)
	ckf(ok && off == 0, "reused slab first alloc off=%d want=0", off)
	pasPool.Put(reused)

	finish("l05_pooled_arena_slabs", seed)
}
