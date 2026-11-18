// L05: map 的 interface 键装箱：int64/string/可比较 struct/*指针四类键混装进
// map[any]*ikVal，值再持有装箱键、body 与链指针。插入/覆盖/删除/整表丢弃/重建
// 之间穿插垃圾分配与显式 GC，用新装箱的等值键查找，验证 interface 哈希/相等性
// 以及键值对象的跨 GC 存活。
package main

import (
	"fmt"
	"os"
	"runtime"
	"strconv"
	"sync/atomic"
)

const (
	ikPerKind = 750
	ikKinds   = 4
	ikTotal   = ikPerKind * ikKinds
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

type ikStructKey struct {
	a, b int32
	tag  string
}

type ikPtrKey struct {
	id int
}

type ikVal struct {
	kind int
	idx  int
	name string
	body []byte
	key  any
	link *ikVal
}

type ikEntry struct {
	key    any // 放入 map 的键（指针键与 lookup 同一指针）
	lookup any // 用等值但新装箱的键做查找
	kind   int
	idx    int
}

func ikName(kind, idx int, updated bool) string {
	suffix := ""
	if updated {
		suffix = "-u"
	}
	return fmt.Sprintf("v-%d-%04d%s", kind, idx, suffix)
}

func ikBody(kind, idx int, updated bool, seed uint64) []byte {
	n := 32 + int((uint64(idx)*7+uint64(kind)*31+seed)%129)
	b := make([]byte, n)
	ver := uint64(0)
	if updated {
		ver = 101
	}
	for i := range b {
		b[i] = byte((seed*13 + uint64(kind)*17 + uint64(idx)*19 + ver + uint64(i)*7) % 251)
	}
	return b
}

func ikCheck(tag string, e *ikEntry, v *ikVal, updated bool, seed uint64) {
	ckf(v != nil, "%s kind=%d idx=%d nil value", tag, e.kind, e.idx)
	if v == nil {
		return
	}
	ckf(v.kind == e.kind && v.idx == e.idx, "%s kind=%d idx=%d got kind=%d idx=%d", tag, e.kind, e.idx, v.kind, v.idx)
	ckf(v.name == ikName(e.kind, e.idx, updated), "%s kind=%d idx=%d name=%q", tag, e.kind, e.idx, v.name)
	ckf(len(v.body) == len(ikBody(e.kind, e.idx, updated, seed)), "%s kind=%d idx=%d body len", tag, e.kind, e.idx)
	for i := 0; i < len(v.body); i += 11 {
		if v.body[i] != ikBody(e.kind, e.idx, updated, seed)[i] {
			ckf(false, "%s kind=%d idx=%d body byte at=%d", tag, e.kind, e.idx, i)
			break
		}
	}
	// 装箱键本身也必须能正确相等比较（指针键要求同一身份）。
	ckf(v.key == e.key, "%s kind=%d idx=%d boxed key identity lost", tag, e.kind, e.idx)
}

func main() {
	seed := loadSeed()
	entries := make([]ikEntry, 0, ikTotal)
	ptrKeys := make([]*ikPtrKey, ikPerKind)

	for idx := 0; idx < ikPerKind; idx++ {
		k0 := int64(idx * 3)
		entries = append(entries, ikEntry{key: k0, lookup: int64(idx * 3), kind: 0, idx: idx})

		k1 := fmt.Sprintf("s-%04d-%d", idx, seed%7)
		entries = append(entries, ikEntry{key: k1, lookup: fmt.Sprintf("s-%04d-%d", idx, seed%7), kind: 1, idx: idx})

		k2 := ikStructKey{a: int32(idx), b: int32(idx*5 + 1), tag: fmt.Sprintf("t-%03d", idx%29)}
		entries = append(entries, ikEntry{key: k2, lookup: ikStructKey{a: int32(idx), b: int32(idx*5 + 1), tag: fmt.Sprintf("t-%03d", idx%29)}, kind: 2, idx: idx})

		k3 := &ikPtrKey{id: idx}
		ptrKeys[idx] = k3
		entries = append(entries, ikEntry{key: k3, lookup: k3, kind: 3, idx: idx})
	}

	m := make(map[any]*ikVal, ikTotal)
	var prev [ikKinds]*ikVal
	for i := range entries {
		e := &entries[i]
		v := &ikVal{kind: e.kind, idx: e.idx, name: ikName(e.kind, e.idx, false), body: ikBody(e.kind, e.idx, false, seed), key: e.key, link: prev[e.kind]}
		prev[e.kind] = v
		m[e.key] = v
	}

	gcStress()
	ckf(len(m) == ikTotal, "map len=%d want=%d", len(m), ikTotal)
	for i := range entries {
		e := &entries[i]
		v, ok := m[e.lookup]
		ckf(ok, "pre-GC lookup kind=%d idx=%d failed", e.kind, e.idx)
		if ok {
			ikCheck("pre-GC", e, v, false, seed)
		}
	}

	// 覆盖 1/3（保留旧值指针），删除 1/4。
	oldVals := make([]*ikVal, 0, ikTotal/3+1)
	for i := range entries {
		if i%3 != 0 {
			continue
		}
		e := &entries[i]
		oldVals = append(oldVals, m[e.key])
		nv := &ikVal{kind: e.kind, idx: e.idx, name: ikName(e.kind, e.idx, true), body: ikBody(e.kind, e.idx, true, seed), key: e.key, link: prev[e.kind]}
		prev[e.kind] = nv
		m[e.key] = nv
	}
	for i := range entries {
		if i%4 == 0 {
			delete(m, entries[i].key)
		}
	}

	gcStress()
	for i := range entries {
		e := &entries[i]
		v, ok := m[e.lookup]
		if i%4 == 0 {
			ckf(!ok, "deleted kind=%d idx=%d still present", e.kind, e.idx)
			continue
		}
		ckf(ok, "post-churn lookup kind=%d idx=%d failed", e.kind, e.idx)
		if ok {
			ikCheck("post-churn", e, v, i%3 == 0, seed)
		}
	}
	for i := range oldVals {
		if oldVals[i] == nil {
			ckf(false, "old value %d missing before update", i)
			continue
		}
		ikCheck("old-value", &ikEntry{key: oldVals[i].key, kind: oldVals[i].kind, idx: oldVals[i].idx}, oldVals[i], false, seed)
	}

	// 整表丢弃，旧值仍被 oldVals 持有；GC 后校验。
	m = nil
	gcStress()
	for i := range oldVals {
		ikCheck("old-after-drop", &ikEntry{key: oldVals[i].key, kind: oldVals[i].kind, idx: oldVals[i].idx}, oldVals[i], false, seed)
	}

	// 用相同的键（重新装箱）重建 map；查找必须命中。
	m2 := make(map[any]*ikVal, ikTotal)
	for i := range entries {
		e := &entries[i]
		m2[e.lookup] = &ikVal{kind: e.kind, idx: e.idx, name: ikName(e.kind, e.idx, false), body: ikBody(e.kind, e.idx, false, seed), key: e.lookup}
	}
	gcStress()
	ckf(len(m2) == ikTotal, "rebuilt map len=%d want=%d", len(m2), ikTotal)
	for i := range entries {
		e := &entries[i]
		v, ok := m2[e.lookup]
		ckf(ok, "rebuilt lookup kind=%d idx=%d failed", e.kind, e.idx)
		if ok {
			ckf(v.kind == e.kind && v.idx == e.idx, "rebuilt kind=%d idx=%d mismatch", e.kind, e.idx)
		}
	}
	// 新装箱的等值键（非指针类）也必须能命中。
	for i := 0; i < ikPerKind; i += 97 {
		if _, ok := m2[int64(i*3)]; !ok {
			ckf(false, "freshly boxed int64 key i=%d missed", i)
		}
		if _, ok := m2[fmt.Sprintf("s-%04d-%d", i, seed%7)]; !ok {
			ckf(false, "freshly boxed string key i=%d missed", i)
		}
		if _, ok := m2[ikStructKey{a: int32(i), b: int32(i*5 + 1), tag: fmt.Sprintf("t-%03d", i%29)}]; !ok {
			ckf(false, "freshly boxed struct key i=%d missed", i)
		}
		if _, ok := m2[ptrKeys[i]]; !ok {
			ckf(false, "pointer key i=%d missed", i)
		}
	}
	m2 = nil
	runtime.KeepAlive(ptrKeys)
	gcStress()

	finish("l05_map_iface_key_boxing", seed)
}
