// L13: map[string][]*T 二级索引在高频追加/截断/删除/重建与 GC 下的指针保活。
package main

import (
	"fmt"
	"math/rand"
	"os"
	"runtime"
	"sort"
	"strconv"
	"sync/atomic"
)

type record struct {
	id   int
	tag  string
	name string
	pad  [40]byte
}

type snapshot struct {
	tag string
	s   []*record
	ids []int
}

var (
	failed  int32
	gcSink  [][]byte
	curSeed int64
)

func ck(ok bool, msg string) {
	if !ok {
		atomic.AddInt32(&failed, 1)
		fmt.Println("FAIL:", msg)
	}
}

func ckSeed(ok bool, msg string) {
	if !ok {
		atomic.AddInt32(&failed, 1)
		fmt.Printf("FAIL: %s (seed=%d)\n", msg, curSeed)
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

func finish(name string) {
	if n := atomic.LoadInt32(&failed); n != 0 {
		fmt.Printf("FAILED %s (%d failed checks)\n", name, n)
		os.Exit(1)
	}
	fmt.Println("ALL PASS " + name)
}

func envSeed() int64 {
	s := os.Getenv("UAF_SEED")
	if s == "" {
		return 1
	}
	v, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return 1
	}
	return v
}

func tortureRounds(def int) int {
	s := os.Getenv("TORTURE_ROUNDS")
	if s == "" {
		return def
	}
	n, err := strconv.Atoi(s)
	if err != nil || n <= 0 {
		return def
	}
	if n > 500 {
		n = 500
	}
	return n
}

func newRecord(tag string, id int) *record {
	r := &record{id: id, tag: tag, name: tag + ":" + strconv.Itoa(id)}
	for i := range r.pad {
		r.pad[i] = byte(id*31 + i*7)
	}
	return r
}

func appendRecs(m map[string][]*record, model map[string][]int, tag string, n int, next *int) {
	if n <= 0 {
		return
	}
	batch := make([]*record, n)
	ids := make([]int, n)
	for i := 0; i < n; i++ {
		id := *next
		*next = id + 1
		batch[i] = newRecord(tag, id)
		ids[i] = id
	}
	m[tag] = append(m[tag], batch...)
	model[tag] = append(model[tag], ids...)
}

func sortedRecordKeys(m map[string][]*record) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func sortedIntKeys(m map[string][]int) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func checkAll(m map[string][]*record, model map[string][]int, round int) {
	roundTag := fmt.Sprintf("round=%d", round)
	ckSeed(len(m) == len(model), roundTag+": outer map size differs from model")
	mk := sortedRecordKeys(m)
	zk := sortedIntKeys(model)
	if len(mk) != len(zk) {
		ckSeed(false, roundTag+": outer key count differs from model")
		return
	}
	for i := range mk {
		if mk[i] != zk[i] {
			ckSeed(false, roundTag+": outer keys differ from model")
			return
		}
	}
	var total int64
	var sum int64
	for _, tag := range mk {
		got := m[tag]
		want := model[tag]
		if len(got) != len(want) {
			ckSeed(false, roundTag+": tag "+tag+" length differs from model")
			continue
		}
		for i := range got {
			total++
			sum += int64(want[i])
			if got[i] == nil {
				ckSeed(false, roundTag+": tag "+tag+" nil record")
				break
			}
			if got[i].id != want[i] || got[i].tag != tag {
				ckSeed(false, roundTag+": tag "+tag+" record identity mismatch")
				break
			}
			if got[i].name != tag+":"+strconv.Itoa(want[i]) {
				ckSeed(false, roundTag+": tag "+tag+" record name mismatch")
				break
			}
			for j := range got[i].pad {
				if got[i].pad[j] != byte(want[i]*31+j*7) {
					ckSeed(false, roundTag+": tag "+tag+" record pad corruption")
					break
				}
			}
		}
	}
	var mt, ms int64
	for _, tag := range mk {
		for _, id := range model[tag] {
			mt++
			ms += int64(id)
		}
	}
	if total != mt || sum != ms {
		ckSeed(false, roundTag+": record totals differ from model")
	}
}

func main() {
	curSeed = envSeed()
	rounds := tortureRounds(4)
	r := rand.New(rand.NewSource(curSeed))

	m := make(map[string][]*record)
	model := make(map[string][]int)
	next := 0
	snaps := make([]snapshot, 0, 48)
	frozen := make(map[string]bool)

	for i := 0; i < 40; i++ {
		tag := fmt.Sprintf("t%02d", i)
		appendRecs(m, model, tag, r.Intn(6), &next)
	}
	gcStress()
	checkAll(m, model, -1)
	ckSeed(len(m) > 0, "initial map is empty")

	for round := 0; round < rounds; round++ {
		for op := 0; op < 240; op++ {
			tag := fmt.Sprintf("t%02d", r.Intn(64))
			kind := r.Intn(6)

			// 在可能改写该 tag 之前保存旧切片头，GC 后它仍应指向旧内容。
			// 冻结该 tag 的改写（仅允许整体删除），避免复用底层数组覆盖快照内容。
			if op%47 == 0 && len(snaps) < 48 && !frozen[tag] {
				if s, ok := m[tag]; ok && len(s) > 0 {
					ids := append([]int(nil), model[tag]...)
					snaps = append(snaps, snapshot{tag: tag, s: s, ids: ids})
					frozen[tag] = true
					continue
				}
			}
			if frozen[tag] {
				if kind == 3 {
					delete(m, tag)
					delete(model, tag)
				}
				continue
			}

			switch kind {
			case 0, 1:
				appendRecs(m, model, tag, 1+r.Intn(4), &next)
			case 2:
				if s, ok := m[tag]; ok {
					keep := 0
					if len(s) > 0 {
						keep = r.Intn(len(s) + 1)
					}
					m[tag] = s[:keep]
					model[tag] = model[tag][:keep]
				}
			case 3:
				delete(m, tag)
				delete(model, tag)
			case 4:
				delete(m, tag)
				delete(model, tag)
				appendRecs(m, model, tag, 1+r.Intn(3), &next)
			case 5:
				appendRecs(m, model, tag, 5+r.Intn(10), &next)
			}
		}
		gcStress()
		checkAll(m, model, round)
	}

	// 快照切片头保活的旧底层数组跨多轮 GC 后内容必须仍然正确。
	gcStress()
	for i := range snaps {
		sn := &snaps[i]
		if len(sn.s) != len(sn.ids) {
			ckSeed(false, "snapshot length changed after GC")
			continue
		}
		for j := range sn.s {
			if sn.s[j] == nil {
				ckSeed(false, "snapshot record became nil after GC")
				break
			}
			if sn.s[j].id != sn.ids[j] || sn.s[j].tag != sn.tag {
				ckSeed(false, "snapshot record content changed after GC")
				break
			}
			if sn.s[j].name != sn.tag+":"+strconv.Itoa(sn.ids[j]) {
				ckSeed(false, "snapshot record name changed after GC")
				break
			}
		}
	}

	// nil / 空 map 的读取与删除语义。
	var nilMap map[string][]*record
	ck(len(nilMap) == 0, "nil map length")
	ck(len(nilMap["absent"]) == 0, "nil map value")
	delete(nilMap, "absent")
	empty := map[string][]*record{}
	delete(empty, "absent")
	ck(len(empty) == 0, "empty map after delete of missing key")

	gcStress()
	checkAll(m, model, rounds)
	ckSeed(len(snaps) > 0, "no snapshots captured")

	runtime.KeepAlive(m)
	runtime.KeepAlive(model)
	runtime.KeepAlive(snaps)
	finish("l13_map_string_slice_ptr_churn")
}
