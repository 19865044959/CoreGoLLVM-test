// L05: 分桶切片 + 桶内字节 arena。20 个桶各自通过 append 增长 items 与 flat，
// 每条记录的 data 是桶内 flat 的子切片并持有独立 tag；跨 GC 校验别名与内容。
// 随后丢一半桶、只保留桶内少数记录跨 GC，再整表丢弃，验证子切片保活底层
// 桶存储、重建的新桶不会改写旧 pin 数据。
package main

import (
	"fmt"
	"hash/crc32"
	"os"
	"runtime"
	"strconv"
	"sync/atomic"
)

const (
	sbBucketCount = 20
	sbItemsPer    = 40
	sbPins        = 3
)

var (
	failed int32
	gcSink [][]byte
)

type sbTag struct {
	id   int
	name string
}

type sbItem struct {
	id   int
	off  int
	n    int
	data []byte
	tag  *sbTag
	crc  uint32
}

type sbBucket struct {
	idx   int
	items []*sbItem
	flat  []byte
}

type sbSnap struct {
	bucket int
	item   int
	data   []byte
	crc    uint32
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

func sbByte(bucket, item, j int, seed uint64) byte {
	return byte((seed*17 + uint64(bucket)*43 + uint64(item)*11 + uint64(j)*5) % 251)
}

func sbItemSize(bucket, item int, seed uint64) int {
	return 32 + int((seed+uint64(bucket)*17+uint64(item)*13)%96)
}

func sbCheckItem(what string, it *sbItem, bucket, item int, seed uint64) {
	if it == nil {
		ckf(false, "%s bucket=%d item=%d nil", what, bucket, item)
		return
	}
	wantN := sbItemSize(bucket, item, seed)
	ckf(it.id == bucket*1000+item, "%s bucket=%d item=%d id=%d", what, bucket, item, it.id)
	ckf(it.n == wantN, "%s bucket=%d item=%d n=%d want=%d", what, bucket, item, it.n, wantN)
	ckf(len(it.data) == wantN, "%s bucket=%d item=%d len=%d want=%d", what, bucket, item, len(it.data), wantN)
	ckf(it.tag != nil, "%s bucket=%d item=%d nil tag", what, bucket, item)
	if it.tag != nil {
		ckf(it.tag.id == it.id, "%s bucket=%d item=%d tag id=%d", what, bucket, item, it.tag.id)
		ckf(it.tag.name == fmt.Sprintf("t%d-%d", bucket, item), "%s bucket=%d item=%d tag name=%q", what, bucket, item, it.tag.name)
	}
	if len(it.data) > 0 {
		ckf(it.data[0] == sbByte(bucket, item, 0, seed), "%s bucket=%d item=%d first byte", what, bucket, item)
		ckf(it.data[len(it.data)-1] == sbByte(bucket, item, len(it.data)-1, seed), "%s bucket=%d item=%d last byte", what, bucket, item)
	}
	if c := crc32.ChecksumIEEE(it.data); c != it.crc {
		ckf(false, "%s bucket=%d item=%d crc=%08x want=%08x", what, bucket, item, c, it.crc)
	}
}

func main() {
	seed := loadSeed()
	buckets := make([]*sbBucket, 0, sbBucketCount)
	snaps := make([]sbSnap, 0, sbBucketCount*sbItemsPer)

	for b := 0; b < sbBucketCount; b++ {
		bkt := &sbBucket{idx: b}
		sizes := make([]int, sbItemsPer)
		total := 0
		for i := 0; i < sbItemsPer; i++ {
			sizes[i] = sbItemSize(b, i, seed)
			total += sizes[i]
		}
		bkt.flat = make([]byte, total)
		off := 0
		for i := 0; i < sbItemsPer; i++ {
			n := sizes[i]
			for j := 0; j < n; j++ {
				bkt.flat[off+j] = sbByte(b, i, j, seed)
			}
			tag := &sbTag{id: b*1000 + i, name: fmt.Sprintf("t%d-%d", b, i)}
			it := &sbItem{
				id:   b*1000 + i,
				off:  off,
				n:    n,
				data: bkt.flat[off : off+n],
				tag:  tag,
				crc:  crc32.ChecksumIEEE(bkt.flat[off : off+n]),
			}
			bkt.items = append(bkt.items, it)
			ckf(cap(bkt.items) >= len(bkt.items), "bucket=%d items cap=%d len=%d", b, cap(bkt.items), len(bkt.items))
			ckf(cap(bkt.flat) >= len(bkt.flat), "bucket=%d flat cap=%d len=%d", b, cap(bkt.flat), len(bkt.flat))
			ckf(&it.data[0] == &bkt.flat[off], "bucket=%d item=%d data does not alias flat", b, i)
			snaps = append(snaps, sbSnap{
				bucket: b,
				item:   i,
				data:   append([]byte(nil), it.data...),
				crc:    it.crc,
			})
			off += n
		}
		ckf(off == total, "bucket=%d flat used=%d total=%d", b, off, total)
		buckets = append(buckets, bkt)
		if b%5 == 4 {
			gcStress()
		}
	}

	gcStress()
	for b, bkt := range buckets {
		ckf(bkt.idx == b, "bucket=%d idx=%d", b, bkt.idx)
		for i, it := range bkt.items {
			sbCheckItem("build+GC", it, b, i, seed)
			ckf(&it.data[0] == &bkt.flat[it.off], "bucket=%d item=%d alias broken after GC", b, i)
		}
	}

	// 丢弃一半桶，仅保留每桶前 3 条记录；item.data 继续保活桶内 flat。
	var droppedPins []*sbItem
	var droppedAt [][2]int
	for b := 0; b < sbBucketCount; b += 2 {
		for i := 0; i < sbPins; i++ {
			droppedPins = append(droppedPins, buckets[b].items[i])
			droppedAt = append(droppedAt, [2]int{b, i})
		}
		buckets[b] = nil
	}
	gcStress()
	for k, it := range droppedPins {
		sbCheckItem("dropped-bucket pin+GC", it, droppedAt[k][0], droppedAt[k][1], seed)
	}
	for k, sn := range snaps {
		c := crc32.ChecksumIEEE(sn.data)
		ckf(c == sn.crc, "snapshot=%d bucket=%d item=%d crc", k, sn.bucket, sn.item)
	}

	// 整表丢弃，只留桶 1 的 4 条记录；GC 后仍有效。
	finalPins := append([]*sbItem(nil), buckets[1].items[:4]...)
	for b := range buckets {
		buckets[b] = nil
	}
	buckets = nil
	gcStress()
	for i, it := range finalPins {
		sbCheckItem("final pin+GC", it, 1, i, seed)
	}

	// 重建一个新桶并改写其 flat，不能影响旧 pin 的底层存储。
	fresh := &sbBucket{idx: 99}
	for i := 0; i < sbItemsPer; i++ {
		n := sbItemSize(0, i, seed)
		off := len(fresh.flat)
		fresh.flat = append(fresh.flat, make([]byte, n)...)
		for j := 0; j < n; j++ {
			fresh.flat[off+j] = 0xaa
		}
		fresh.items = append(fresh.items, &sbItem{id: 90000 + i, off: off, n: n, data: fresh.flat[off : off+n]})
	}
	gcStress()
	for i, it := range finalPins {
		sbCheckItem("final pin after rebuild+GC", it, 1, i, seed)
	}
	for i, it := range fresh.items {
		ckf(it.data[0] == 0xaa && it.data[len(it.data)-1] == 0xaa, "fresh bucket=%d item=%d content", 99, i)
	}

	finish("l05_slice_buckets_gc", seed)
}
