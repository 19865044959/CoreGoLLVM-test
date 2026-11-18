// L05: map[int64]*entry 的删除/重插压力：分批删除、GC、以新版本重插、再删再插；
// 被删除但仍被局部变量引用的旧 entry 必须存活，最终按确定版本校验全部 key。
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
	deleteMapKeys  = 2500
	defaultRounds  = 2
	churnBatches   = 3
	entryValueSize = 128
)

var (
	failed int32
	gcSink [][]byte
)

type item struct {
	tag string
	crc uint32
}

type entry struct {
	ver  int
	data []byte
	it   *item
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

func newEntry(id, ver int, seed uint64) *entry {
	data := make([]byte, entryValueSize)
	base := uint64(id)*37 + uint64(ver)*101 + seed*13 + 1
	for i := range data {
		data[i] = byte((base + uint64(i)*7) % 251)
	}
	return &entry{
		ver:  ver,
		data: data,
		it:   &item{tag: fmt.Sprintf("v-%03d", ver), crc: crc32.ChecksumIEEE(data)},
	}
}

func checkEntry(e *entry, id, ver int, seed uint64) bool {
	if e == nil || e.ver != ver || e.it == nil || e.it.tag != fmt.Sprintf("v-%03d", ver) {
		return false
	}
	if len(e.data) != entryValueSize || crc32.ChecksumIEEE(e.data) != e.it.crc {
		return false
	}
	base := uint64(id)*37 + uint64(ver)*101 + seed*13 + 1
	for i := 0; i < entryValueSize; i += 31 {
		if e.data[i] != byte((base+uint64(i)*7)%251) {
			return false
		}
	}
	return true
}

// delBatch 删除满足条件的 key，返回被删 key 的确定顺序。
func delBatch(m map[int64]*entry, round, batch int) []int64 {
	deleted := make([]int64, 0, deleteMapKeys/churnBatches+8)
	for id := 0; id < deleteMapKeys; id++ {
		if (id*7+round*13+batch*29)%10 < 6 {
			key := int64(id)
			if _, ok := m[key]; ok {
				delete(m, key)
				deleted = append(deleted, key)
			}
		}
	}
	return deleted
}

func main() {
	seed := loadSeed()
	rounds := envInt("TORTURE_ROUNDS", defaultRounds, 1, 20)

	for round := 0; round < rounds; round++ {
		m := make(map[int64]*entry, deleteMapKeys)
		expectedVer := make([]int, deleteMapKeys)
		baseVer := round*100 + 1
		for id := 0; id < deleteMapKeys; id++ {
			m[int64(id)] = newEntry(id, baseVer, seed)
			expectedVer[id] = baseVer
		}
		gcStress()
		for id := 0; id < deleteMapKeys; id += 53 {
			if !checkEntry(m[int64(id)], id, expectedVer[id], seed) {
				ckf(false, "round=%d initial key=%d corrupted", round, id)
				break
			}
		}

		// 删除前保留一个引用：删除 + GC 后该对象仍必须可读。
		heldKey := int64(7)
		held := m[heldKey]
		heldCRC := crc32.ChecksumIEEE(held.data)
		heldVer := held.ver

		var pinnedEnts []*entry
		var pinnedIDs []int
		var pinnedVer []int
		for batch := 0; batch < churnBatches; batch++ {
			deleted := delBatch(m, round, batch)
			gcStress()
			for _, key := range deleted {
				if _, ok := m[key]; ok {
					ckf(false, "round=%d batch=%d key=%d still present after delete", round, batch, key)
				}
			}
			// 其余 key 内容不变。
			for id := 0; id < deleteMapKeys; id += 61 {
				key := int64(id)
				e, ok := m[key]
				if !ok {
					continue
				}
				if !checkEntry(e, id, expectedVer[id], seed) {
					ckf(false, "round=%d batch=%d survivor key=%d corrupted", round, batch, id)
					break
				}
			}

			// 被删 key 以更高版本重插；保留第一批重插值跨后续删除/GC 校验。
			newVer := baseVer + (batch+1)*10
			for _, key := range deleted {
				id := int(key)
				e := newEntry(id, newVer, seed)
				if batch == 0 && len(pinnedEnts) < 32 {
					pinnedEnts = append(pinnedEnts, e)
					pinnedIDs = append(pinnedIDs, id)
					pinnedVer = append(pinnedVer, newVer)
				}
				m[key] = e
				expectedVer[id] = newVer
			}
			runtime.GC()
		}

		gcStress()
		for id := 0; id < deleteMapKeys; id++ {
			e, ok := m[int64(id)]
			if !ok {
				ckf(false, "round=%d key=%d missing after churn", round, id)
				continue
			}
			if !checkEntry(e, id, expectedVer[id], seed) {
				ckf(false, "round=%d key=%d wrong version/content after churn", round, id)
			}
		}
		ck(len(m) == deleteMapKeys, "map size after full reinsertion")
		if held == nil || crc32.ChecksumIEEE(held.data) != heldCRC || held.ver != heldVer {
			ckf(false, "round=%d held deleted entry corrupted after churn", round)
		}
		for k := range pinnedEnts {
			if !checkEntry(pinnedEnts[k], pinnedIDs[k], pinnedVer[k], seed) {
				ckf(false, "round=%d pinned reinserted key=%d ver=%d corrupted", round, pinnedIDs[k], pinnedVer[k])
			}
		}

		m = nil
		pinnedEnts = nil
		gcStress()
	}

	finish("l05_map_delete_reinsert", seed)
}
