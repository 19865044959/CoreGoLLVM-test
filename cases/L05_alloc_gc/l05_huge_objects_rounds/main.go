// L05: 1/2/4/8MB 超大对象多轮分配、写满、GC、保留副本、释放、再重建；
// 每轮校验长度、边界字节与 CRC，验证大对象走独立 span 时的存活与回收。
package main

import (
	"fmt"
	"hash/crc32"
	"os"
	"runtime"
	"strconv"
	"sync/atomic"
)

const defaultRounds = 2

var (
	failed int32
	gcSink [][]byte
	sizes  = []int{1 << 20, 2 << 20, 4 << 20, 8 << 20}
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
		b := make([]byte, 4096)
		b[0] = byte(i)
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

func envInt(name string, def, lo, hi int) int {
	s := os.Getenv(name)
	if s == "" {
		return def
	}
	n, err := strconv.Atoi(s)
	if err != nil || n < lo {
		return def
	}
	if n > hi {
		n = hi
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

func fillHuge(b []byte, round, class int, seed uint64) {
	base := uint64(round)*101 + uint64(class)*37 + seed*13 + 7
	for i := range b {
		b[i] = byte((base + uint64(i)*7) % 251)
	}
}

func hugeByte(round, class, i int, seed uint64) byte {
	base := uint64(round)*101 + uint64(class)*37 + seed*13 + 7
	return byte((base + uint64(i)*7) % 251)
}

func checkHuge(b []byte, round, class int, seed uint64, wantCRC uint32) bool {
	if len(b) != sizes[class] {
		return false
	}
	n := len(b)
	if b[0] != hugeByte(round, class, 0, seed) ||
		b[n/2] != hugeByte(round, class, n/2, seed) ||
		b[n-1] != hugeByte(round, class, n-1, seed) {
		return false
	}
	for i := 0; i < n; i += (1 << 20) - 1 {
		if b[i] != hugeByte(round, class, i, seed) {
			return false
		}
	}
	return crc32.ChecksumIEEE(b) == wantCRC
}

func main() {
	seed := loadSeed()
	rounds := envInt("TORTURE_ROUNDS", defaultRounds, 1, 8)

	type pinnedObj struct {
		b     []byte
		round int
		class int
		crc   uint32
	}
	var pinned []pinnedObj

	for round := 0; round < rounds; round++ {
		objs := make([][]byte, len(sizes))
		crcref := make([]uint32, len(sizes))
		for class, n := range sizes {
			b := make([]byte, n)
			fillHuge(b, round, class, seed)
			objs[class] = b
			crcref[class] = crc32.ChecksumIEEE(b)
			ckf(len(b) == n, "round=%d class=%d alloc len=%d want=%d", round, class, len(b), n)
			// 分配下一个更大对象前制造垃圾，让大对象在 GC 中与新分配交错。
			junk := make([]byte, 256<<10)
			junk[0] = byte(class)
			gcSink = append(gcSink[:0], junk)
		}
		gcSink = gcSink[:0]
		gcStress()

		for class := range objs {
			if !checkHuge(objs[class], round, class, seed, crcref[class]) {
				ckf(false, "round=%d class=%d corrupted while alive after GC", round, class)
			}
		}

		// 每个尺寸各保留一份副本，随后释放本轮大对象并 GC。
		for class := range objs {
			cp := append([]byte(nil), objs[class]...)
			pinned = append(pinned, pinnedObj{b: cp, round: round, class: class, crc: crcref[class]})
		}
		objs = nil
		for i := 0; i < 8; i++ {
			junk := make([]byte, 512<<10)
			junk[0] = byte(round*8 + i)
			gcSink = append(gcSink[:0], junk)
		}
		gcSink = gcSink[:0]
		runtime.GC()
		runtime.GC()

		// 大对象已释放，保留副本必须完整。
		for k := range pinned {
			p := &pinned[k]
			if len(p.b) != sizes[p.class] || crc32.ChecksumIEEE(p.b) != p.crc {
				ckf(false, "pinned round=%d class=%d corrupted after release+GC", p.round, p.class)
			}
			if !checkHuge(p.b, p.round, p.class, seed, p.crc) {
				ckf(false, "pinned round=%d class=%d content after release+GC", p.round, p.class)
			}
		}

		// 重建一次全部尺寸（下一轮会再次分配并释放），GC 后所有保留项仍完好。
		rebuilt := make([][]byte, len(sizes))
		rebuiltCRC := make([]uint32, len(sizes))
		for class, n := range sizes {
			rebuilt[class] = make([]byte, n)
			fillHuge(rebuilt[class], round, class, seed)
			rebuiltCRC[class] = crc32.ChecksumIEEE(rebuilt[class])
		}
		gcStress()
		for class := range rebuilt {
			if !checkHuge(rebuilt[class], round, class, seed, rebuiltCRC[class]) {
				ckf(false, "round=%d class=%d rebuilt corrupted", round, class)
			}
		}
		rebuilt = nil
	}

	gcStress()
	for k := range pinned {
		p := &pinned[k]
		if crc32.ChecksumIEEE(p.b) != p.crc {
			ckf(false, "final pinned round=%d class=%d crc", p.round, p.class)
		}
	}
	ck(len(pinned) == rounds*len(sizes), "pinned count accounting")
	runtime.KeepAlive(pinned)

	finish("l05_huge_objects_rounds", seed)
}
