// L01: 泛型区间树（treap + maxHi 增强），随机插入/Stab/Intersects/删除。
// 对应 etcd pkg/adt.IntervalTree 的随机区间测试与中序 Visit。
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

type ivPayload struct {
	id   int64
	name string
	blob []byte
}

type ivNode[T cmp.Ordered, V any] struct {
	lo, hi, maxHi T
	val           *V
	prio          uint64
	left, right   *ivNode[T, V]
	size          int
}

type intervalTree[T cmp.Ordered, V any] struct {
	root *ivNode[T, V]
	seq  uint64
}

func splitmix64(x uint64) uint64 {
	x += 0x9e3779b97f4a7c15
	x = (x ^ (x >> 30)) * 0xbf58476d1ce4e5b9
	x = (x ^ (x >> 27)) * 0x94d049bb133111eb
	return x ^ (x >> 31)
}

func (t *intervalTree[T, V]) nextPrio() uint64 {
	t.seq++
	return splitmix64(t.seq * 0x9e3779b97f4a7c15)
}

func ivLess[T cmp.Ordered](lo1, hi1, lo2, hi2 T) bool {
	if c := cmp.Compare(lo1, lo2); c != 0 {
		return c < 0
	}
	return cmp.Compare(hi1, hi2) < 0
}

func ivSize[T cmp.Ordered, V any](n *ivNode[T, V]) int {
	if n == nil {
		return 0
	}
	return n.size
}

func ivUpdate[T cmp.Ordered, V any](n *ivNode[T, V]) {
	n.size = 1 + ivSize(n.left) + ivSize(n.right)
	n.maxHi = n.hi
	if n.left != nil && cmp.Compare(n.left.maxHi, n.maxHi) > 0 {
		n.maxHi = n.left.maxHi
	}
	if n.right != nil && cmp.Compare(n.right.maxHi, n.maxHi) > 0 {
		n.maxHi = n.right.maxHi
	}
}

func ivRotateRight[T cmp.Ordered, V any](n *ivNode[T, V]) *ivNode[T, V] {
	l := n.left
	n.left = l.right
	l.right = n
	ivUpdate(n)
	ivUpdate(l)
	return l
}

func ivRotateLeft[T cmp.Ordered, V any](n *ivNode[T, V]) *ivNode[T, V] {
	r := n.right
	n.right = r.left
	r.left = n
	ivUpdate(n)
	ivUpdate(r)
	return r
}

func ivInsert[T cmp.Ordered, V any](n *ivNode[T, V], lo, hi T, val *V, prio uint64) *ivNode[T, V] {
	if n == nil {
		return &ivNode[T, V]{lo: lo, hi: hi, maxHi: hi, val: val, prio: prio, size: 1}
	}
	if ivLess(lo, hi, n.lo, n.hi) {
		n.left = ivInsert(n.left, lo, hi, val, prio)
		if n.left.prio < n.prio {
			n = ivRotateRight(n)
		}
	} else if ivLess(n.lo, n.hi, lo, hi) {
		n.right = ivInsert(n.right, lo, hi, val, prio)
		if n.right.prio < n.prio {
			n = ivRotateLeft(n)
		}
	} else {
		n.val = val
		return n
	}
	ivUpdate(n)
	return n
}

func ivMerge[T cmp.Ordered, V any](a, b *ivNode[T, V]) *ivNode[T, V] {
	if a == nil {
		return b
	}
	if b == nil {
		return a
	}
	if a.prio < b.prio {
		a.right = ivMerge(a.right, b)
		ivUpdate(a)
		return a
	}
	b.left = ivMerge(a, b.left)
	ivUpdate(b)
	return b
}

func ivDelete[T cmp.Ordered, V any](n *ivNode[T, V], lo, hi T) (*ivNode[T, V], bool) {
	if n == nil {
		return nil, false
	}
	if ivLess(lo, hi, n.lo, n.hi) {
		var ok bool
		n.left, ok = ivDelete(n.left, lo, hi)
		ivUpdate(n)
		return n, ok
	}
	if ivLess(n.lo, n.hi, lo, hi) {
		var ok bool
		n.right, ok = ivDelete(n.right, lo, hi)
		ivUpdate(n)
		return n, ok
	}
	return ivMerge(n.left, n.right), true
}

