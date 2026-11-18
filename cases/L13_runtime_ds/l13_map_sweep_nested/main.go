// L13: 嵌套 map 遍历中删除：内层逐键清扫、外层删当前组、别名快照与跨 GC 指针保活。
package main

import (
	"fmt"
	"os"
	"runtime"
	"strconv"
	"sync/atomic"
)

type sweepVal struct {
	id  string
	num int
	pad [8]byte
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

func gkey(g int) string { return fmt.Sprintf("g%02d", g) }
func ikey(k int) string { return fmt.Sprintf("k%02d", k) }

func newVal(g, k int) *sweepVal {
	v := &sweepVal{id: fmt.Sprintf("v-%02d-%02d", g, k), num: g*100 + k}
	for i := range v.pad {
		v.pad[i] = byte(g*13 + k*7 + i*3)
	}
	return v
}

func validVal(v *sweepVal, g, k int) bool {
	if v == nil || v.id != fmt.Sprintf("v-%02d-%02d", g, k) || v.num != g*100+k {
		return false
	}
	for i := range v.pad {
		if v.pad[i] != byte(g*13+k*7+i*3) {
			return false
		}
	}
	return true
}

func keepGroup(g, seed int) bool    { return (g*31+seed)%7 != 0 }
func keepInner(g, k, seed int) bool { return (g*131+k*17+seed)%5 != 0 }
func innerCount(g, seed int) int    { return (g*7 + seed) % 32 }
func normSeed(s int64) int          { return int((s%1000 + 1000) % 1000) }

func buildNested(nGroups, seed int) (map[string]map[string]*sweepVal, map[string]int, int) {
	m := make(map[string]map[string]*sweepVal, nGroups)
	idx := make(map[string]int, nGroups)
	total := 0
	for g := 0; g < nGroups; g++ {
		gk := gkey(g)
		cnt := innerCount(g, seed)
		im := make(map[string]*sweepVal, cnt)
		for k := 0; k < cnt; k++ {
			im[ikey(k)] = newVal(g, k)
		}
		m[gk] = im
		idx[gk] = g
		total += cnt
	}
	return m, idx, total
}

func main() {
	curSeed = envSeed()
	seed := normSeed(curSeed)

	// 1) 双层遍历删除：内层删谓词命中的当前键，外层删谓词命中的当前组。
	const nGroups = 64
	m, gIdx, initInner := buildNested(nGroups, seed)
	gcStress()

	visitedGroups := 0
	visitedInner := 0
	for gk, im := range m {
		g, ok := gIdx[gk]
		ckSeed(ok, "nested: unknown outer key")
		before := len(im)
		deleted := 0
		for ik, v := range im {
			visitedInner++
			if !validVal(v, g, atoi2(ik)) {
				ckSeed(false, fmt.Sprintf("nested: inner value corrupt g=%d k=%s", g, ik))
				break
			}
			if !keepInner(g, atoi2(ik), seed) {
				delete(im, ik)
				deleted++
			}
		}
		ckSeed(before-deleted == len(im), fmt.Sprintf("nested: inner length accounting g=%d", g))
		for k := 0; k < innerCount(g, seed); k++ {
			v, present := im[ikey(k)]
			if keepInner(g, k, seed) {
				ckSeed(present && validVal(v, g, k), fmt.Sprintf("nested: kept inner missing/corrupt g=%d k=%d", g, k))
			} else {
				ckSeed(!present, fmt.Sprintf("nested: deleted inner still present g=%d k=%d", g, k))
			}
		}
		visitedGroups++
		if !keepGroup(g, seed) {
			delete(m, gk)
		}
		if visitedGroups%9 == 0 {
			gcStress()
		}
	}
	ckSeed(visitedGroups == nGroups, "nested: outer iterations != original groups")
	ckSeed(visitedInner == initInner, "nested: inner iterations != original entries")

	wantGroups := 0
	for g := 0; g < nGroups; g++ {
		keep := keepGroup(g, seed)
		_, ok := m[gkey(g)]
		ckSeed(ok == keep, fmt.Sprintf("nested: group presence mismatch g=%d", g))
		if keep {
			wantGroups++
		}
	}
	ck(len(m) == wantGroups, "nested: group count after sweep")
	gcStress()
	totalRemaining := 0
	for g := 0; g < nGroups; g++ {
		if !keepGroup(g, seed) {
			continue
		}
		im := m[gkey(g)]
		want := 0
		for k := 0; k < innerCount(g, seed); k++ {
			if !keepInner(g, k, seed) {
				continue
			}
			want++
			v, ok := im[ikey(k)]
			ckSeed(ok && validVal(v, g, k), fmt.Sprintf("nested: post-GC entry bad g=%d k=%d", g, k))
		}
		ckSeed(len(im) == want, fmt.Sprintf("nested: post-GC inner count g=%d", g))
		totalRemaining += want
	}

	// 2) 全部删除：内层删每个当前键，外层再删每个当前组，最终 map 为空。
	allVisited := 0
	allInner := 0
	for gk, im := range m {
		for ik := range im {
			delete(im, ik)
			allInner++
		}
		ckSeed(len(im) == 0, "nested: inner not empty after delete-all")
		delete(m, gk)
		allVisited++
		if allVisited%11 == 0 {
			gcStress()
		}
	}
	ck(allVisited == wantGroups, "nested: delete-all outer visit count")
	ck(allInner == totalRemaining, "nested: delete-all inner visit count")
	gcStress()
	ck(len(m) == 0, "nested: map not empty after delete-all")

	// 3) 别名快照：删除外层键后内层 map 仍由快照保活。
	small, _, _ := buildNested(16, seed)
	gcStress()
	snap := small["g05"]
	ck(snap != nil, "nested alias: g05 missing")
	small["g05"] = nil
	delete(small, "g05")
	ck(len(small) == 15, "nested alias: outer size after delete")
	gcStress()
	ckSeed(snap != nil && len(snap) == innerCount(5, seed), "nested alias: snapshot length after GC")
	for k, v := range snap {
		ckSeed(validVal(v, 5, atoi2(k)), fmt.Sprintf("nested alias: snapshot value corrupt k=%s", k))
	}
	// 通过快照删除一半，快照自身一致。
	delSnap := 0
	for k := range snap {
		if atoi2(k)%2 == 0 {
			delete(snap, k)
			delSnap++
		}
	}
	ck(len(snap) == innerCount(5, seed)-delSnap, "nested alias: snapshot delete count")
	gcStress()
	for k, v := range snap {
		ckSeed(atoi2(k)%2 == 1 && validVal(v, 5, atoi2(k)), fmt.Sprintf("nested alias: retained snapshot bad k=%s", k))
	}

	// 4) nil 内层 map：len/读取/delete 安全，GC 后仍为 nil。
	small["nilgroup"] = nil
	gcStress()
	ck(len(small["nilgroup"]) == 0, "nested nil: len(nil inner) != 0")
	_, ok := small["nilgroup"]["k00"]
	ck(!ok, "nested nil: lookup on nil inner succeeded")
	delete(small["nilgroup"], "k00")
	ck(small["nilgroup"] == nil, "nested nil: delete changed nil map")

	runtime.KeepAlive(m)
	runtime.KeepAlive(small)
	runtime.KeepAlive(snap)
	finish("l13_map_sweep_nested")
}

func atoi2(s string) int {
	if len(s) < 2 {
		return 0
	}
	return int(s[len(s)-2]-'0')*10 + int(s[len(s)-1]-'0')
}
