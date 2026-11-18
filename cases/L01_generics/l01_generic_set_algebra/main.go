// L01: 泛型集合代数：Union/Intersect/Difference/SymDiff/Subset 与有序集合区间查询。
// 对应 go-redis SINTER/SDIFF/SCARD 语义与 golangci-lint 的检查器集合运算。
package main

import (
	"cmp"
	"fmt"
	"os"
	"runtime"
	"sort"
	"strconv"
	"sync/atomic"
)

type setPayload struct {
	id   int64
	name string
	blob []byte
}

type setKey struct {
	a int64
	b string
}

type genSet[T comparable] map[T]struct{}

func newGenSet[T comparable](vals ...T) genSet[T] {
	s := make(genSet[T], len(vals))
	for _, v := range vals {
		s[v] = struct{}{}
	}
	return s
}

func (s genSet[T]) add(v T) bool {
	if _, ok := s[v]; ok {
		return false
	}
	s[v] = struct{}{}
	return true
}

func (s genSet[T]) remove(v T) bool {
	if _, ok := s[v]; !ok {
		return false
	}
	delete(s, v)
	return true
}

func (s genSet[T]) has(v T) bool {
	_, ok := s[v]
	return ok
}

func (s genSet[T]) len() int { return len(s) }

func (s genSet[T]) clone() genSet[T] {
	out := make(genSet[T], len(s))
	for v := range s {
		out[v] = struct{}{}
	}
	return out
}

func unionSets[T comparable](a, b genSet[T]) genSet[T] {
	out := make(genSet[T], len(a)+len(b))
	for v := range a {
		out[v] = struct{}{}
	}
	for v := range b {
		out[v] = struct{}{}
	}
	return out
}

func intersectSets[T comparable](a, b genSet[T]) genSet[T] {
	out := make(genSet[T])
	if len(a) > len(b) {
		a, b = b, a
	}
	for v := range a {
		if b.has(v) {
			out[v] = struct{}{}
		}
	}
	return out
}

func differenceSets[T comparable](a, b genSet[T]) genSet[T] {
	out := make(genSet[T], len(a))
	for v := range a {
		if !b.has(v) {
			out[v] = struct{}{}
		}
	}
	return out
}

func symDifferenceSets[T comparable](a, b genSet[T]) genSet[T] {
	out := differenceSets(a, b)
	for v := range differenceSets(b, a) {
		out[v] = struct{}{}
	}
	return out
}

func subsetOf[T comparable](a, b genSet[T]) bool {
	for v := range a {
		if !b.has(v) {
			return false
		}
	}
	return true
}

func equalSets[T comparable](a, b genSet[T]) bool {
	return len(a) == len(b) && subsetOf(a, b)
}

type orderedSet[T cmp.Ordered] struct {
	vals []T
}

func (o *orderedSet[T]) add(v T) bool {
	i := sort.Search(len(o.vals), func(i int) bool { return cmp.Compare(o.vals[i], v) >= 0 })
	if i < len(o.vals) && cmp.Compare(o.vals[i], v) == 0 {
		return false
	}
	o.vals = append(o.vals, v)
	copy(o.vals[i+1:], o.vals[i:])
	o.vals[i] = v
	return true
}

func (o *orderedSet[T]) has(v T) bool {
	i := sort.Search(len(o.vals), func(i int) bool { return cmp.Compare(o.vals[i], v) >= 0 })
	return i < len(o.vals) && cmp.Compare(o.vals[i], v) == 0
}

func (o *orderedSet[T]) len() int { return len(o.vals) }

func (o *orderedSet[T]) slice() []T { return append([]T(nil), o.vals...) }

// rangeQuery 返回 [lo,hi] 闭区间内的元素，天然升序。
func (o *orderedSet[T]) rangeQuery(lo, hi T) []T {
	i := sort.Search(len(o.vals), func(i int) bool { return cmp.Compare(o.vals[i], lo) >= 0 })
	var out []T
	for ; i < len(o.vals) && cmp.Compare(o.vals[i], hi) <= 0; i++ {
		out = append(out, o.vals[i])
	}
	return out
}