func (t *intervalTree[T, V]) find(lo, hi T) *ivNode[T, V] {
	n := t.root
	for n != nil {
		if ivLess(lo, hi, n.lo, n.hi) {
			n = n.left
		} else if ivLess(n.lo, n.hi, lo, hi) {
			n = n.right
		} else {
			return n
		}
	}
	return nil
}

// Insert 返回是否新建；重复区间只更新值（模拟 lease 堆的 RegisterOrUpdate）。
func (t *intervalTree[T, V]) Insert(lo, hi T, val *V) bool {
	if n := t.find(lo, hi); n != nil {
		n.val = val
		return false
	}
	t.root = ivInsert(t.root, lo, hi, val, t.nextPrio())
	return true
}

func (t *intervalTree[T, V]) Delete(lo, hi T) bool {
	var ok bool
	t.root, ok = ivDelete(t.root, lo, hi)
	return ok
}

func (t *intervalTree[T, V]) Size() int { return ivSize(t.root) }

// Stab 返回所有覆盖点 p 的区间值。
func (t *intervalTree[T, V]) Stab(p T) []*V {
	var out []*V
	var walk func(n *ivNode[T, V])
	walk = func(n *ivNode[T, V]) {
		if n == nil {
			return
		}
		if n.left != nil && cmp.Compare(n.left.maxHi, p) >= 0 {
			walk(n.left)
		}
		if cmp.Compare(n.lo, p) <= 0 && cmp.Compare(p, n.hi) <= 0 {
			out = append(out, n.val)
		}
		if cmp.Compare(n.lo, p) <= 0 {
			walk(n.right)
		}
	}
	walk(t.root)
	return out
}

// Intersects 返回与 [lo,hi] 相交的区间值。
func (t *intervalTree[T, V]) Intersects(lo, hi T) []*V {
	var out []*V
	var walk func(n *ivNode[T, V])
	walk = func(n *ivNode[T, V]) {
		if n == nil {
			return
		}
		if n.left != nil && cmp.Compare(n.left.maxHi, lo) >= 0 {
			walk(n.left)
		}
		if cmp.Compare(n.lo, hi) <= 0 && cmp.Compare(n.hi, lo) >= 0 {
			out = append(out, n.val)
		}
		if cmp.Compare(n.lo, hi) <= 0 {
			walk(n.right)
		}
	}
	walk(t.root)
	return out
}

func (t *intervalTree[T, V]) inOrder() []*V {
	var out []*V
	var walk func(n *ivNode[T, V])
	walk = func(n *ivNode[T, V]) {
		if n == nil {
			return
		}
		walk(n.left)
		out = append(out, n.val)
		walk(n.right)
	}
	walk(t.root)
	return out
}

func (t *intervalTree[T, V]) check() error {
	var walk func(n *ivNode[T, V]) error
	walk = func(n *ivNode[T, V]) (err error) {
		if n == nil {
			return nil
		}
		wantMax := n.hi
		if n.left != nil {
			if !ivLess(n.left.lo, n.left.hi, n.lo, n.hi) {
				return fmt.Errorf("left key not less: [%v,%v] vs [%v,%v]", n.left.lo, n.left.hi, n.lo, n.hi)
			}
			if n.left.prio < n.prio {
				return fmt.Errorf("treap priority order broken in left child")
			}
			if cmp.Compare(n.left.maxHi, wantMax) > 0 {
				wantMax = n.left.maxHi
			}
			if err := walk(n.left); err != nil {
				return err
			}
		}
		if n.right != nil {
			if !ivLess(n.lo, n.hi, n.right.lo, n.right.hi) {
				return fmt.Errorf("right key not greater: [%v,%v] vs [%v,%v]", n.right.lo, n.right.hi, n.lo, n.hi)
			}
			if n.right.prio < n.prio {
				return fmt.Errorf("treap priority order broken in right child")
			}
			if cmp.Compare(n.right.maxHi, wantMax) > 0 {
				wantMax = n.right.maxHi
			}
			if err := walk(n.right); err != nil {
				return err
			}
		}
		if cmp.Compare(n.maxHi, wantMax) != 0 {
			return fmt.Errorf("maxHi augmented field wrong: got=%v want=%v", n.maxHi, wantMax)
		}
		if n.size != 1+ivSize(n.left)+ivSize(n.right) {
			return fmt.Errorf("size field wrong at [%v,%v]", n.lo, n.hi)
		}
		return nil
	}
	return walk(t.root)
}

