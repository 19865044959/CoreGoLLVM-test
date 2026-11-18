// L05: 块式 bump arena：从 64KB 块中按 16B 对齐切分记录，多代 reset 后复用
// 同一批块，跨代快照与独立大分配必须保持有效；每代穿插垃圾分配与显式 GC。
package main

import (
	"fmt"
	"os"
	"runtime"
	"strconv"
	"sync/atomic"
)

const (
	arenaBlock  = 64 << 10
	arenaRecs   = 360
	arenaGens   = 4
	arenaLarge  = 48 << 10
	arenaDirect = arenaBlock / 4
)

var (
	failed    int32
	gcSink    [][]byte
	gcScratch [64 << 10]byte
)

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

func gcStress() {
	for i := 0; i < 16; i++ {
		b := make([]byte, 16<<10)
		b[0] = byte(i)
		gcSink = append(gcSink[:0], b)
	}
	gcSink = gcSink[:0]
	runtime.GC()
	runtime.GC()
	runtime.KeepAlive(gcScratch[:0])
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

// blockArena 是只增不缩的 bump allocator：reset 只回卷游标，块本身保留复用。
type blockArena struct {
	blocks [][]byte
	bi     int
	off    int
}

func (a *blockArena) reset() {
	a.bi = 0
	a.off = 0
}

// alloc 返回长度恰为 n 的视图；大请求直接分配，loc 为 -1。
func (a *blockArena) alloc(n int) ([]byte, int) {
	if n <= 0 {
		return nil, -1
	}
	if n > arenaDirect {
		return make([]byte, n), -1
	}
	step := (n + 15) &^ 15
	if a.bi >= len(a.blocks) {
		a.blocks = append(a.blocks, make([]byte, arenaBlock))
	}
	if a.off+step > arenaBlock {
		a.bi++
		a.off = 0
		if a.bi >= len(a.blocks) {
			a.blocks = append(a.blocks, make([]byte, arenaBlock))
		}
	}
	block := a.blocks[a.bi]
	loc := a.bi*arenaBlock + a.off
	buf := block[a.off : a.off+n]
	a.off += step
	return buf, loc
}

type arenaRec struct {
	buf  []byte
	loc  int
	sum  uint64
	snap []byte
}

func arenaByte(gen, idx, off int, seed uint64) byte {
	return byte((seed*17 + uint64(gen)*101 + uint64(idx)*29 + uint64(off)*7 + 3) % 251)
}

func arenaFill(b []byte, gen, idx int, seed uint64) {
	for i := range b {
		b[i] = arenaByte(gen, idx, i, seed)
	}
}

func fnv(b []byte) uint64 {
	h := uint64(1469598103934665603)
	for i := range b {
		h ^= uint64(b[i])
		h *= 1099511628211
	}
	return h
}

func arenaCheck(tag string, gen, idx int, r *arenaRec, seed uint64) {
	ckf(len(r.buf) == len(r.snap), "%s gen=%d rec=%d view len changed", tag, gen, idx)
	ckf(fnv(r.buf) == r.sum, "%s gen=%d rec=%d fnv mismatch", tag, gen, idx)
	ckf(fnv(r.snap) == r.sum, "%s gen=%d rec=%d snapshot changed", tag, gen, idx)
	for i := 0; i < len(r.buf); i += 31 {
		if r.buf[i] != arenaByte(gen, idx, i, seed) {
			ckf(false, "%s gen=%d rec=%d byte mismatch at=%d", tag, gen, idx, i)
			break
		}
	}
}

func main() {
	seed := loadSeed()
	a := &blockArena{}

	var recs []arenaRec
	var baseBlocks int
	var firstBlock *byte
	var pinnedRec []byte
	var pinnedRecSum uint64
	var pinnedLarge []byte
	var pinnedLargeSum uint64

	for gen := 0; gen < arenaGens; gen++ {
		a.reset()
		recs = recs[:0]
		prevEnd := -1
		for idx := 0; idx < arenaRecs; idx++ {
			size := 24 + int((uint64(idx)*37+seed*11)%480)
			buf, loc := a.alloc(size)
			ckf(len(buf) == size, "gen=%d rec=%d alloc len=%d want=%d", gen, idx, len(buf), size)
			if loc >= 0 {
				ckf(loc%16 == 0, "gen=%d rec=%d unaligned loc=%d", gen, idx, loc)
				if prevEnd >= 0 {
					ckf(loc >= prevEnd, "gen=%d rec=%d overlap loc=%d prevEnd=%d", gen, idx, loc, prevEnd)
				}
				prevEnd = loc + ((size + 15) &^ 15)
				if gen > 0 {
					ckf(loc >= 0 && loc < arenaBlock*2, "gen=%d rec=%d loc out of range=%d", gen, idx, loc)
				}
			}
			arenaFill(buf, gen, idx, seed)
			recs = append(recs, arenaRec{buf: buf, loc: loc, sum: fnv(buf), snap: append([]byte(nil), buf...)})
		}

		large, loc := a.alloc(arenaLarge)
		ck(loc == -1 && len(large) == arenaLarge, "large allocation must be direct")
		for i := range large {
			large[i] = byte((uint64(gen)*53 + uint64(i)*9 + seed) % 251)
		}
		largeSum := fnv(large)

		if gen == 0 {
			baseBlocks = len(a.blocks)
			ckf(baseBlocks >= 2, "arena used only %d blocks", baseBlocks)
			firstBlock = &a.blocks[0][0]
			pinnedRec = append([]byte(nil), recs[17].buf...)
			pinnedRecSum = recs[17].sum
			pinnedLarge = append([]byte(nil), large...)
			pinnedLargeSum = largeSum
		} else {
			ckf(len(a.blocks) == baseBlocks, "gen=%d block count=%d want=%d (reset must reuse)", gen, len(a.blocks), baseBlocks)
			ckf(&a.blocks[0][0] == firstBlock, "gen=%d first block backing array changed", gen)
			ckf(fnv(pinnedLarge) == pinnedLargeSum, "gen=%d pinned direct allocation changed", gen)
			ckf(fnv(pinnedRec) == pinnedRecSum, "gen=%d pinned record copy changed", gen)
		}

		gcStress()
		for idx := range recs {
			arenaCheck("afterGC", gen, idx, &recs[idx], seed)
		}
		ckf(fnv(pinnedLarge) == pinnedLargeSum, "gen=%d pinned large corrupted after GC", gen)
		ckf(fnv(pinnedRec) == pinnedRecSum, "gen=%d pinned record corrupted after GC", gen)
		ckf(fnv(large) == largeSum, "gen=%d live large allocation corrupted after GC", gen)
	}

	// 最终 reset 后再踏一块，确认游标从块头开始且不新增块。
	a.reset()
	buf, loc := a.alloc(128)
	ck(loc == 0 && len(buf) == 128, "post-reset bump must start at offset 0")
	ckf(len(a.blocks) == baseBlocks, "post-reset allocation grew block count=%d want=%d", len(a.blocks), baseBlocks)
	runtime.KeepAlive(recs)
	runtime.KeepAlive(seed)

	finish("l05_arena_bump_blocks", seed)
}
