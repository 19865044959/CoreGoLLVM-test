// L01: 泛型索引堆（带句柄更新/删除）+ 泛型自定义比较器 k 路归并。
// 对应 etcd lease 到期堆 RegisterOrUpdate/Poll/回填 与 badger 多路归并迭代器。
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

type pqPayload struct {
	id   int64
	name string
	blob []byte
}

type heapItem[K cmp.Ordered, V any] struct {
	key K
	val V
	idx int
}

// indexHeap 是 key 唯一的二叉最小堆，where 提供 O(log n) 句柄定位。
type indexHeap[K cmp.Ordered, V any] struct {
	items []*heapItem[K, V]
	where map[K]*heapItem[K, V]
}

func newIndexHeap[K cmp.Ordered, V any]() *indexHeap[K, V] {
	return &indexHeap[K, V]{where: make(map[K]*heapItem[K, V])}
}

func (h *indexHeap[K, V]) Len() int { return len(h.items) }

func (h *indexHeap[K, V]) less(i, j int) bool {
	return cmp.Compare(h.items[i].key, h.items[j].key) < 0
}

func (h *indexHeap[K, V]) swap(i, j int) {
	h.items[i], h.items[j] = h.items[j], h.items[i]
	h.items[i].idx = i
	h.items[j].idx = j
}

func (h *indexHeap[K, V]) up(j int) bool {
	moved := false
	for {
		i := (j - 1) / 2
		if i == j || !h.less(j, i) {
			break
		}
		h.swap(i, j)
		j = i
		moved = true
	}
	return moved
}

func (h *indexHeap[K, V]) down(i int) bool {
	n := len(h.items)
	moved := false
	for {
		smallest := i
		l := 2*i + 1
		r := l + 1
		if l < n && h.less(l, smallest) {
			smallest = l
		}
		if r < n && h.less(r, smallest) {
			smallest = r
		}
		if smallest == i {
			break
		}
		h.swap(i, smallest)
		i = smallest
		moved = true
	}
	return moved
}

func (h *indexHeap[K, V]) Push(k K, v V) {
	it := &heapItem[K, V]{key: k, val: v, idx: len(h.items)}
	h.items = append(h.items, it)
	h.where[k] = it
	h.up(it.idx)
}

// Upsert 对应 lease RegisterOrUpdate：已存在 key 只更新值。
func (h *indexHeap[K, V]) Upsert(k K, v V) (old V, existed bool) {
	if it, ok := h.where[k]; ok {
		old = it.val
		it.val = v
		return old, true
	}
	h.Push(k, v)
	return old, false
}

func (h *indexHeap[K, V]) Get(k K) (V, bool) {
	it, ok := h.where[k]
	if !ok {
		var zero V
		return zero, false
	}
	return it.val, true
}

func (h *indexHeap[K, V]) Remove(k K) (V, bool) {
	it, ok := h.where[k]
	if !ok {
		var zero V
		return zero, false
	}
	i := it.idx
	last := len(h.items) - 1
	h.swap(i, last)
	h.items = h.items[:last]
	delete(h.where, k)
	if i < last {
		if !h.down(i) {
			h.up(i)
		}
	}
	return it.val, true
}

func (h *indexHeap[K, V]) Pop() (K, V, bool) {
	var k K
	var v V
	if len(h.items) == 0 {
		return k, v, false
	}
	it := h.items[0]
	k, v = it.key, it.val
	h.Remove(k)
	return k, v, true
}

func (h *indexHeap[K, V]) check() error {
	if len(h.where) != len(h.items) {
		return fmt.Errorf("index size got=%d want=%d", len(h.where), len(h.items))
	}
	for i, it := range h.items {
		if it.idx != i {
			return fmt.Errorf("idx mismatch at %d: got=%d", i, it.idx)
		}
		if got, ok := h.where[it.key]; !ok || got != it {
			return fmt.Errorf("where handle mismatch for item %d", i)
		}
		if i > 0 && h.less(i, (i-1)/2) {
			return fmt.Errorf("heap order violated at %d", i)
		}
	}
	return nil
}

// flexHeap 允许重复 key，用外部 less 定义顺序（badger 归并迭代器语义）。
type flexHeap[T any] struct {
	items []T
	less  func(a, b T) bool
}

func (h *flexHeap[T]) Len() int { return len(h.items) }

func (h *flexHeap[T]) swap(i, j int) { h.items[i], h.items[j] = h.items[j], h.items[i] }

func (h *flexHeap[T]) up(j int) {
	for {
		i := (j - 1) / 2
		if i == j || !h.less(h.items[j], h.items[i]) {
			break
		}
		h.swap(i, j)
		j = i
	}
}