var (
	failed      int32
	gcSink      [][]byte
	payloadSink []*ivPayload
	uafSeed     int64 = 1
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

func envInt(key string, def int) int {
	s := os.Getenv(key)
	if s == "" {
		return def
	}
	n, err := strconv.Atoi(s)
	if err != nil || n <= 0 {
		return def
	}
	return n
}

func envSeed() int64 {
	n, err := strconv.ParseInt(os.Getenv("UAF_SEED"), 10, 64)
	if err != nil || n <= 0 {
		return 1
	}
	return n
}

type rng64 struct{ s uint64 }

func newRNG(seed int64, round int) *rng64 {
	s := uint64(seed+int64(round)*104729)*0x9e3779b97f4a7c15 + 7
	if s == 0 {
		s = 1
	}
	return &rng64{s: s}
}

func (r *rng64) next() uint64 {
	r.s ^= r.s << 13
	r.s ^= r.s >> 7
	r.s ^= r.s << 17
	return r.s
}

func (r *rng64) n(n int) int { return int(r.next() % uint64(n)) }

type modelIv struct {
	lo, hi int64
	val    *ivPayload
}

func sortVals(vs []*ivPayload) []*ivPayload {
	out := append([]*ivPayload(nil), vs...)
	sort.Slice(out, func(i, j int) bool {
		if out[i].id != out[j].id {
			return out[i].id < out[j].id
		}
		return out[i].name < out[j].name
	})
	return out
}

func sameVals(a, b []*ivPayload) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func runRound(seed int64, round, count, queries int) {
	r := newRNG(seed, round)
	tree := &intervalTree[int64, ivPayload]{}
	model := make(map[[2]int64]*ivPayload)
	unique := make(map[[2]int64]bool)
	maxv := 640
	seq := int64(round*100000 + 1)

	for len(unique) < count {
		lo := int64(r.n(maxv))
		width := int64(1 + r.n(24))
		hi := lo + width
		key := [2]int64{lo, hi}
		if unique[key] {
			continue
		}
		unique[key] = true
		p := &ivPayload{
			id:   seq,
			name: fmt.Sprintf("iv-%d", seq),
			blob: []byte{byte(lo), byte(hi), byte(seq)},
		}
		seq++
		payloadSink = append(payloadSink, p)
		added := tree.Insert(lo, hi, p)
		ck(added, fmt.Sprintf("insert reported existing key [%d,%d]", lo, hi))
		model[key] = p
	}
	ck(tree.Size() == len(model), fmt.Sprintf("tree size got=%d want=%d", tree.Size(), len(model)))
	if err := tree.check(); err != nil {
		ck(false, fmt.Sprintf("tree invariant after insert: %v", err))
		return
	}

	gcStress()
	if err := tree.check(); err != nil {
		ck(false, fmt.Sprintf("tree invariant after GC: %v", err))
		return
	}

	// Stab 与 Intersects 对模型做全量比对（结果排序后比较）。
	for q := 0; q < queries; q++ {
		p := int64(r.n(maxv + 32))
		var want []*ivPayload
		for key, iv := range model {
			if key[0] <= p && p <= key[1] {
				want = append(want, iv)
			}
		}
		got := sortVals(tree.Stab(p))
		ck(sameVals(got, sortVals(want)), fmt.Sprintf("Stab mismatch at p=%d got=%d want=%d", p, len(got), len(want)))

		lo := int64(r.n(maxv))
		hi := lo + int64(r.n(48))
		want = want[:0]
		for key, iv := range model {
			if key[0] <= hi && key[1] >= lo {
				want = append(want, iv)
			}
		}
		got = sortVals(tree.Intersects(lo, hi))
		ck(sameVals(got, sortVals(want)), fmt.Sprintf("Intersects mismatch [%d,%d] got=%d want=%d", lo, hi, len(got), len(want)))
		if atomic.LoadInt32(&failed) != 0 {
			return
		}
	}

	// 重复区间更新：返回 false，且 Stab 看到新指针（取最小 key，保持确定性）。
	var first [2]int64
	firstSet := false
	for k := range model {
		if !firstSet || k[0] < first[0] || (k[0] == first[0] && k[1] < first[1]) {
			first, firstSet = k, true
		}
	}
	newVal := &ivPayload{id: -1, name: "updated", blob: []byte{0xaa}}
	payloadSink = append(payloadSink, newVal)
	added := tree.Insert(first[0], first[1], newVal)
	ck(!added, "duplicate interval insert should update not insert")
	model[first] = newVal
	ck(sameVals(sortVals(tree.Stab(first[0])), sortVals(func() []*ivPayload {
		var want []*ivPayload
		for key, iv := range model {
			if key[0] <= first[0] && first[0] <= key[1] {
				want = append(want, iv)
			}
		}
		return want
	}())), "duplicate interval update not visible via Stab")

	// 删除三分之一区间并 GC 复查（按 key 排序后删除，保持确定性）。
	var keys [][2]int64
	for k := range model {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i][0] != keys[j][0] {
			return keys[i][0] < keys[j][0]
		}
		return keys[i][1] < keys[j][1]
	})
	deleted := 0
	for _, k := range keys {
		if k[0]%3 == 0 && deleted < count/3 {
			ck(tree.Delete(k[0], k[1]), fmt.Sprintf("delete missing interval [%d,%d]", k[0], k[1]))
			delete(model, k)
			deleted++
		}
	}
	ck(!tree.Delete(-1, -2), "delete absent interval returned true")
	ck(tree.Size() == len(model), fmt.Sprintf("size after delete got=%d want=%d", tree.Size(), len(model)))
	gcStress()
	if err := tree.check(); err != nil {
		ck(false, fmt.Sprintf("tree invariant after delete+GC: %v", err))
		return
	}
	for q := 0; q < queries/2; q++ {
		p := int64(r.n(maxv + 32))
		var want []*ivPayload
		for key, iv := range model {
			if key[0] <= p && p <= key[1] {
				want = append(want, iv)
			}
		}
		ck(sameVals(sortVals(tree.Stab(p)), sortVals(want)), fmt.Sprintf("post-delete Stab mismatch at p=%d", p))
		if atomic.LoadInt32(&failed) != 0 {
			return
		}
	}
	inOrder := tree.inOrder()
	ck(len(inOrder) == len(model), "in-order count mismatch")
	for _, p := range inOrder {
		ck(p != nil && p.id != 0 && len(p.blob) > 0 && p.name != "", "payload corrupted after GC")
	}
}

