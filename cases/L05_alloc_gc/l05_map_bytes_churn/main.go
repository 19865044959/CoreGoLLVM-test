// L05: 大 map[string][]byte 反复构建、整表校验、GC、整表丢弃、重建；
// 每轮保留少量副本跨轮持有，验证旧值不因 map 被回收而复用/破坏。
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
	mapKeyCount   = 1500
	defaultRounds = 3
)

var (
	failed int32
	gcSink [][]byte
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

func keyOf(id int) string {
	return fmt.Sprintf("key-%05d", id)
}

func valueSize(id, round int, seed uint64) int {
	return 96 + int((uint64(id)*13+uint64(round)*7+seed)%448)
}

func fillValue(b []byte, id, round int, seed uint64) {
	base := uint64(id)*37 + uint64(round)*101 + seed*13 + 5
	for i := range b {
		b[i] = byte((base + uint64(i)*7) % 251)
	}
}

func checkValue(v []byte, id, round int, seed uint64) bool {
	n := valueSize(id, round, seed)
	if len(v) != n {
		return false
	}
	base := uint64(id)*37 + uint64(round)*101 + seed*13 + 5
	if v[0] != byte(base%251) || v[n-1] != byte((base+uint64(n-1)*7)%251) {
		return false
	}
	for i := 0; i < n; i += 29 {
		if v[i] != byte((base+uint64(i)*7)%251) {
			return false
		}
	}
	return true
}

func main() {
	seed := loadSeed()
	rounds := envInt("TORTURE_ROUNDS", defaultRounds, 1, 20)

	var pinned [][]byte
	var pinnedID, pinnedRound []int
	var pinnedCRC []uint32

	for round := 0; round < rounds; round++ {
		m := make(map[string][]byte, mapKeyCount)
		total := 0
		for id := 0; id < mapKeyCount; id++ {
			n := valueSize(id, round, seed)
			v := make([]byte, n)
			fillValue(v, id, round, seed)
			m[keyOf(id)] = v
			total += n
			if id%300 == 0 {
				pinned = append(pinned, append([]byte(nil), v...))
				pinnedID = append(pinnedID, id)
				pinnedRound = append(pinnedRound, round)
				pinnedCRC = append(pinnedCRC, crc32.ChecksumIEEE(v))
			}
		}
		ckf(len(m) == mapKeyCount, "round=%d map len=%d want=%d", round, len(m), mapKeyCount)

		sumLen := 0
		var sumCRC uint32
		for id := 0; id < mapKeyCount; id++ {
			v, ok := m[keyOf(id)]
			if !ok {
				ckf(false, "round=%d key=%d missing before GC", round, id)
				continue
			}
			if !checkValue(v, id, round, seed) {
				ckf(false, "round=%d key=%d content mismatch before GC", round, id)
			}
			sumLen += len(v)
			sumCRC = crc32.Update(sumCRC, crc32.IEEETable, v)
		}
		ckf(sumLen == total, "round=%d total len=%d want=%d", round, sumLen, total)

		// map 仍被持有期间强制 GC：整张表与全部 value 必须存活。
		gcStress()
		var afterCRC uint32
		for id := 0; id < mapKeyCount; id += 97 {
			v := m[keyOf(id)]
			if !checkValue(v, id, round, seed) {
				ckf(false, "round=%d key=%d corrupted after GC", round, id)
			}
			afterCRC = crc32.Update(afterCRC, crc32.IEEETable, v)
		}
		ck(afterCRC != 0, "post-GC sample checksum is zero")

		// 整表丢弃 + 大块垃圾，确认旧表占用的内存可被回收而不影响保活副本。
		m = nil
		for i := 0; i < 32; i++ {
			junk := make([]byte, 64<<10)
			junk[0] = byte(round + i)
			gcSink = append(gcSink[:0], junk)
		}
		gcSink = gcSink[:0]
		runtime.GC()
		runtime.GC()

		for k := range pinned {
			if crc32.ChecksumIEEE(pinned[k]) != pinnedCRC[k] {
				ckf(false, "pinned round=%d key=%d checksum after map drop+GC", pinnedRound[k], pinnedID[k])
			}
			if !checkValue(pinned[k], pinnedID[k], pinnedRound[k], seed) {
				ckf(false, "pinned round=%d key=%d content after map drop+GC", pinnedRound[k], pinnedID[k])
			}
		}
	}

	// 全部轮次释放后重建一次完整表，确认堆可复用且期望值仍确定。
	final := make(map[string][]byte, mapKeyCount)
	for id := 0; id < mapKeyCount; id++ {
		v := make([]byte, valueSize(id, rounds, seed))
		fillValue(v, id, rounds, seed)
		final[keyOf(id)] = v
	}
	gcStress()
	for id := 0; id < mapKeyCount; id++ {
		if !checkValue(final[keyOf(id)], id, rounds, seed) {
			ckf(false, "final key=%d mismatch after rebuild", id)
		}
	}
	for k := range pinned {
		if crc32.ChecksumIEEE(pinned[k]) != pinnedCRC[k] {
			ckf(false, "pinned round=%d key=%d checksum at end", pinnedRound[k], pinnedID[k])
		}
	}
	runtime.KeepAlive(final)

	finish("l05_map_bytes_churn", seed)
}