func orderedUnion[T cmp.Ordered](a, b *orderedSet[T]) *orderedSet[T] {
	out := &orderedSet[T]{}
	i, j := 0, 0
	for i < len(a.vals) || j < len(b.vals) {
		switch {
		case j >= len(b.vals) || (i < len(a.vals) && cmp.Compare(a.vals[i], b.vals[j]) < 0):
			out.vals = append(out.vals, a.vals[i])
			i++
		case i >= len(a.vals) || cmp.Compare(a.vals[i], b.vals[j]) > 0:
			out.vals = append(out.vals, b.vals[j])
			j++
		default:
			out.vals = append(out.vals, a.vals[i])
			i++
			j++
		}
	}
	return out
}

func orderedIntersect[T cmp.Ordered](a, b *orderedSet[T]) *orderedSet[T] {
	out := &orderedSet[T]{}
	i, j := 0, 0
	for i < len(a.vals) && j < len(b.vals) {
		c := cmp.Compare(a.vals[i], b.vals[j])
		if c < 0 {
			i++
		} else if c > 0 {
			j++
		} else {
			out.vals = append(out.vals, a.vals[i])
			i++
			j++
		}
	}
	return out
}

var (
	failed  int32
	gcSink  [][]byte
	uafSeed int64 = 1
)

func ck(ok bool, msg string) {
	if !ok {
		atomic.AddInt32(&failed, 1)
		fmt.Println("FAIL:", msg)
	}
}

func gcStress() {
	for i := 0; i < 16; i++ {
		b := make([]byte, 2048)
		b[0] = byte(i)
		gcSink = append(gcSink[:0], b)
	}
	gcSink = gcSink[:0]
	runtime.GC()
	runtime.GC()
}

func finish(name string) {
	if n := atomic.LoadInt32(&failed); n != 0 {
		fmt.Printf("FAILED %s seed=%d (%d failed checks)\n", name, uafSeed, n)
		os.Exit(1)
	}
	fmt.Println("ALL PASS " + name)
}

func envSeed() int64 {
	n, err := strconv.ParseInt(os.Getenv("UAF_SEED"), 10, 64)
	if err != nil || n <= 0 {
		return 1
	}
	return n
}

func countTrue(ref []bool) int {
	n := 0
	for _, v := range ref {
		if v {
			n++
		}
	}
	return n
}

func checkRef(label string, s genSet[int64], ref []bool) {
	ck(s.len() == countTrue(ref), fmt.Sprintf("%s cardinality got=%d want=%d", label, s.len(), countTrue(ref)))
	for i := range ref {
		ck(s.has(int64(i)) == ref[i], fmt.Sprintf("%s membership mismatch at %d", label, i))
	}
}

func makeRefSet(seed uint64, universe int) (genSet[int64], []bool) {
	s := newGenSet[int64]()
	ref := make([]bool, universe)
	for i := 0; i < universe; i++ {
		h := uint64(i)*0x9e3779b97f4a7c15 + seed*0x100000001b3
		h ^= h >> 33
		if h%11 < 4 {
			s.add(int64(i))
			ref[i] = true
		}
	}
	return s, ref
}

func runFixed() {
	// go-redis 文档示例：racing:france = {1,2,3}, racing:usa = {1,4}。
	france := newGenSet[int64](1, 2, 3)
	usa := newGenSet[int64](1, 4)
	inter := intersectSets(france, usa)
	diff := differenceSets(france, usa)
	uni := unionSets(france, usa)
	sym := symDifferenceSets(france, usa)
	ck(inter.len() == 1 && inter.has(1), "SInter result wrong")
	ck(diff.len() == 2 && diff.has(2) && diff.has(3), "SDiff result wrong")
	ck(uni.len() == 4 && uni.has(1) && uni.has(4), "union result wrong")
	ck(sym.len() == 3 && sym.has(2) && sym.has(3) && sym.has(4), "symdiff result wrong")
	ck(subsetOf(inter, france) && subsetOf(inter, usa), "intersection not subset")
	ck(equalSets(unionSets(france, usa), unionSets(usa, france)), "union not commutative")

	str := newGenSet[string]("bike:1", "bike:2", "bike:3")
	str2 := newGenSet[string]("bike:1", "bike:4")
	ck(intersectSets(str, str2).len() == 1 && intersectSets(str, str2).has("bike:1"), "string SInter wrong")
	ck(differenceSets(str, str2).len() == 2, "string SDiff wrong")

	// 边界：空集/重复 add/删除不存在元素。
	empty := newGenSet[int64]()
	ck(unionSets(empty, france).len() == 3, "union with empty wrong")
	ck(intersectSets(empty, france).len() == 0, "intersect with empty wrong")
	ck(subsetOf(empty, france), "empty set not subset")
	ck(empty.add(9), "first add should report inserted")
	ck(!empty.add(9), "duplicate add should report existing")
	ck(empty.remove(9), "first remove should report deleted")
	ck(!empty.remove(9), "second remove should report absent")
}

