// L05: 宽 map + 超过 32KB 的大 value（独立大对象）。每轮向 900 键的
// map[int64]*blob 写入 36–44KB 的大值，GC 后逐项校验；再删除 1/8、覆盖
// 1/8 为 ver=2，再次 GC；随后丢弃整张表，仅保留 pin 与旧版本值跨 GC 校验。
// 默认 2 轮，TORTURE_ROUNDS 可放大。
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
	mhKeys = 900
	mhMin  = 36 << 10
	mhSpan = 8 << 10
	mhPins = 4
)

var (
	failed int32
	gcSink [][]byte
)

type mhBlob struct {
	id   int64
	ver  int
	tag  string
	data []byte
	crc  uint32
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
		return hi
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
	for i := 0; i < 16; i++ {
		b := make([]byte, 64<<10)
		b[0] = byte(i)
		gcSink = append(gcSink[:0], b)
	}
	gcSink = gcSink[:0]
	runtime.GC()
	runtime.GC()
}

func mhSize(id int64, ver int, seed uint64) int {
	return mhMin + int((uint64(id)*37+uint64(ver)*101+seed*7)%uint64(mhSpan))
}

func mhByte(id int64, ver, j int, seed uint64) byte {
	return byte((seed*13 + uint64(id)*7 + uint64(ver)*29 + uint64(j)*3) % 251)
}

func mhMake(id int64, ver int, seed uint64) *mhBlob {
	n := mhSize(id, ver, seed)
	data := make([]byte, n)
	for j := range data {
		data[j] = mhByte(id, ver, j, seed)
	}
	return &mhBlob{
		id:   id,
		ver:  ver,
		tag:  fmt.Sprintf("k%d-v%d", id, ver),
		data: data,
		crc:  crc32.ChecksumIEEE(data),
	}
}

func mhCheck(what string, b *mhBlob, id int64, ver int, seed uint64) {
	if b == nil {
		ckf(false, "%s id=%d ver=%d missing", what, id, ver)
		return
	}
	ckf(b.id == id, "%s id=%d stored id=%d", what, id, b.id)
	ckf(b.ver == ver, "%s id=%d ver=%d want=%d", what, id, b.ver, ver)
	ckf(b.tag == fmt.Sprintf("k%d-v%d", id, ver), "%s id=%d tag=%q", what, id, b.tag)
	ckf(len(b.data) == mhSize(id, ver, seed), "%s id=%d len=%d want=%d", what, id, len(b.data), mhSize(id, ver, seed))
	if len(b.data) == 0 {
		return
	}
	ckf(b.data[0] == mhByte(id, ver, 0, seed), "%s id=%d first byte", what, id)
	ckf(b.data[len(b.data)-1] == mhByte(id, ver, len(b.data)-1, seed), "%s id=%d last byte", what, id)
	if c := crc32.ChecksumIEEE(b.data); c != b.crc {
		ckf(false, "%s id=%d crc=%08x want=%08x", what, id, c, b.crc)
	}
}

func main() {
	seed := loadSeed()
	rounds := envInt("TORTURE_ROUNDS", 2, 1, 6)

	pins := make([]*mhBlob, 0, rounds*mhPins)
	pinIDs := make([]int64, 0, rounds*mhPins)
	held := make([]*mhBlob, 0, rounds*mhKeys/8)
	var heldIDs []int64
	var heldVers []int
	var heldSeed uint64

	for round := 0; round < rounds; round++ {
		m := make(map[int64]*mhBlob)
		for id := int64(0); id < mhKeys; id++ {
			m[id] = mhMake(id, 1, seed)
		}
		ckf(len(m) == mhKeys, "round=%d map len=%d want=%d", round, len(m), mhKeys)

		gcStress()
		for id := int64(0); id < mhKeys; id++ {
			mhCheck(fmt.Sprintf("round=%d build", round), m[id], id, 1, seed)
		}

		// 删除 1/8，覆盖 1/8；保留被覆盖前的旧值指针。
		deleted := 0
		for id := int64(0); id < mhKeys; id++ {
			switch {
			case id%8 == 3:
				delete(m, id)
				deleted++
			case id%8 == 5:
				held = append(held, m[id])
				heldIDs = append(heldIDs, id)
				heldVers = append(heldVers, 1)
				heldSeed = seed
				m[id] = mhMake(id, 2, seed)
			}
		}
		ckf(len(m) == mhKeys-deleted, "round=%d after delete len=%d want=%d", round, len(m), mhKeys-deleted)

		// pin 住 4 个未删除、未覆盖的键。
		for _, id := range []int64{1, 2, 4, 6} {
			b := m[id]
			ckf(b != nil, "round=%d pin id=%d missing", round, id)
			if b != nil {
				pins = append(pins, b)
				pinIDs = append(pinIDs, id)
			}
		}

		gcStress()
		for id := int64(0); id < mhKeys; id++ {
			switch {
			case id%8 == 3:
				_, ok := m[id]
				ckf(!ok, "round=%d deleted id=%d still present", round, id)
			case id%8 == 5:
				mhCheck(fmt.Sprintf("round=%d overwritten", round), m[id], id, 2, seed)
			default:
				mhCheck(fmt.Sprintf("round=%d surviving", round), m[id], id, 1, seed)
			}
		}
		for i := range pins {
			mhCheck(fmt.Sprintf("round=%d pin after GC", round), pins[i], pinIDs[i], 1, seed)
		}
		for i, b := range held {
			mhCheck(fmt.Sprintf("round=%d held after overwrite+GC", round), b, heldIDs[i], heldVers[i], heldSeed)
		}

		// 整表丢弃，只留 pin 与 held，制造大块垃圾并 GC。
		m = nil
		gcStress()
		for i := range pins {
			mhCheck(fmt.Sprintf("round=%d pin after drop+GC", round), pins[i], pinIDs[i], 1, seed)
		}
		for i, b := range held {
			mhCheck(fmt.Sprintf("round=%d held after drop+GC", round), b, heldIDs[i], heldVers[i], heldSeed)
		}
	}

	gcStress()
	for i := range pins {
		mhCheck("final pin", pins[i], pinIDs[i], 1, seed)
	}
	for i, b := range held {
		mhCheck("final held", b, heldIDs[i], heldVers[i], heldSeed)
	}
	ckf(len(pins) == rounds*mhPins, "pinned total=%d want=%d", len(pins), rounds*mhPins)

	finish("l05_map_huge_value_bulk", seed)
}
