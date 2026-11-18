// L05: 手工 size-bucket 分配器：6 档固定尺寸，每档在 4KB slab 上切槽，
// 空闲槽用 free-list 回收。分配/释放/重分配之间穿插垃圾分配与显式 GC，
// 校验槽位不重复、内容不被串写、释放后可复用且不无限增长 slab。
package main

import (
	"fmt"
	"os"
	"runtime"
	"strconv"
	"sync/atomic"
)

const (
	bSlabSize  = 4096
	bPerBucket = 96
)

var (
	failed int32
	gcSink [][]byte
	bSizes = []int{16, 32, 64, 128, 256, 512}
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

type bucket struct {
	size    int
	slabs   [][]byte
	free    []int
	used    []bool
	perSlab int
}

func newBucket(size int) *bucket {
	return &bucket{size: size, perSlab: bSlabSize / size}
}

func (b *bucket) addSlab() {
	b.slabs = append(b.slabs, make([]byte, bSlabSize))
	base := (len(b.slabs) - 1) * b.perSlab
	for s := 0; s < b.perSlab; s++ {
		b.used = append(b.used, false)
		b.free = append(b.free, base+s)
	}
}

func (b *bucket) alloc() (int, []byte) {
	if len(b.free) == 0 {
		b.addSlab()
	}
	idx := b.free[len(b.free)-1]
	b.free = b.free[:len(b.free)-1]
	if idx >= len(b.used) || b.used[idx] {
		ckf(false, "bucket size=%d double or invalid alloc idx=%d", b.size, idx)
		return -1, nil
	}
	b.used[idx] = true
	slab := idx / b.perSlab
	off := (idx % b.perSlab) * b.size
	return idx, b.slabs[slab][off : off+b.size]
}

func (b *bucket) release(idx int) {
	if idx < 0 || idx >= len(b.used) || !b.used[idx] {
		ckf(false, "bucket size=%d invalid free idx=%d", b.size, idx)
		return
	}
	b.used[idx] = false
	b.free = append(b.free, idx)
}

func (b *bucket) liveCount() int {
	n := 0
	for _, u := range b.used {
		if u {
			n++
		}
	}
	return n
}

type bHandle struct {
	bucket int
	idx    int
	buf    []byte
	sum    uint64
	snap   []byte
}

func bByte(bi, idx, off int, seed uint64) byte {
	return byte((seed*31 + uint64(bi)*17 + uint64(idx)*13 + uint64(off)*7) % 251)
}

func bFill(buf []byte, bi, idx int, seed uint64) {
	for i := range buf {
		buf[i] = bByte(bi, idx, i, seed)
	}
}

func bCheck(tag string, h *bHandle, seed uint64, gen int) {
	ckf(len(h.buf) == bSizes[h.bucket], "%s gen=%d bucket=%d len=%d want=%d", tag, gen, h.bucket, len(h.buf), bSizes[h.bucket])
	ckf(h.sum == fnvBytes(h.buf), "%s gen=%d bucket=%d fnv mismatch", tag, gen, h.bucket)
	ckf(h.sum == fnvBytes(h.snap), "%s gen=%d bucket=%d snapshot changed", tag, gen, h.bucket)
	for i := 0; i < len(h.buf); i += 7 {
		if h.buf[i] != bByte(h.bucket, h.idx, i, seed) {
			ckf(false, "%s gen=%d bucket=%d byte mismatch at=%d", tag, gen, h.bucket, i)
			break
		}
	}
}

func fnvBytes(b []byte) uint64 {
	h := uint64(1469598103934665603)
	for i := range b {
		h ^= uint64(b[i])
		h *= 1099511628211
	}
	return h
}

func main() {
	seed := loadSeed()
	buckets := make([]*bucket, len(bSizes))
	for i, s := range bSizes {
		buckets[i] = newBucket(s)
	}

	// 第一轮：每档分配固定数量并填充。
	handles := make([]bHandle, 0, len(bSizes)*bPerBucket)
	for bi := range buckets {
		for n := 0; n < bPerBucket; n++ {
			idx, buf := buckets[bi].alloc()
			ckf(buf != nil && len(buf) == bSizes[bi], "bucket=%d alloc %d len=%d", bi, n, len(buf))
			if idx >= 0 {
				off := (idx % buckets[bi].perSlab) * bSizes[bi]
				ckf(off%bSizes[bi] == 0, "bucket=%d idx=%d misaligned", bi, idx)
			}
			bFill(buf, bi, idx, seed)
			handles = append(handles, bHandle{bucket: bi, idx: idx, buf: buf, sum: fnvBytes(buf), snap: append([]byte(nil), buf...)})
		}
	}
	for bi := range buckets {
		ckf(buckets[bi].liveCount() == bPerBucket, "bucket=%d live=%d want=%d", bi, buckets[bi].liveCount(), bPerBucket)
	}
	gcStress()
	for i := range handles {
		bCheck("after-alloc", &handles[i], seed, 0)
	}

	// 释放每档一半槽位，快照旧内容，GC 后其余仍完好。
	freed := make([]bHandle, 0, len(handles)/2)
	kept := make([]bHandle, 0, len(handles))
	perBucketSeen := make(map[int]int, len(bSizes))
	for i := range handles {
		h := handles[i]
		c := perBucketSeen[h.bucket]
		perBucketSeen[h.bucket] = c + 1
		if c%2 == 0 {
			freed = append(freed, h)
			buckets[h.bucket].release(h.idx)
		} else {
			kept = append(kept, h)
		}
	}
	gcStress()
	for i := range kept {
		bCheck("after-free-GC", &kept[i], seed, 1)
	}
	for i := range freed {
		ckf(fnvBytes(freed[i].snap) == freed[i].sum, "freed snapshot bucket=%d idx=%d changed", freed[i].bucket, freed[i].idx)
	}

	// 重新分配等量槽位：必须取自 free-list，且与 kept 槽位不重叠。
	reused := make([]bHandle, 0, len(freed))
	live := make(map[[2]int]bool, len(kept)+len(freed))
	for i := range kept {
		live[[2]int{kept[i].bucket, kept[i].idx}] = true
	}
	for i := range freed {
		bi := freed[i].bucket
		idx, buf := buckets[bi].alloc()
		if live[[2]int{bi, idx}] {
			ckf(false, "bucket=%d idx=%d allocated while still live", bi, idx)
		}
		live[[2]int{bi, idx}] = true
		bFill(buf, bi, idx, seed)
		reused = append(reused, bHandle{bucket: bi, idx: idx, buf: buf, sum: fnvBytes(buf), snap: append([]byte(nil), buf...)})
	}
	gcStress()
	for i := range reused {
		bCheck("reused", &reused[i], seed, 2)
	}
	for i := range kept {
		bCheck("kept-after-reuse", &kept[i], seed, 2)
	}

	// 释放全部并重新分配同样数量：slab 数不得增长（free-list 应完全复用旧槽）。
	slabsBefore := make([]int, len(buckets))
	for bi := range buckets {
		slabsBefore[bi] = len(buckets[bi].slabs)
	}
	for i := range kept {
		buckets[kept[i].bucket].release(kept[i].idx)
	}
	for i := range reused {
		buckets[reused[i].bucket].release(reused[i].idx)
	}
	for bi := range buckets {
		ckf(buckets[bi].liveCount() == 0, "bucket=%d live after free-all=%d", bi, buckets[bi].liveCount())
		ckf(len(buckets[bi].free) == len(buckets[bi].used), "bucket=%d free-list len=%d used=%d", bi, len(buckets[bi].free), len(buckets[bi].used))
	}
	gcStress()
	for bi := range buckets {
		for n := 0; n < bPerBucket; n++ {
			idx, buf := buckets[bi].alloc()
			bFill(buf, bi, idx, seed)
			ckf(len(buf) == bSizes[bi], "bucket=%d realloc len=%d", bi, len(buf))
			_ = idx
		}
		ckf(len(buckets[bi].slabs) == slabsBefore[bi], "bucket=%d slabs grew after full recycle: %d -> %d", bi, slabsBefore[bi], len(buckets[bi].slabs))
		delete(live, [2]int{bi, 0})
	}
	gcStress()

	finish("l05_bucket_allocator", seed)
}