func runRandom(seed int64) {
	const universe = 96
	a, ra := makeRefSet(uint64(seed), universe)
	b, rb := makeRefSet(uint64(seed)+17, universe)
	c, rc := makeRefSet(uint64(seed)+31, universe)

	unionAB := unionSets(a, b)
	interAB := intersectSets(a, b)
	diffAB := differenceSets(a, b)
	symAB := symDifferenceSets(a, b)
	unionBC := unionSets(b, c)
	interBC := intersectSets(b, c)
	diffAC := differenceSets(a, c)
	diffAB2 := differenceSets(a, b)
	interDiff := intersectSets(diffAB, diffAC)
	unionAC := unionSets(a, c)

	for i := 0; i < universe; i++ {
		ck(unionAB.has(int64(i)) == (ra[i] || rb[i]), fmt.Sprintf("unionAB membership at %d", i))
		ck(interAB.has(int64(i)) == (ra[i] && rb[i]), fmt.Sprintf("interAB membership at %d", i))
		ck(diffAB.has(int64(i)) == (ra[i] && !rb[i]), fmt.Sprintf("diffAB membership at %d", i))
		ck(symAB.has(int64(i)) == (ra[i] != rb[i]), fmt.Sprintf("symAB membership at %d", i))
	}

	// 代数律。
	ck(subsetOf(diffAB, a), "A-B not subset of A")
	ck(equalSets(diffAB, diffAB2), "difference not deterministic")
	ck(equalSets(symAB, differenceSets(unionAB, interAB)), "symdiff identity broken")
	ck(equalSets(differenceSets(a, unionBC), intersectSets(diffAB, diffAC)), "De Morgan broken")
	ck(equalSets(intersectSets(a, unionBC), unionSets(intersectSets(a, b), intersectSets(a, c))), "distributive law broken")
	ck(equalSets(unionSets(a, unionSets(b, c)), unionSets(unionSets(a, b), c)), "union not associative")
	ck(equalSets(intersectSets(a, unionAC), a), "intersect/union absorption broken")

	checkRef("A", a, ra)
	checkRef("B", b, rb)
	checkRef("C", c, rc)
	checkRef("unionAB", unionAB, func() []bool {
		out := make([]bool, universe)
		for i := range out {
			out[i] = ra[i] || rb[i]
		}
		return out
	}())
	checkRef("interDiff", interDiff, func() []bool {
		out := make([]bool, universe)
		for i := range out {
			out[i] = ra[i] && !rb[i] && !rc[i]
		}
		return out
	}())
	_ = interBC

	// 显式 GC 后集合内容与成员判断必须保持。
	gcStress()
	checkRef("A-after-GC", a, ra)
	ck(equalSets(unionSets(a, b), unionAB), "union changed after GC")
	ck(equalSets(intersectSets(b, c), interBC), "intersection changed after GC")
}

