// L13: maps/slices 标准库容器：Clone 浅拷贝、Copy 覆盖、Equal/EqualFunc、DeleteFunc 与 slices 增删排序。
package main

import (
	"fmt"
	"maps"
	"math/rand"
	"os"
	"runtime"
	"slices"
	"sort"
	"strconv"
	"sync/atomic"
)

type leaf struct {
	id   int
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

func newLeaf(id int, salt int64) *leaf {
	l := &leaf{id: id, name: fmt.Sprintf("leaf-%05d", id), salt: salt, pad: make([]byte, 12)}
	for i := range l.pad {
		l.pad[i] = byte(id*19 + i*7 + int(salt))
	}
	return l
}

func validLeaf(l *leaf, id int, salt int64) bool {
	if l == nil || l.id != id || l.salt != salt || l.name != fmt.Sprintf("leaf-%05d", id) || len(l.pad) != 12 {
		return false
	}
	for i := range l.pad {
		if l.pad[i] != byte(id*19+i*7+int(salt)) {
			return false
		}
	}
	return true
}

func sameLeaf(a, b *leaf) bool {
	if a == nil || b == nil {
		return a == b
	}
	if a.id != b.id || a.name != b.name || a.salt != b.salt || len(a.pad) != len(b.pad) {
		return false
	}
	for i := range a.pad {
		if a.pad[i] != b.pad[i] {
			return false
		}
	}
	return true
}

func main() {
	curSeed = envSeed()
	rnd := rand.New(rand.NewSource(curSeed))

	const n = 500
	orig := make(map[string]*leaf, n)
	keys := make([]string, 0, n)
	for i := 0; i < n; i++ {
		k := fmt.Sprintf("k-%05d-%02d", i, curSeed%17)
		orig[k] = newLeaf(i, curSeed)
		keys = append(keys, k)
	}
	ck(len(orig) == n, "maps: initial len")

	// 1) maps.Clone 是浅拷贝：value 指针相同，增删互不影响。
	clone := maps.Clone(orig)
	ck(len(clone) == n, "maps: clone len")
	for _, k := range keys {
		if clone[k] != orig[k] {
			ck(false, "maps: clone value pointer changed")
			break
		}
	}
	clone["extra"] = newLeaf(9001, curSeed)
	delete(clone, keys[0])
	clone[keys[1]].name = "mutated"
	ck(len(orig) == n, "maps: clone add/delete leaked to original")
	if _, ok := orig["extra"]; ok {
		ck(false, "maps: clone extra key visible in original")
	}
	if orig[keys[1]].name != "mutated" {
		ck(false, "maps: clone did not share value object")
	}
	orig[keys[1]].name = fmt.Sprintf("leaf-%05d", orig[keys[1]].id)
	gcStress()
	for _, k := range keys {
		if orig[k] == nil || clone[k] == nil && k != keys[0] {
			ckSeed(false, fmt.Sprintf("maps: clone entry lost at %s", k))
			break
		}
	}

	// 2) Equal 比较 value 指针，EqualFunc 比较内容。
	same := maps.Clone(orig)
	ck(maps.Equal(orig, same), "maps: Equal same pointers")
	structural := maps.Clone(orig)
	old := structural[keys[2]]
	structural[keys[2]] = newLeaf(old.id, old.salt)
	ck(!maps.Equal(orig, structural), "maps: Equal ignored pointer difference")
	ck(maps.EqualFunc(orig, structural, sameLeaf), "maps: EqualFunc content mismatch")
	ck(!maps.Equal(orig, structural), "maps: Equal changed after EqualFunc")
	ck(maps.EqualFunc(orig, same, func(a, b *leaf) bool { return a == b }), "maps: EqualFunc identity")
	var nilMap map[string]*leaf
	empty := map[string]*leaf{}
	ck(maps.Equal(nilMap, empty), "maps: nil vs empty Equal")

	// 3) maps.Copy：重叠 key 覆盖，dst 独有 key 保留。
	dst := make(map[string]*leaf, n+50)
	for i := 0; i < 100; i++ {
		dst[keys[i]] = newLeaf(-i-1, curSeed)
	}
	for i := 0; i < 50; i++ {
		dst[fmt.Sprintf("zzz-%03d", i)] = newLeaf(20000+i, curSeed)
	}
	maps.Copy(dst, orig)
	ck(len(dst) == n+50, "maps: Copy len")
	for i := 0; i < 100; i++ {
		if dst[keys[i]] != orig[keys[i]] {
			ck(false, "maps: Copy did not overwrite overlap")
			break
		}
	}
	for i := 0; i < 50; i++ {
		k := fmt.Sprintf("zzz-%03d", i)
		if !validLeaf(dst[k], 20000+i, curSeed) {
			ck(false, "maps: Copy dropped dst-only key")
			break
		}
	}
	emptyDst := make(map[string]*leaf, n)
	maps.Copy(emptyDst, orig)
	ck(len(emptyDst) == n && maps.EqualFunc(emptyDst, orig, sameLeaf), "maps: Copy into empty")
	maps.Copy(emptyDst, map[string]*leaf{})
	ck(len(emptyDst) == n, "maps: Copy empty source")
	gcStress()
	for _, k := range keys {
		if !validLeaf(emptyDst[k], orig[k].id, curSeed) {
			ckSeed(false, fmt.Sprintf("maps: Copy payload corrupt at %s", k))
			break
		}
	}

	// 4) maps.DeleteFunc + Keys 迭代器（排序后比较）。
	filtered := maps.Clone(orig)
	want := 0
	for _, v := range orig {
		if v.id%3 != 0 {
			want++
		}
	}
	maps.DeleteFunc(filtered, func(_ string, v *leaf) bool { return v.id%3 == 0 })
	ck(len(filtered) == want, "maps: DeleteFunc count")
	for _, k := range keys {
		_, ok := filtered[k]
		if (orig[k].id%3 == 0) == ok {
			ckSeed(false, fmt.Sprintf("maps: DeleteFunc membership at %s", k))
			break
		}
	}
	gotKeys := make([]string, 0, len(filtered))
	for k := range maps.Keys(filtered) {
		gotKeys = append(gotKeys, k)
	}
	sort.Strings(gotKeys)
	ck(len(gotKeys) == len(filtered), "maps: Keys count")
	for i := 1; i < len(gotKeys); i++ {
		if gotKeys[i-1] > gotKeys[i] {
			ck(false, "maps: sorted keys order")
			break
		}
	}

	// 5) slices.Clone/Compact/Delete/Insert/Replace/Grow/SortFunc。
	sl := make([]*leaf, 300)
	for i := range sl {
		sl[i] = newLeaf(i, curSeed)
	}
	slc := slices.Clone(sl)
	ck(len(slc) == len(sl), "slices: Clone len")
	slc[0] = newLeaf(7777, curSeed)
	ck(sl[0] != slc[0], "slices: Clone element alias")
	slc[1].name = "mutated"
	ck(sl[1].name == "mutated", "slices: Clone lost object sharing")
	sl[1].name = fmt.Sprintf("leaf-%05d", sl[1].id)

	nums := []int{1, 1, 2, 2, 2, 3, 1, 1}
	comp := slices.Compact(nums)
	ck(slices.Equal(comp, []int{1, 2, 3, 1}), "slices: Compact result")

	delBase := make([]*leaf, 100)
	for i := range delBase {
		delBase[i] = newLeaf(1000+i, curSeed)
	}
	wantShift := delBase[20]
	del := slices.Delete(delBase, 10, 20)
	ck(len(del) == 90, "slices: Delete len")
	ck(del[10] == wantShift, "slices: Delete shift")
	for i := 90; i < 100; i++ {
		if delBase[i] != nil {
			ck(false, "slices: Delete did not zero tail")
			break
		}
	}
	ins0 := del[5]
	insNew := newLeaf(5000, curSeed)
	ins := slices.Insert(del, 5, insNew)
	ck(len(ins) == 91 && ins[5] == insNew && ins[6] == ins0, "slices: Insert result")
	rep1, rep2 := newLeaf(6001, curSeed), newLeaf(6002, curSeed)
	wantRep2 := ins[3]
	rep := slices.Replace(ins, 0, 3, rep1, rep2)
	ck(len(rep) == 90 && rep[0] == rep1 && rep[1] == rep2, "slices: Replace result")
	ck(rep[2] == wantRep2, "slices: Replace shift")

	gr := slices.Grow(rep, 64)
	ck(len(gr) == len(rep) && cap(gr) >= len(rep)+64, "slices: Grow len/cap")
	for i := range gr {
		if gr[i] != rep[i] {
			ck(false, "slices: Grow changed elements")
			break
		}
	}

	sorted := slices.Clone(sl)
	rnd.Shuffle(len(sorted), func(i, j int) { sorted[i], sorted[j] = sorted[j], sorted[i] })
	slices.SortFunc(sorted, func(a, b *leaf) int { return a.id - b.id })
	for i := 1; i < len(sorted); i++ {
		if sorted[i-1].id > sorted[i].id {
			ck(false, "slices: SortFunc order")
			break
		}
	}
	gcStress()
	for i := 0; i < len(sorted); i++ {
		if !validLeaf(sorted[i], i, curSeed) {
			ckSeed(false, fmt.Sprintf("slices: sorted payload corrupt at %d", i))
			break
		}
	}

	// 6) 跨 GC 全量校验。
	gcStress()
	for _, k := range keys {
		if !validLeaf(orig[k], orig[k].id, curSeed) {
			ckSeed(false, fmt.Sprintf("maps: original payload corrupt at %s", k))
			break
		}
	}
	runtime.KeepAlive(orig)
	runtime.KeepAlive(clone)
	runtime.KeepAlive(dst)
	runtime.KeepAlive(emptyDst)
	runtime.KeepAlive(filtered)
	runtime.KeepAlive(sorted)
	finish("l13_maps_clone_equal")
}
