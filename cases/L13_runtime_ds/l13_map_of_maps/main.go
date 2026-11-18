// L13: map of maps 嵌套映射：内层增删改、外层删除/重插、别名共享与三级嵌套的 GC 保活。
package main

import (
	"fmt"
	"os"
	"runtime"
	"sort"
	"strconv"
	"sync/atomic"
)

type leaf struct {
	v    int
	s    string
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

func newLeaf(group, key string, v int, seed int64) *leaf {
	l := &leaf{v: v, s: fmt.Sprintf("%s/%s:%d", group, key, v), salt: seed, pad: make([]byte, 12)}
	for i := range l.pad {
		l.pad[i] = byte(v*7 + i*3 + int(seed))
	}
	return l
}

func validLeaf(l *leaf, group, key string, v int) bool {
	if l == nil || l.v != v || l.s != fmt.Sprintf("%s/%s:%d", group, key, v) || len(l.pad) != 12 {
		return false
	}
	for i := range l.pad {
		if l.pad[i] != byte(l.v*7+i*3+int(l.salt)) {
			return false
		}
	}
	return true
}

func sortedKeys(outer map[string]map[string]*leaf) []string {
	keys := make([]string, 0, len(outer))
	for k := range outer {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func sortedInner(m map[string]*leaf) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func checkMaps(outer map[string]map[string]*leaf, model map[string]map[string]int, tag string) {
	if len(outer) != len(model) {
		ckSeed(false, tag+": outer size differs from model")
		return
	}
	ok := sortedKeys(outer)
	zk := sortedKeys2(model)
	if len(ok) != len(zk) {
		ckSeed(false, tag+": outer key count differs from model")
		return
	}
	for i := range ok {
		if ok[i] != zk[i] {
			ckSeed(false, tag+": outer keys differ from model")
			return
		}
	}
	for _, g := range ok {
		got := outer[g]
		want := model[g]
		leafGroup := g
		if g == "alias" {
			// alias 与 g01 共享同一个内层 map，叶子的命名沿用 g01。
			leafGroup = "g01"
		}
		if (got == nil) != (want == nil) {
			ckSeed(false, tag+": inner map nil-ness differs for group "+g)
			continue
		}
		if got == nil {
			continue
		}
		if len(got) != len(want) {
			ckSeed(false, tag+": inner size differs for group "+g)
			continue
		}
		ik := sortedInner(got)
		wk := sortedKeysInt(want)
		if len(ik) != len(wk) {
			ckSeed(false, tag+": inner key count differs for group "+g)
			continue
		}
		for i := range ik {
			if ik[i] != wk[i] {
				ckSeed(false, tag+": inner keys differ for group "+g)
				break
			}
		}
		for _, k := range ik {
			if !validLeaf(got[k], leafGroup, k, want[k]) {
				ckSeed(false, tag+": leaf mismatch for "+g+"/"+k)
				break
			}
		}
	}
}

func sortedKeys2(m map[string]map[string]int) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func sortedKeysInt(m map[string]int) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func main() {
	curSeed = envSeed()
	seed := curSeed

	var outerKeys, innerKeys []string
	for i := 0; i < 20; i++ {
		outerKeys = append(outerKeys, fmt.Sprintf("g%02d", i))
	}
	for i := 0; i < 12; i++ {
		innerKeys = append(innerKeys, fmt.Sprintf("k%02d", i))
	}

	outer := make(map[string]map[string]*leaf)
	model := make(map[string]map[string]int)
	for oi, g := range outerKeys {
		inner := make(map[string]*leaf)
		mi := make(map[string]int)
		for ki, k := range innerKeys {
			if (oi+ki)%4 == 0 {
				continue
			}
			v := oi*1000 + ki
			inner[k] = newLeaf(g, k, v, seed)
			mi[k] = v
		}
		outer[g] = inner
		model[g] = mi
	}
	gcStress()
	checkMaps(outer, model, "build")

	// 内层更新：现有条目替换为新指针。
	for oi, g := range outerKeys {
		for ki, k := range innerKeys {
			if (oi+ki)%3 != 0 {
				continue
			}
			if inner, ok := outer[g]; ok {
				if _, exists := inner[k]; exists {
					v := 10000 + oi*100 + ki
					inner[k] = newLeaf(g, k, v, seed+1)
					model[g][k] = v
				}
			}
		}
	}
	gcStress()
	checkMaps(outer, model, "inner-update")

	// 内层删除。
	for _, g := range outerKeys {
		if inner, ok := outer[g]; ok {
			for ki, k := range innerKeys {
				if ki%4 == 1 {
					delete(inner, k)
					delete(model[g], k)
				}
			}
		}
	}
	gcStress()
	checkMaps(outer, model, "inner-delete")

	// 外层删除（每 5 组删一组）。
	for oi, g := range outerKeys {
		if oi%5 == 0 {
			delete(outer, g)
			delete(model, g)
		}
	}
	gcStress()
	checkMaps(outer, model, "outer-delete")

	// 外层重插两个空内层 map，再填充条目。
	for _, g := range []string{"g05", "g10"} {
		inner := make(map[string]*leaf)
		mi := make(map[string]int)
		for j := 0; j < 3; j++ {
			k := innerKeys[j]
			v := 50000 + j
			inner[k] = newLeaf(g, k, v, seed+2)
			mi[k] = v
		}
		outer[g] = inner
		model[g] = mi
	}
	gcStress()
	checkMaps(outer, model, "outer-reinsert")

	// 别名：两个外层键共享同一个内层 map，经别名写入对另一键可见。
	outer["alias"] = outer["g01"]
	model["alias"] = model["g01"]
	outer["alias"]["kA"] = newLeaf("g01", "kA", 70000, seed+3)
	model["alias"]["kA"] = 70000
	ckSeed(outer["g01"]["kA"] != nil, "alias write not visible through original key")
	checkMaps(outer, model, "alias-add")
	delete(outer, "g01")
	delete(model, "g01")
	gcStress()
	checkMaps(outer, model, "alias-after-outer-delete")
	ckSeed(outer["alias"]["kA"] != nil && outer["alias"]["kA"].v == 70000, "alias value lost after original deleted")

	// nil 内层：读取/len/delete 均安全，GC 后仍为 nil。
	outer["gNil"] = nil
	model["gNil"] = nil
	ckSeed(len(outer["gNil"]) == 0, "nil inner map length")
	ckSeed(outer["gNil"]["absent"] == nil, "nil inner map read")
	delete(outer["gNil"], "absent")
	gcStress()
	checkMaps(outer, model, "nil-inner")

	// 三级嵌套 map：内层值同时与模型逐项对照。
	deep := make(map[string]map[string]map[string]*leaf)
	deepModel := make(map[string]map[string]map[string]int)
	gA := make(map[string]map[string]*leaf)
	gB := make(map[string]map[string]*leaf)
	for gi, g := range []string{"ga", "gb"} {
		mids := make(map[string]map[string]*leaf)
		mmids := make(map[string]map[string]int)
		for mi, m := range []string{"mx", "my", "mz"} {
			bottoms := make(map[string]*leaf)
			mb := make(map[string]int)
			for bi := 0; bi < 4; bi++ {
				if (gi+mi+bi)%3 == 0 {
					continue
				}
				k := fmt.Sprintf("b%d", bi)
				v := 80000 + gi*100 + mi*10 + bi
				bottoms[k] = newLeaf(g+"/"+m, k, v, seed+4)
				mb[k] = v
			}
			mids[m] = bottoms
			mmids[m] = mb
		}
		if g == "ga" {
			gA = mids
		} else {
			gB = mids
		}
		deep[g] = mids
		deepModel[g] = mmids
	}
	gcStress()
	checkDeep(deep, deepModel)
	delete(gA, "mx")
	delete(deepModel["ga"], "mx")
	gcStress()
	checkDeep(deep, deepModel)
	ckSeed(len(deep["ga"]) == 2, "deep delete mid level")
	delete(deep, "gb")
	delete(deepModel, "gb")
	gcStress()
	ckSeed(len(deep) == 1 && len(deep["ga"]) == 2, "deep outer delete")
	checkDeep(deep, deepModel)

	// 最终 GC 后再跑一次完整外层校验。
	gcStress()
	checkMaps(outer, model, "final")
	checkDeep(deep, deepModel)

	runtime.KeepAlive(outer)
	runtime.KeepAlive(model)
	runtime.KeepAlive(deep)
	runtime.KeepAlive(deepModel)
	runtime.KeepAlive(gA)
	runtime.KeepAlive(gB)
	finish("l13_map_of_maps")
}

func checkDeep(deep map[string]map[string]map[string]*leaf, model map[string]map[string]map[string]int) {
	if len(deep) != len(model) {
		ckSeed(false, "deep outer size differs from model")
		return
	}
	gk := make([]string, 0, len(deep))
	for g := range deep {
		gk = append(gk, g)
	}
	sort.Strings(gk)
	for _, g := range gk {
		mids := deep[g]
		wmids := model[g]
		if len(mids) != len(wmids) {
			ckSeed(false, "deep mid size differs from model")
			continue
		}
		mk := make([]string, 0, len(mids))
		for m := range mids {
			mk = append(mk, m)
		}
		sort.Strings(mk)
		for _, m := range mk {
			bottoms := mids[m]
			wb := wmids[m]
			if len(bottoms) != len(wb) {
				ckSeed(false, "deep bottom size differs from model")
				continue
			}
			bk := sortedInner(bottoms)
			wk := sortedKeysInt(wb)
			for i := range bk {
				if bk[i] != wk[i] {
					ckSeed(false, "deep bottom keys differ from model")
					break
				}
			}
			for _, k := range bk {
				if !validLeaf(bottoms[k], g+"/"+m, k, wb[k]) {
					ckSeed(false, "deep leaf mismatch")
					return
				}
			}
		}
	}
}
