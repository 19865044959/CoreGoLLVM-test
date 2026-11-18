// L05: map 中存放大量独立分配的小对象（指针/字符串/切片/peer 指针混合）。
// 填表、建 peer 环、显式 GC、覆盖 1/3、删除 1/7，再整表丢弃并制造垃圾，
// 校验存活项、被覆盖旧对象与 peer 目标在 GC 后仍完整。
package main

import (
	"fmt"
	"os"
	"runtime"
	"strconv"
	"sync/atomic"
)

const (
	objCount      = 12000
	defaultRounds = 2
)

var (
	failed int32
	gcSink [][]byte
)

type peer struct {
	id   int64
	name string
}

type small struct {
	id     int64
	weight int64
	name   string
	data   []byte
	link   *small
	tag    *peer
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

func smallName(id, round int) string {
	return fmt.Sprintf("rec-%05d-%02d", id, round)
}

func dataLen(id, round int, seed uint64) int {
	return 12 + int((uint64(id)*13+uint64(round)*7+seed)%53)
}

func smallByte(id, round, i int, seed uint64) byte {
	base := uint64(id)*37 + uint64(round)*101 + seed*13 + 1
	return byte((base + uint64(i)*7) % 251)
}

func makeSmall(id, round int, seed uint64) *small {
	n := dataLen(id, round, seed)
	b := make([]byte, n)
	for i := range b {
		b[i] = smallByte(id, round, i, seed)
	}
	return &small{
		id:     int64(id),
		weight: int64(id)*3 + int64(round)*5 - int64(seed%17),
		name:   smallName(id, round),
		data:   b,
		tag:    &peer{id: int64(id)*2 + 1, name: fmt.Sprintf("peer-%05d", id)},
	}
}

func checkSmall(s *small, id, round int, seed uint64) bool {
	if s == nil || s.id != int64(id) || s.name != smallName(id, round) {
		return false
	}
	if s.weight != int64(id)*3+int64(round)*5-int64(seed%17) {
		return false
	}
	n := dataLen(id, round, seed)
	if len(s.data) != n {
		return false
	}
	for i := 0; i < n; i++ {
		if s.data[i] != smallByte(id, round, i, seed) {
			return false
		}
	}
	if s.tag == nil || s.tag.id != int64(id)*2+1 || s.tag.name != fmt.Sprintf("peer-%05d", id) {
		return false
	}
	return true
}

func main() {
	seed := loadSeed()
	rounds := envInt("TORTURE_ROUNDS", defaultRounds, 1, 8)

	type pinned struct {
		s     *small
		id    int
		round int
	}
	var pins []pinned
	anchor := makeSmall(objCount+7, 0, seed)

	for round := 0; round < rounds; round++ {
		m := make(map[int64]*small, objCount)
		for id := 0; id < objCount; id++ {
			m[int64(id)] = makeSmall(id, round, seed)
			if id%500 == 0 {
				gcStress()
			}
		}
		// 建立 peer 环：每个对象通过 link 指向另一个小对象。
		for id := 0; id < objCount; id++ {
			m[int64(id)].link = m[int64((id*17+3)%objCount)]
		}
		ckf(len(m) == objCount, "round=%d map len=%d want=%d", round, len(m), objCount)

		gcStress()

		ok := true
		for id := 0; id < objCount; id++ {
			s := m[int64(id)]
			if !checkSmall(s, id, round, seed) {
				ckf(false, "round=%d id=%d corrupted after fill+GC", round, id)
				ok = false
				break
			}
			lid := (id*17 + 3) % objCount
			if s.link == nil || s.link.id != int64(lid) || s.link.name != smallName(lid, round) {
				ckf(false, "round=%d id=%d link corrupted after fill+GC", round, id)
				ok = false
				break
			}
		}
		ck(ok, "initial small-object table intact")

		// 覆盖 id%3==0 的项，删除 id%7==0 的项（删除优先）。
		for id := 0; id < objCount; id++ {
			if id%7 == 0 {
				delete(m, int64(id))
				continue
			}
			if id%3 == 0 {
				old := m[int64(id)]
				if id%97 == 0 {
					pins = append(pins, pinned{s: old, id: id, round: round})
				}
				m[int64(id)] = makeSmall(id, round+100, seed)
				m[int64(id)].link = anchor
			}
		}
		wantLen := 0
		for id := 0; id < objCount; id++ {
			if id%7 != 0 {
				wantLen++
			}
		}
		ckf(len(m) == wantLen, "round=%d after mutate len=%d want=%d", round, len(m), wantLen)

		gcStress()
		for id := 0; id < objCount; id++ {
			if id%7 == 0 {
				if _, exists := m[int64(id)]; exists {
					ckf(false, "round=%d id=%d deleted key still present", round, id)
				}
				continue
			}
			s := m[int64(id)]
			if id%3 == 0 {
				if !checkSmall(s, id, round+100, seed) {
					ckf(false, "round=%d id=%d overwritten value corrupted", round, id)
				}
				if s.link != anchor {
					ckf(false, "round=%d id=%d overwritten link identity lost", round, id)
				}
				continue
			}
			if !checkSmall(s, id, round, seed) {
				ckf(false, "round=%d id=%d survivor corrupted", round, id)
				continue
			}
			lid := (id*17 + 3) % objCount
			if !checkSmall(s.link, lid, round, seed) {
				ckf(false, "round=%d id=%d peer target corrupted after delete/overwrite", round, id)
			}
		}
		if !checkSmall(anchor, objCount+7, 0, seed) {
			ckf(false, "round=%d shared anchor corrupted", round)
		}

		// 保留少量对象后丢弃整张表：所有 pin 必须跨 GC 存活。
		saved := []*small{m[int64(1)], m[int64(2)], m[int64(4)], m[int64(5)], anchor}
		m = nil
		for i := 0; i < 16; i++ {
			junk := make([]byte, 512<<10)
			junk[0] = byte(round*16 + i)
			gcSink = append(gcSink[:0], junk)
		}
		gcSink = gcSink[:0]
		runtime.GC()
		runtime.GC()

		for _, s := range saved {
			if s == nil {
				ckf(false, "round=%d saved object nil after map drop", round)
			}
		}
		if !checkSmall(saved[0], 1, round, seed) ||
			!checkSmall(saved[1], 2, round, seed) ||
			!checkSmall(saved[2], 4, round, seed) ||
			!checkSmall(saved[3], 5, round, seed) {
			ckf(false, "round=%d saved objects corrupted after map drop+GC", round)
		}
		for k := range pins {
			p := &pins[k]
			if !checkSmall(p.s, p.id, p.round, seed) {
				ckf(false, "pinned id=%d round=%d corrupted after drop+GC", p.id, p.round)
			}
		}
		runtime.KeepAlive(saved)
	}

	gd := make(map[int64]*small, objCount)
	for id := 0; id < objCount; id++ {
		gd[int64(id)] = makeSmall(id, rounds+1, seed)
	}
	gcStress()
	for id := 0; id < objCount; id++ {
		if !checkSmall(gd[int64(id)], id, rounds+1, seed) {
			ckf(false, "final rebuild id=%d corrupted", id)
			break
		}
	}
	runtime.KeepAlive(gd)

	finish("l05_map_small_objects", seed)
}
