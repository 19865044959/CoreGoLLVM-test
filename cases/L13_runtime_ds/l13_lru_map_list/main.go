// L13: map + container/list LRU：命中提升、容量淘汰、更新/删除与参考模型逐步对照，跨 GC 保活。
package main

import (
	"container/list"
	"fmt"
	"math/rand"
	"os"
	"runtime"
	"strconv"
	"sync/atomic"
)

type item struct {
	key  string
	val  int64
	salt int64
	pad  []byte
}

type lru struct {
	capacity int
	ll       *list.List
	idx      map[string]*list.Element
}

type model struct {
	capacity int
	order    []string
	vals     map[string]*item
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

func envInt(name string, def int) int {
	s := os.Getenv(name)
	if s == "" {
		return def
	}
	v, err := strconv.Atoi(s)
	if err != nil || v < 1 {
		return def
	}
	if v > 200 {
		return 200
	}
	return v
}

func newItem(key string, val, salt int64) *item {
	it := &item{key: key, val: val, salt: salt, pad: make([]byte, 10)}
	for i := range it.pad {
		it.pad[i] = byte(int(val)*13 + i*5 + int(salt))
	}
	return it
}

func validItem(it *item, key string, salt int64) bool {
	if it == nil || it.key != key || it.salt != salt || len(it.pad) != 10 {
		return false
	}
	for i := range it.pad {
		if it.pad[i] != byte(int(it.val)*13+i*5+int(salt)) {
			return false
		}
	}
	return true
}

func newLRU(capacity int) *lru {
	return &lru{capacity: capacity, ll: list.New(), idx: make(map[string]*list.Element, capacity+1)}
}

func (l *lru) get(k string) (*item, bool) {
	el, ok := l.idx[k]
	if !ok {
		return nil, false
	}
	l.ll.MoveToFront(el)
	return el.Value.(*item), true
}

func (l *lru) put(it *item) *item {
	if el, ok := l.idx[it.key]; ok {
		el.Value = it
		l.ll.MoveToFront(el)
		return nil
	}
	el := l.ll.PushFront(it)
	l.idx[it.key] = el
	if l.ll.Len() > l.capacity {
		back := l.ll.Back()
		ev := back.Value.(*item)
		l.ll.Remove(back)
		delete(l.idx, ev.key)
		return ev
	}
	return nil
}

func (l *lru) remove(k string) *item {
	el, ok := l.idx[k]
	if !ok {
		return nil
	}
	delete(l.idx, k)
	l.ll.Remove(el)
	return el.Value.(*item)
}

func (l *lru) order() []string {
	out := make([]string, 0, l.ll.Len())
	for el := l.ll.Front(); el != nil; el = el.Next() {
		out = append(out, el.Value.(*item).key)
	}
	return out
}

func (l *lru) consistent() bool {
	if l.ll.Len() != len(l.idx) {
		return false
	}
	n := 0
	for el := l.ll.Front(); el != nil; el = el.Next() {
		it, ok := el.Value.(*item)
		if !ok || l.idx[it.key] != el {
			return false
		}
		n++
	}
	return n == len(l.idx)
}

func newModel(capacity int) *model {
	return &model{capacity: capacity, vals: make(map[string]*item, capacity+1)}
}

func (m *model) find(k string) int {
	for i, key := range m.order {
		if key == k {
			return i
		}
	}
	return -1
}

func (m *model) moveFront(i int) {
	k := m.order[i]
	copy(m.order[1:i+1], m.order[:i])
	m.order[0] = k
}

func (m *model) get(k string) (*item, bool) {
	i := m.find(k)
	if i < 0 {
		return nil, false
	}
	it := m.vals[k]
	m.moveFront(i)
	return it, true
}

func (m *model) put(it *item) *item {
	i := m.find(it.key)
	if i >= 0 {
		m.vals[it.key] = it
		m.moveFront(i)
		return nil
	}
	m.order = append(m.order, "")
	copy(m.order[1:], m.order[:len(m.order)-1])
	m.order[0] = it.key
	m.vals[it.key] = it
	if len(m.order) > m.capacity {
		evKey := m.order[len(m.order)-1]
		ev := m.vals[evKey]
		m.order = m.order[:len(m.order)-1]
		delete(m.vals, evKey)
		return ev
	}
	return nil
}

func (m *model) remove(k string) *item {
	i := m.find(k)
	if i < 0 {
		return nil
	}
	it := m.vals[k]
	m.order = append(m.order[:i], m.order[i+1:]...)
	delete(m.vals, k)
	return it
}

func sameOrder(a, b []string) bool {
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

func main() {
	curSeed = envSeed()

	// 1) 固定小容量序列，逐步核对命中提升与淘汰。
	l := newLRU(3)
	l.put(newItem("A", 1, curSeed))
	l.put(newItem("B", 2, curSeed))
	l.put(newItem("C", 3, curSeed))
	ck(sameOrder(l.order(), []string{"C", "B", "A"}), "lru: initial order")
	_, hit := l.get("A")
	ck(hit && sameOrder(l.order(), []string{"A", "C", "B"}), "lru: get move-front")
	ev := l.put(newItem("D", 4, curSeed))
	ck(ev != nil && ev.key == "B", "lru: eviction victim")
	ck(sameOrder(l.order(), []string{"D", "A", "C"}), "lru: order after evict")
	if _, ok := l.get("B"); ok {
		ck(false, "lru: evicted key visible")
	}
	l.put(newItem("C", 33, curSeed))
	ck(sameOrder(l.order(), []string{"C", "D", "A"}), "lru: update move-front")
	rm := l.remove("D")
	ck(rm != nil && rm.key == "D" && sameOrder(l.order(), []string{"C", "A"}), "lru: remove")
	gcStress()
	ck(l.consistent(), "lru: inconsistent after basic GC")

	rounds := envInt("TORTURE_ROUNDS", 3)
	for round := 0; round < rounds; round++ {
		salt := curSeed + int64(round)*571
		rnd := rand.New(rand.NewSource(curSeed + int64(round)*31))
		const capacity = 64
		const ops = 1800
		real := newLRU(capacity)
		ref := newModel(capacity)

		for step := 0; step < ops; step++ {
			key := fmt.Sprintf("k%03d", rnd.Intn(120))
			switch c := rnd.Intn(100); {
			case c < 45:
				it := newItem(key, int64(step)+int64(round)*1000000, salt)
				ev1 := real.put(it)
				ev2 := ref.put(it)
				if (ev1 == nil) != (ev2 == nil) || (ev1 != nil && ev1.key != ev2.key) {
					ckSeed(false, fmt.Sprintf("lru: put eviction mismatch round %d step %d", round, step))
					step = ops
					continue
				}
				if real.order()[0] != key {
					ckSeed(false, fmt.Sprintf("lru: put not front round %d step %d", round, step))
					step = ops
					continue
				}
			case c < 80:
				v1, ok1 := real.get(key)
				v2, ok2 := ref.get(key)
				if ok1 != ok2 || v1 != v2 {
					ckSeed(false, fmt.Sprintf("lru: get mismatch round %d step %d", round, step))
					step = ops
					continue
				}
			default:
				v1 := real.remove(key)
				v2 := ref.remove(key)
				if v1 != v2 {
					ckSeed(false, fmt.Sprintf("lru: remove mismatch round %d step %d", round, step))
					step = ops
					continue
				}
			}
			if !sameOrder(real.order(), ref.order) {
				ckSeed(false, fmt.Sprintf("lru: order mismatch round %d step %d", round, step))
				step = ops
				continue
			}
			if step%250 == 249 {
				gcStress()
				if !real.consistent() {
					ckSeed(false, fmt.Sprintf("lru: list/map inconsistent round %d step %d", round, step))
					step = ops
					continue
				}
				for el := real.ll.Front(); el != nil; el = el.Next() {
					it := el.Value.(*item)
					if !validItem(it, it.key, salt) || ref.vals[it.key] != it {
						ckSeed(false, fmt.Sprintf("lru: payload corrupt round %d step %d", round, step))
						step = ops
						break
					}
				}
			}
		}
		ck(real.consistent() && sameOrder(real.order(), ref.order), "lru: final state")
		gcStress()
		ck(real.consistent() && sameOrder(real.order(), ref.order), "lru: final state after GC")
		runtime.KeepAlive(real)
		runtime.KeepAlive(ref)
	}

	finish("l13_lru_map_list")
}