func runOrdered(seed int64) {
	o := &orderedSet[int64]{}
	ref := make(map[int64]bool)
	for i := 0; i < 180; i++ {
		v := int64((i*37 + int(seed)*13) % 200)
		added := o.add(v)
		if ref[v] {
			ck(!added, fmt.Sprintf("ordered add duplicate %d returned true", v))
		} else {
			ck(added, fmt.Sprintf("ordered add %d returned false", v))
		}
		ref[v] = true
	}
	gcStress()
	vals := o.slice()
	ck(len(vals) == len(ref), fmt.Sprintf("ordered len got=%d want=%d", len(vals), len(ref)))
	for i := 1; i < len(vals); i++ {
		ck(vals[i-1] < vals[i], "ordered set not strictly ascending")
	}
	for v := int64(0); v < 210; v++ {
		ck(o.has(v) == ref[v], fmt.Sprintf("ordered membership mismatch %d", v))
	}

	// 区间查询与暴力过滤一致。
	for _, q := range [][2]int64{{0, 0}, {1, 50}, {40, 120}, {180, 220}, {199, 199}} {
		got := o.rangeQuery(q[0], q[1])
		var want []int64
		for v := range ref {
			if v >= q[0] && v <= q[1] {
				want = append(want, v)
			}
		}
		sort.Slice(want, func(i, j int) bool { return want[i] < want[j] })
		ck(len(got) == len(want), fmt.Sprintf("range query [%d,%d] len got=%d want=%d", q[0], q[1], len(got), len(want)))
		for i := 0; i < len(got) && i < len(want); i++ {
			ck(got[i] == want[i], fmt.Sprintf("range query [%d,%d] value mismatch at %d", q[0], q[1], i))
		}
	}

	// 有序并/交：合并结果仍升序且成员的集合语义正确。
	o2 := &orderedSet[int64]{}
	for i := 0; i < 90; i++ {
		o2.add(int64((i*53 + 7) % 160))
	}
	u := orderedUnion(o, o2)
	in := orderedIntersect(o, o2)
	for i := 1; i < len(u.vals); i++ {
		ck(u.vals[i-1] < u.vals[i], "ordered union not sorted")
	}
	for _, v := range u.vals {
		ck(o.has(v) || o2.has(v), "ordered union contains phantom value")
	}
	for _, v := range o.vals {
		if o2.has(v) {
			ck(in.has(v), "ordered intersection missing value")
		}
	}
	for _, v := range in.vals {
		ck(o.has(v) && o2.has(v), "ordered intersection contains phantom value")
	}
}

func runPointers(seed int64) {
	ps := newGenSet[*setPayload]()
	all := make([]*setPayload, 0, 120)
	for i := 0; i < 120; i++ {
		p := &setPayload{
			id:   int64(i),
			name: fmt.Sprintf("p-%03d", i),
			blob: []byte{byte(i), byte(seed)},
		}
		all = append(all, p)
		ps.add(p)
	}
	for i := 0; i < len(all); i += 5 {
		ck(ps.remove(all[i]), fmt.Sprintf("pointer remove %d failed", i))
	}
	gcStress()
	ck(ps.len() == 96, fmt.Sprintf("pointer set len got=%d want=96", ps.len()))
	for i, p := range all {
		ck(ps.has(p) == (i%5 != 0), fmt.Sprintf("pointer membership mismatch %d", i))
		if i%5 != 0 {
			ck(p.name == fmt.Sprintf("p-%03d", i) && p.blob[0] == byte(i), "pointer payload corrupted after GC")
		}
	}
	other := newGenSet[*setPayload](all[1], all[2], all[100])
	inter := intersectSets(ps, other)
	ck(inter.len() == 2 && inter.has(all[1]) && inter.has(all[2]), "pointer set intersection wrong")
	_ = other
}

func runStructKey() {
	s := newGenSet[setKey]()
	total := 0
	for i := 0; i < 60; i++ {
		k := setKey{a: int64(i), b: fmt.Sprintf("v%d", i%7)}
		if s.add(k) {
			total++
		}
	}
	ck(s.len() == total && s.has(setKey{a: 5, b: "v5"}), "struct key set wrong")
	ck(!s.has(setKey{a: 5, b: "v6"}), "struct key false positive")
	gcStress()
	ck(s.has(setKey{a: 59, b: "v3"}), "struct key set corrupted after GC")
}

func main() {
	uafSeed = envSeed()
	runFixed()
	runRandom(uafSeed)
	runOrdered(uafSeed)
	runPointers(uafSeed)
	runStructKey()
	finish("l01_generic_set_algebra")
}