func runStringTree() {
	tree := &intervalTree[string, ivPayload]{}
	items := []struct {
		lo, hi, name string
	}{
		{"a", "c", "A"},
		{"b", "f", "B"},
		{"m", "z", "C"},
		{"c", "e", "D"},
	}
	for i, it := range items {
		p := &ivPayload{id: int64(i + 1), name: it.name, blob: []byte(it.name)}
		payloadSink = append(payloadSink, p)
		ck(tree.Insert(it.lo, it.hi, p), "string interval insert failed")
	}
	gcStress()
	if err := tree.check(); err != nil {
		ck(false, fmt.Sprintf("string tree invariant: %v", err))
		return
	}
	names := func(vs []*ivPayload) string {
		vs = sortVals(vs)
		s := ""
		for _, v := range vs {
			s += v.name
		}
		return s
	}
	ck(names(tree.Stab("b")) == "AB", "string Stab(b) got="+names(tree.Stab("b")))
	ck(names(tree.Intersects("e", "n")) == "BCD", "string Intersects(e,n) got="+names(tree.Intersects("e", "n")))
	// 逆序中序遍历应保持 lo 升序。
	var prevLo string
	first := true
	for _, p := range tree.inOrder() {
		lo := ""
		for _, it := range items {
			if it.name == p.name {
				lo = it.lo
			}
		}
		if !first {
			ck(prevLo <= lo, "string in-order not sorted")
		}
		prevLo = lo
		first = false
		if p.name != string(p.blob) {
			ck(false, "string payload corrupted")
		}
	}
}

func main() {
	uafSeed = envSeed()
	rounds := envInt("TORTURE_ROUNDS", 3)
	count := envInt("TORTURE_INTERVALS", 320)
	queries := envInt("TORTURE_QUERIES", 220)

	for round := 0; round < rounds; round++ {
		runRound(uafSeed, round, count, queries)
		if atomic.LoadInt32(&failed) != 0 {
			finish("l01_generic_interval_tree")
		}
	}
	runStringTree()

	finish("l01_generic_interval_tree")
}
