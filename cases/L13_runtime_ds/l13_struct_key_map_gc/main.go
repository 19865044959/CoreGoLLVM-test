// L13: 结构体 map key：嵌套 struct/数组/字符串、time.Time 与 [3]int64 的增删改查与 GC。
package main

import (
	"fmt"
	"math/rand"
	"os"
	"runtime"
	"strconv"
	"sync/atomic"
	"time"
)

type zone struct {
	x, y int64
}

type nodeKey struct {
	id   int64
	name string
	tags [2]string
	z    zone
}

type entry struct {
	id   int64
	name string
	salt int64
	pad  []byte
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

func newEntry(id, salt int64) *entry {
	e := &entry{id: id, name: fmt.Sprintf("e-%06d", id), salt: salt, pad: make([]byte, 16)}
	for i := range e.pad {
		e.pad[i] = byte(id*7 + int64(i*3) + salt)
	}
	return e
}

func validEntry(e *entry, id, salt int64) bool {
	if e == nil || e.id != id || e.salt != salt || e.name != fmt.Sprintf("e-%06d", id) {
		return false
	}
	for i := range e.pad {
		if e.pad[i] != byte(id*7+int64(i*3)+salt) {
			return false
		}
	}
	return true
}

// copyKey 用新分配的字符串重建 key，确保查找不依赖字符串头部身份。
func copyKey(k nodeKey) nodeKey {
	return nodeKey{
		id:   k.id,
		name: string(append([]byte(nil), k.name...)),
		tags: [2]string{
			string(append([]byte(nil), k.tags[0]...)),
			string(append([]byte(nil), k.tags[1]...)),
		},
		z: k.z,
	}
}

func main() {
	curSeed = envSeed()
	r := rand.New(rand.NewSource(curSeed))

	const n = 1500
	m := make(map[nodeKey]*entry, n)
	keys := make([]nodeKey, 0, n)
	vals := make([]*entry, 0, n)
	for i := 0; i < n; i++ {
		id := int64(i) + curSeed%1000
		k := nodeKey{
			id:   id,
			name: fmt.Sprintf("k-%05d-%02d", i, curSeed%97),
			tags: [2]string{fmt.Sprintf("a%d", i%5), fmt.Sprintf("b%d", i%7)},
			z:    zone{x: int64(i % 11), y: int64(i % 13)},
		}
		e := newEntry(id, curSeed)
		m[k] = e
		keys = append(keys, k)
		vals = append(vals, e)
	}
	ck(len(m) == n, "struct key: initial len")

	perm := r.Perm(n)
	validate := func(tag string) {
		for _, i := range perm {
			got := m[copyKey(keys[i])]
			if vals[i] == nil {
				if got != nil {
					ckSeed(false, fmt.Sprintf("struct key: %s deleted key resurrected at %d", tag, i))
					return
				}
				continue
			}
			if got != vals[i] || !validEntry(vals[i], vals[i].id, curSeed) {
				ckSeed(false, fmt.Sprintf("struct key: %s lookup/value mismatch at %d", tag, i))
				return
			}
		}
	}
	validate("build")
	gcStress()
	validate("after build GC")

	// 同 key 覆盖：长度不变，位置上的指针必须被替换。
	for i := 0; i < n; i += 4 {
		old := m[keys[i]]
		e := newEntry(int64(i)+500000, curSeed)
		m[keys[i]] = e
		ck(m[keys[i]] != old, "struct key: overwrite kept old pointer")
		vals[i] = e
	}
	ck(len(m) == n, "struct key: len after overwrite")
	validate("overwrite")
	gcStress()
	validate("after overwrite GC")

	// 删除每 3 个 key，GC 后确认缺席且邻近 key 不受影响。
	deleted := 0
	for i := 0; i < n; i += 3 {
		delete(m, keys[i])
		vals[i] = nil
		deleted++
	}
	ck(len(m) == n-deleted, "struct key: len after delete")
	validate("delete")
	for i := 0; i < n; i += 3 {
		if _, ok := m[copyKey(keys[i])]; ok {
			ckSeed(false, fmt.Sprintf("struct key: deleted key resurrected at %d", i))
			break
		}
	}
	gcStress()
	validate("after delete GC")

	// 重新插入并扩容到 3300 项。
	for i := 0; i < n; i += 3 {
		e := newEntry(int64(i)+900000, curSeed)
		m[keys[i]] = e
		vals[i] = e
	}
	total := n
	for i := n; i < 3300; i++ {
		id := int64(i) + 1000000
		k := nodeKey{
			id:   id,
			name: fmt.Sprintf("k-%05d-%02d", i, curSeed%97),
			tags: [2]string{fmt.Sprintf("a%d", i%5), fmt.Sprintf("b%d", i%7)},
			z:    zone{x: int64(i % 11), y: int64(i % 13)},
		}
		e := newEntry(id, curSeed)
		m[k] = e
		keys = append(keys, k)
		vals = append(vals, e)
		total++
	}
	ck(len(m) == total, "struct key: len after growth")
	perm = r.Perm(total)
	validate("growth")
	gcStress()
	validate("after growth GC")

	var sum int64
	var count int
	for _, e := range m {
		count++
		sum += e.id % 1000
	}
	var wantSum int64
	for _, e := range vals {
		wantSum += e.id % 1000
	}
	ck(count == total && sum == wantSum, "struct key: iteration aggregate")

	// 仅一个字段不同的 sibling key 必须互不干扰。
	a := keys[17]
	b := a
	b.z.y++
	b2 := a
	b2.tags[1] = "zzz-other"
	b3 := a
	b3.name = "zzz-name"
	_, okB := m[b]
	_, okB2 := m[b2]
	_, okB3 := m[b3]
	ck(!okB && !okB2 && !okB3, "struct key: sibling key aliased")
	ck(m[a] == vals[17], "struct key: original key lost after sibling probes")

	// [3]int64 数组 key。
	am := make(map[[3]int64]string, 512)
	akeys := make([][3]int64, 0, 512)
	for i := 0; i < 512; i++ {
		k := [3]int64{int64(i%17) - 8, int64(i/17) - 15, curSeed % 7}
		am[k] = fmt.Sprintf("v%03d", i)
		akeys = append(akeys, k)
	}
	ck(len(am) == 512, "array key: initial len")
	for i := 0; i < 512; i += 5 {
		am[akeys[i]] = fmt.Sprintf("w%03d", i)
	}
	for i := 0; i < 512; i += 7 {
		delete(am, akeys[i])
	}
	gcStress()
	for i := 0; i < 512; i++ {
		v, ok := am[akeys[i]]
		if i%7 == 0 {
			ck(!ok, "array key: deleted key visible")
		} else if !ok {
			ckSeed(false, fmt.Sprintf("array key: missing at %d", i))
		} else if i%5 == 0 && v != fmt.Sprintf("w%03d", i) {
			ckSeed(false, fmt.Sprintf("array key: stale value at %d", i))
		}
	}
	for i := 0; i < 512; i += 7 {
		am[akeys[i]] = fmt.Sprintf("v%03d", i)
	}
	ck(len(am) == 512, "array key: len after reinsert")

	// time.Time 结构体 key（含 Location 指针字段）。
	base := time.Unix(1700000000, 0).UTC()
	tm := make(map[time.Time]string, 256)
	tts := make([]time.Time, 0, 256)
	for i := 0; i < 256; i++ {
		t := base.Add(time.Duration(i*37) * time.Second)
		tm[t] = fmt.Sprintf("t%03d", i)
		tts = append(tts, t)
	}
	for i := 0; i < 256; i += 4 {
		delete(tm, tts[i])
	}
	gcStress()
	for i := 0; i < 256; i++ {
		_, ok := tm[base.Add(time.Duration(i*37)*time.Second)]
		if i%4 == 0 {
			ck(!ok, "time key: deleted key visible")
		} else if !ok {
			ckSeed(false, fmt.Sprintf("time key: missing at %d", i))
		}
	}
	for i := 0; i < 256; i += 4 {
		tm[base.Add(time.Duration(i*37)*time.Second)] = fmt.Sprintf("t%03d", i)
	}
	ck(len(tm) == 256, "time key: len after reinsert")
	gcStress()
	for i := 0; i < 256; i++ {
		if tm[base.Add(time.Duration(i*37)*time.Second)] != fmt.Sprintf("t%03d", i) {
			ckSeed(false, fmt.Sprintf("time key: value mismatch at %d", i))
			break
		}
	}

	gcStress()
	validate("final")
	ck(len(m) == total && len(am) == 512 && len(tm) == 256, "struct key: final lengths")
	runtime.KeepAlive(m)
	runtime.KeepAlive(am)
	runtime.KeepAlive(tm)
	finish("l13_struct_key_map_gc")
}