func (h *flexHeap[T]) down(i int) {
	n := len(h.items)
	for {
		smallest := i
		l := 2*i + 1
		r := l + 1
		if l < n && h.less(h.items[l], h.items[smallest]) {
			smallest = l
		}
		if r < n && h.less(h.items[r], h.items[smallest]) {
			smallest = r
		}
		if smallest == i {
			break
		}
		h.swap(i, smallest)
		i = smallest
	}
}

func (h *flexHeap[T]) Push(v T) {
	h.items = append(h.items, v)
	h.up(len(h.items) - 1)
}

func (h *flexHeap[T]) Pop() (T, bool) {
	var zero T
	if len(h.items) == 0 {
		return zero, false
	}
	v := h.items[0]
	last := len(h.items) - 1
	h.swap(0, last)
	h.items = h.items[:last]
	if last > 0 {
		h.down(0)
	}
	return v, true
}

type mergeCursor struct {
	val  int64
	list int
	pos  int
}

var (
	failed      int32
	gcSink      [][]byte
	payloadSink []*pqPayload
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

func newRNG(seed int64) *rng64 {
	s := uint64(seed)*0x9e3779b97f4a7c15 + 0x1234567
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

func newPayload(step, key int64) *pqPayload {
	p := &pqPayload{
		id:   step*1000 + key,
		name: fmt.Sprintf("k%d-step%d", key, step),
		blob: make([]byte, 8+int((step+key)%9)),
	}
	p.blob[0] = byte(key)
	p.blob[len(p.blob)-1] = byte(step)
	return p
}

func modelMin(model map[int64]*pqPayload) (int64, *pqPayload, bool) {
	var mk int64
	var mv *pqPayload
	found := false
	for k, v := range model {
		if !found || k < mk {
			mk, mv, found = k, v, true
		}
	}
	return mk, mv, found
}

func runHeapRound(seed int64, round, steps, keySpace int) {
	r := newRNG(seed + int64(round)*7919)
	h := newIndexHeap[int64, *pqPayload]()
	model := make(map[int64]*pqPayload)

	for step := 0; step < steps; step++ {
		switch r.n(6) {
		case 0, 1, 2: // insert
			k := int64(r.n(keySpace))
			p := newPayload(int64(step), k)
			payloadSink = append(payloadSink, p)
			old, existed := h.Upsert(k, p)
			oldModel, mex := model[k]
			ck(existed == mex, fmt.Sprintf("upsert existence mismatch step=%d key=%d", step, k))
			if existed {
				ck(old == oldModel, fmt.Sprintf("upsert old pointer mismatch step=%d key=%d", step, k))
			}
			model[k] = p
		case 3: // remove
			k := int64(r.n(keySpace))
			got, ok := h.Remove(k)
			want, mex := model[k]
			ck(ok == mex, fmt.Sprintf("remove existence mismatch step=%d key=%d", step, k))
			if mex {
				ck(got == want, fmt.Sprintf("remove pointer mismatch step=%d key=%d", step, k))
				delete(model, k)
			}
		case 4: // pop minimum
			k, got, ok := h.Pop()
			mk, want, mex := modelMin(model)
			ck(ok == mex, fmt.Sprintf("pop empty mismatch step=%d", step))
			if mex {
				ck(k == mk, fmt.Sprintf("pop key mismatch step=%d got=%d want=%d", step, k, mk))
				ck(got == want, fmt.Sprintf("pop pointer mismatch step=%d key=%d", step, k))
				delete(model, mk)
			}
		default: // lookup + length
			k := int64(r.n(keySpace))
			got, ok := h.Get(k)
			want, mex := model[k]
			ck(ok == mex, fmt.Sprintf("get existence mismatch step=%d key=%d", step, k))
			if mex {
				ck(got == want, fmt.Sprintf("get pointer mismatch step=%d key=%d", step, k))
				if got != nil {
					ck(got.blob[0] == byte(k) && got.name != "", "payload corrupted after lookup")
				}
			}
			ck(h.Len() == len(model), fmt.Sprintf("len mismatch step=%d got=%d want=%d", step, h.Len(), len(model)))
		}

		if step%97 == 0 {
			gcStress()
			if err := h.check(); err != nil {
				ck(false, fmt.Sprintf("heap invariant after GC step=%d: %v", step, err))
				return
			}
			for k, want := range model {
				got, ok := h.Get(k)
				ck(ok && got == want, fmt.Sprintf("post-GC pointer mismatch step=%d key=%d", step, k))
				if ok && got != nil {
					ck(got.blob[0] == byte(k), fmt.Sprintf("post-GC blob corrupted step=%d key=%d", step, k))
				}
			}
		}
		if atomic.LoadInt32(&failed) != 0 {
			return
		}
	}

	for h.Len() > 0 {
		k, got, ok := h.Pop()
		mk, want, mex := modelMin(model)
		ck(ok && mex, "drain existence mismatch")
		if !ok || !mex {
			return
		}
		ck(k == mk && got == want, fmt.Sprintf("drain order mismatch key=%d want=%d", k, mk))
		delete(model, mk)
	}
	ck(len(model) == 0, "model not empty after drain")
	if err := h.check(); err != nil {
		ck(false, fmt.Sprintf("heap invariant after drain: %v", err))
	}
}

func runMerge(seed int64) {
	lists := make([][]int64, 5+int(seed%4))
	var all []int64
	for i := range lists {
		l := make([]int64, 9+i*4)
		v := i * 3
		for j := range l {
			l[j] = int64(v)
			v += 1 + (i+j)%4
		}
		lists[i] = l
		all = append(all, l...)
	}

	// 正向归并：值升序，平局按 list/pos 稳定。
	lessFwd := func(a, b mergeCursor) bool {
		if a.val != b.val {
			return a.val < b.val
		}
		if a.list != b.list {
			return a.list < b.list
		}
		return a.pos < b.pos
	}
	h := &flexHeap[mergeCursor]{less: lessFwd}
	for i, l := range lists {
		if len(l) > 0 {
			h.Push(mergeCursor{val: l[0], list: i, pos: 0})
		}
	}
	var got []int64
	for h.Len() > 0 {
		c, _ := h.Pop()
		got = append(got, c.val)
		if c.pos+1 < len(lists[c.list]) {
			h.Push(mergeCursor{val: lists[c.list][c.pos+1], list: c.list, pos: c.pos + 1})
		}
	}
	ref := append([]int64(nil), all...)
	sort.Slice(ref, func(i, j int) bool { return ref[i] < ref[j] })
	ck(len(got) == len(ref), fmt.Sprintf("merge length got=%d want=%d", len(got), len(ref)))
	for i := 0; i < len(got) && i < len(ref); i++ {
		ck(got[i] == ref[i], fmt.Sprintf("merge order mismatch at %d got=%d want=%d", i, got[i], ref[i]))
	}

	// 反向归并：从每个 list 尾部开始，降序。
	lessRev := func(a, b mergeCursor) bool { return lessFwd(b, a) }
	hr := &flexHeap[mergeCursor]{less: lessRev}
	for i, l := range lists {
		if len(l) > 0 {
			hr.Push(mergeCursor{val: l[len(l)-1], list: i, pos: len(l) - 1})
		}
	}
	got = got[:0]
	for hr.Len() > 0 {
		c, _ := hr.Pop()
		got = append(got, c.val)
		if c.pos > 0 {
			hr.Push(mergeCursor{val: lists[c.list][c.pos-1], list: c.list, pos: c.pos - 1})
		}
	}
	sort.Slice(ref, func(i, j int) bool { return ref[i] > ref[j] })
	for i := 0; i < len(got) && i < len(ref); i++ {
		ck(got[i] == ref[i], fmt.Sprintf("reverse merge mismatch at %d got=%d want=%d", i, got[i], ref[i]))
	}
}

func runStringHeap(seed int64) {
	h := newIndexHeap[string, *pqPayload]()
	keys := make([]string, 0, 40)
	for i := 0; i < 40; i++ {
		keys = append(keys, fmt.Sprintf("svc-%02d", (i*17+int(seed))%100))
	}
	// 去重后乱序插入。
	seen := make(map[string]bool)
	var uniq []string
	for _, k := range keys {
		if !seen[k] {
			seen[k] = true
			uniq = append(uniq, k)
		}
	}
	for i := len(uniq) - 1; i >= 0; i-- {
		p := &pqPayload{id: int64(i), name: uniq[i], blob: []byte(uniq[i])}
		payloadSink = append(payloadSink, p)
		h.Upsert(uniq[i], p)
	}
	gcStress()
	sort.Strings(uniq)
	for _, want := range uniq {
		k, got, ok := h.Pop()
		ck(ok, "string heap unexpectedly empty")
		ck(k == want, fmt.Sprintf("string heap order got=%q want=%q", k, want))
		ck(got != nil && got.name == want, "string heap payload mismatch")
		if got != nil {
			ck(string(got.blob) == want, "string heap blob corrupted after GC")
		}
	}
	ck(h.Len() == 0, "string heap not drained")
}

func main() {
	uafSeed = envSeed()
	rounds := envInt("TORTURE_ROUNDS", 3)
	steps := envInt("TORTURE_STEPS", 500)
	keySpace := 48 + int(uafSeed%17)

	for round := 0; round < rounds; round++ {
		runHeapRound(uafSeed, round, steps, keySpace)
		if atomic.LoadInt32(&failed) != 0 {
			finish("l01_generic_heap_pq")
		}
	}
	runMerge(uafSeed)
	runStringHeap(uafSeed)

	finish("l01_generic_heap_pq")
}
