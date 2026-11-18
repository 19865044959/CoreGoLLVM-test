// L10: 生成风格容器访问器：map/slice 的 Get/Set/Append/Put/Delete/At/Snapshot
// 与 nil/空容器、调用方别名、GC 后视图存活。
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

// ---- 生成风格容器访问器 ----

type item struct {
	ID   int32
	Name string
	Blob []byte
}

type msg struct {
	tags   []string
	labels map[string]string
	items  []*item
	index  map[int32]*item
}

func (m *msg) GetTags() []string {
	if m == nil {
		return nil
	}
	return m.tags
}

func (m *msg) SetTags(v []string) {
	if m == nil {
		panic("accessors: SetTags on nil receiver")
	}
	m.tags = v
}

func (m *msg) AppendTags(v ...string) {
	if m == nil {
		panic("accessors: AppendTags on nil receiver")
	}
	m.tags = append(m.tags, v...)
}

func (m *msg) TagCount() int {
	if m == nil {
		return 0
	}
	return len(m.tags)
}

func (m *msg) GetLabels() map[string]string {
	if m == nil {
		return nil
	}
	return m.labels
}

func (m *msg) PutLabel(k, v string) {
	if m == nil {
		panic("accessors: PutLabel on nil receiver")
	}
	if m.labels == nil {
		m.labels = make(map[string]string)
	}
	m.labels[k] = v
}

func (m *msg) GetLabel(k string) string {
	if m == nil {
		return ""
	}
	return m.labels[k]
}

func (m *msg) HasLabel(k string) bool {
	if m == nil {
		return false
	}
	_, ok := m.labels[k]
	return ok
}

func (m *msg) DeleteLabel(k string) {
	if m == nil {
		panic("accessors: DeleteLabel on nil receiver")
	}
	delete(m.labels, k)
}

func (m *msg) LabelCount() int {
	if m == nil {
		return 0
	}
	return len(m.labels)
}

func (m *msg) SortedLabelKeys() []string {
	if m == nil || m.labels == nil {
		return nil
	}
	keys := make([]string, 0, len(m.labels))
	for k := range m.labels {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func (m *msg) GetItems() []*item {
	if m == nil {
		return nil
	}
	return m.items
}

func (m *msg) SetItems(v []*item) {
	if m == nil {
		panic("accessors: SetItems on nil receiver")
	}
	m.items = v
}

func (m *msg) AddItem(it *item) {
	if m == nil {
		panic("accessors: AddItem on nil receiver")
	}
	m.items = append(m.items, it)
}

func (m *msg) ItemAt(i int) *item {
	if m == nil || i < 0 || i >= len(m.items) {
		return nil
	}
	return m.items[i]
}

func (m *msg) ItemCount() int {
	if m == nil {
		return 0
	}
	return len(m.items)
}

func (m *msg) GetIndex() map[int32]*item {
	if m == nil {
		return nil
	}
	return m.index
}

func (m *msg) PutIndex(id int32, it *item) {
	if m == nil {
		panic("accessors: PutIndex on nil receiver")
	}
	if m.index == nil {
		m.index = make(map[int32]*item)
	}
	m.index[id] = it
}

func (m *msg) RemoveIndex(id int32) {
	if m == nil {
		panic("accessors: RemoveIndex on nil receiver")
	}
	delete(m.index, id)
}

func (m *msg) SortedIndexIDs() []int32 {
	if m == nil || m.index == nil {
		return nil
	}
	ids := make([]int32, 0, len(m.index))
	for id := range m.index {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	return ids
}

func (m *msg) ClearContainers() {
	if m == nil {
		panic("accessors: ClearContainers on nil receiver")
	}
	m.tags, m.labels, m.items, m.index = nil, nil, nil, nil
}

func copyItem(it *item) *item {
	if it == nil {
		return nil
	}
	out := &item{ID: it.ID, Name: it.Name}
	if it.Blob != nil {
		out.Blob = make([]byte, len(it.Blob))
		copy(out.Blob, it.Blob)
	}
	return out
}

func (m *msg) Snapshot() *msg {
	if m == nil {
		return nil
	}
	out := &msg{}
	if m.tags != nil {
		out.tags = append([]string{}, m.tags...)
	}
	if m.labels != nil {
		out.labels = make(map[string]string, len(m.labels))
		for k, v := range m.labels {
			out.labels[k] = v
		}
	}
	if m.items != nil {
		out.items = make([]*item, len(m.items))
		for i, it := range m.items {
			out.items[i] = copyItem(it)
		}
	}
	if m.index != nil {
		out.index = make(map[int32]*item, len(m.index))
		for id, it := range m.index {
			out.index[id] = copyItem(it)
		}
	}
	return out
}

func dump(m *msg) string {
	if m == nil {
		return "<nil>"
	}
	s := "tags:"
	if m.tags == nil {
		s += "<nil>"
	} else {
		s += fmt.Sprintf("%d", len(m.tags))
		for _, t := range m.tags {
			s += "|" + t
		}
	}
	s += ";labels:"
	if m.labels == nil {
		s += "<nil>"
	} else {
		keys := make([]string, 0, len(m.labels))
		for k := range m.labels {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			s += "|" + k + "=" + m.labels[k]
		}
	}
	s += ";items:"
	if m.items == nil {
		s += "<nil>"
	} else {
		s += fmt.Sprintf("%d", len(m.items))
		for _, it := range m.items {
			s += ";"
			if it == nil {
				s += "<nil>"
			} else {
				s += fmt.Sprintf("%d,%s,%x", it.ID, it.Name, it.Blob)
			}
		}
	}
	s += ";index:"
	if m.index == nil {
		s += "<nil>"
	} else {
		ids := make([]int32, 0, len(m.index))
		for id := range m.index {
			ids = append(ids, id)
		}
		sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
		for _, id := range ids {
			it := m.index[id]
			s += fmt.Sprintf("|%d:", id)
			if it == nil {
				s += "<nil>"
			} else {
				s += fmt.Sprintf("%d,%s,%x", it.ID, it.Name, it.Blob)
			}
		}
	}
	return s
}

func panics(f func()) (p bool) {
	defer func() {
		if recover() != nil {
			p = true
		}
	}()
	f()
	return false
}

func mkItem(r *rand.Rand) *item {
	b := make([]byte, r.Intn(12))
	for i := range b {
		b[i] = byte(r.Intn(256))
	}
	return &item{ID: int32(r.Intn(50)), Name: fmt.Sprintf("it-%d", r.Intn(1000)), Blob: b}
}

func copyTags(v []string) []string {
	if v == nil {
		return nil
	}
	return append([]string{}, v...)
}

func copyLabels(v map[string]string) map[string]string {
	if v == nil {
		return nil
	}
	out := make(map[string]string, len(v))
	for k, x := range v {
		out[k] = x
	}
	return out
}

func copyItems(v []*item) []*item {
	if v == nil {
		return nil
	}
	out := make([]*item, len(v))
	for i, it := range v {
		out[i] = copyItem(it)
	}
	return out
}

func copyIndex(v map[int32]*item) map[int32]*item {
	if v == nil {
		return nil
	}
	out := make(map[int32]*item, len(v))
	for id, it := range v {
		out[id] = copyItem(it)
	}
	return out
}

func main() {
	curSeed = 1
	if s := os.Getenv("UAF_SEED"); s != "" {
		if v, err := strconv.ParseInt(s, 10, 64); err == nil {
			curSeed = v
		}
	}

	// 1. 零值与 nil receiver。
	z := &msg{}
	ck(z.GetTags() == nil && z.GetLabels() == nil && z.GetItems() == nil && z.GetIndex() == nil,
		"zero value getters return nil")
	ck(z.TagCount() == 0 && z.LabelCount() == 0 && z.ItemCount() == 0, "zero value counts")
	ck(z.ItemAt(0) == nil && z.ItemAt(-1) == nil, "zero value ItemAt bounds")
	ck(z.SortedLabelKeys() == nil && z.SortedIndexIDs() == nil, "zero value sorted keys nil")
	var nilMsg *msg
	ck(nilMsg.GetTags() == nil && nilMsg.GetLabels() == nil && nilMsg.GetItems() == nil && nilMsg.GetIndex() == nil,
		"nil receiver getters return nil")
	ck(nilMsg.GetLabel("k") == "" && !nilMsg.HasLabel("k") && nilMsg.ItemAt(0) == nil, "nil receiver scalar getters")
	ck(nilMsg.TagCount() == 0 && nilMsg.LabelCount() == 0 && nilMsg.ItemCount() == 0, "nil receiver counts")
	ck(panics(func() { nilMsg.SetTags(nil) }) && panics(func() { nilMsg.AppendTags("x") }),
		"nil receiver slice setters panic")
	ck(panics(func() { nilMsg.PutLabel("a", "b") }) && panics(func() { nilMsg.DeleteLabel("a") }),
		"nil receiver map mutators panic")
	ck(panics(func() { nilMsg.AddItem(nil) }) && panics(func() { nilMsg.PutIndex(1, nil) }),
		"nil receiver item mutators panic")
	ck(nilMsg.Snapshot() == nil, "nil snapshot")

	// 2. nil 与空容器语义。
	m := &msg{}
	m.SetTags(nil)
	ck(m.GetTags() == nil, "SetTags(nil) keeps nil")
	m.SetTags([]string{})
	ck(m.GetTags() != nil && len(m.GetTags()) == 0, "SetTags(empty) keeps non-nil empty")
	m.AppendTags()
	ck(m.GetTags() != nil && len(m.GetTags()) == 0, "AppendTags() on non-nil empty stays non-nil")
	m.SetTags(nil)
	m.AppendTags("a", "b")
	ck(len(m.GetTags()) == 2 && m.GetTags()[0] == "a", "AppendTags allocates from nil")
	m.SetItems(nil)
	ck(m.GetItems() == nil, "SetItems(nil) keeps nil")
	m.SetItems([]*item{})
	ck(m.GetItems() != nil && len(m.GetItems()) == 0, "SetItems(empty) keeps non-nil empty")
	m.SetItems(nil)
	m.AddItem(nil)
	ck(m.GetItems() != nil && len(m.GetItems()) == 1 && m.ItemAt(0) == nil, "AddItem(nil) preserves element")

	// 3. 访问器返回活视图；setter 接管调用方引用。
	m = &msg{}
	m.SetTags([]string{"a", "b"})
	view := m.GetTags()
	view[0] = "A"
	ck(m.GetTags()[0] == "A", "GetTags returns live slice")
	src := []string{"x", "y"}
	m.SetTags(src)
	src[0] = "X"
	ck(m.GetTags()[0] == "X", "SetTags takes ownership of caller slice")
	m.PutLabel("k", "v")
	lv := m.GetLabels()
	lv["k"] = "V"
	lv["new"] = "N"
	ck(m.GetLabel("k") == "V" && m.HasLabel("new"), "GetLabels returns live map")
	delete(lv, "new")
	ck(!m.HasLabel("new"), "live map delete visible")
	it := &item{ID: 1, Name: "one", Blob: []byte{1, 2, 3}}
	m.AddItem(it)
	m.GetItems()[0].Blob[0] = 9
	ck(it.Blob[0] == 9 && m.ItemAt(0) == it, "GetItems returns live element pointers")
	m.PutIndex(1, it)
	iv := m.GetIndex()
	iv[2] = &item{ID: 2, Name: "two"}
	ck(len(m.GetIndex()) == 2 && m.GetIndex()[2].ID == 2, "GetIndex returns live map")

	// 4. Put/Delete/Has/排序与覆盖。
	m.PutLabel("b", "2")
	m.PutLabel("a", "1")
	keys := m.SortedLabelKeys()
	ck(len(keys) == 3 && keys[0] == "a" && keys[1] == "b" && keys[2] == "k", "SortedLabelKeys deterministic")
	m.DeleteLabel("k")
	ck(!m.HasLabel("k") && m.LabelCount() == 2, "DeleteLabel")
	m.PutIndex(1, &item{ID: 7, Name: "seven"})
	ck(m.GetIndex()[1].Name == "seven" && len(m.GetIndex()) == 2, "PutIndex overwrites")
	m.RemoveIndex(2)
	ids := m.SortedIndexIDs()
	ck(len(ids) == 1 && ids[0] == 1, "RemoveIndex + SortedIndexIDs")

	// 5. Snapshot 深拷贝，源修改不影响快照；空/nil 语义保留。
	orig := &msg{
		tags:   []string{"t1", "t2"},
		labels: map[string]string{"x": "1"},
		items:  []*item{{ID: 1, Name: "a", Blob: []byte{1, 2}}, nil},
		index:  map[int32]*item{1: {ID: 1, Name: "a", Blob: []byte{3, 4}}},
	}
	snap := orig.Snapshot()
	ck(dump(snap) == dump(orig), "snapshot equals source")
	orig.tags[0] = "mut"
	orig.labels["x"] = "mut"
	orig.items[0].Blob[0] = 99
	orig.index[1].Name = "mut"
	orig.items = append(orig.items, &item{ID: 2})
	ck(dump(snap) == dump(&msg{
		tags:   []string{"t1", "t2"},
		labels: map[string]string{"x": "1"},
		items:  []*item{{ID: 1, Name: "a", Blob: []byte{1, 2}}, nil},
		index:  map[int32]*item{1: {ID: 1, Name: "a", Blob: []byte{3, 4}}},
	}), "snapshot isolated from source mutation")
	empty := &msg{tags: []string{}, labels: map[string]string{}, items: []*item{}, index: map[int32]*item{}}
	es := empty.Snapshot()
	ck(es.tags != nil && es.labels != nil && es.items != nil && es.index != nil, "snapshot preserves non-nil empties")
	ns := (&msg{}).Snapshot()
	ck(ns.tags == nil && ns.labels == nil && ns.items == nil && ns.index == nil, "snapshot preserves nils")

	// 6. GC：只保留访问器视图，丢弃消息后视图仍可读。
	m = &msg{tags: []string{"gc1", "gc2"}, labels: map[string]string{"g": "c"}}
	big := &item{ID: 3, Name: "gc-item", Blob: make([]byte, 64)}
	for i := range big.Blob {
		big.Blob[i] = byte(i)
	}
	m.AddItem(big)
	m.PutIndex(3, big)
	tagsView := m.GetTags()
	labelsView := m.GetLabels()
	itemsView := m.GetItems()
	indexView := m.GetIndex()
	itemView := m.ItemAt(0)
	m = nil
	gcSink = append(gcSink, []byte(tagsView[0]), []byte(labelsView["g"]), itemView.Blob)
	gcStress()
	ck(tagsView[0] == "gc1" && tagsView[1] == "gc2", "tags view survives GC after message dropped")
	ck(labelsView["g"] == "c", "labels view survives GC")
	ck(len(itemsView) == 1 && itemsView[0] == itemView && itemView.Name == "gc-item", "items view survives GC")
	ck(indexView[3] == itemView && len(indexView) == 1, "index view survives GC")
	for i := 0; i < len(itemView.Blob); i++ {
		if itemView.Blob[i] != byte(i) {
			ck(false, "blob view corrupted after GC")
			break
		}
	}
	// 快照在源释放、多轮 GC 后仍然完整。
	m = &msg{tags: []string{"s1"}, labels: map[string]string{"s": "v"}, items: []*item{{ID: 9, Blob: []byte{7}}}}
	keep := m.Snapshot()
	want := dump(m)
	m = nil
	gcStress()
	ck(dump(keep) == want, "snapshot survives GC after source dropped")

	// 7. 随机操作：与独立模型对账；周期性 GC 与快照。
	r := rand.New(rand.NewSource(curSeed))
	act := &msg{}
	mdl := &msg{}
	ops := 0
	for i := 0; i < 320; i++ {
		switch r.Intn(11) {
		case 0: // AppendTags
			n := r.Intn(3) + 1
			vs := make([]string, n)
			for j := range vs {
				vs[j] = fmt.Sprintf("t%d-%d", i, r.Intn(100))
			}
			act.AppendTags(vs...)
			mdl.tags = append(mdl.tags, copyTags(vs)...)
		case 1: // SetTags(nil/empty/随机)
			var vs []string
			switch r.Intn(3) {
			case 0:
				vs = nil
			case 1:
				vs = []string{}
			default:
				vs = make([]string, r.Intn(5))
				for j := range vs {
					vs[j] = fmt.Sprintf("s%d-%d", i, j)
				}
			}
			act.SetTags(vs)
			mdl.tags = copyTags(vs)
		case 2: // PutLabel
			k := fmt.Sprintf("k%d", r.Intn(8))
			v := fmt.Sprintf("v%d", r.Intn(100))
			act.PutLabel(k, v)
			if mdl.labels == nil {
				mdl.labels = make(map[string]string)
			}
			mdl.labels[k] = v
		case 3: // DeleteLabel
			k := fmt.Sprintf("k%d", r.Intn(8))
			act.DeleteLabel(k)
			if mdl.labels != nil {
				delete(mdl.labels, k)
			}
		case 4: // AddItem
			it := mkItem(r)
			act.AddItem(it)
			mdl.items = append(mdl.items, copyItem(it))
		case 5: // SetItems
			switch r.Intn(3) {
			case 0:
				act.SetItems(nil)
				mdl.items = nil
			case 1:
				act.SetItems([]*item{})
				mdl.items = []*item{}
			default:
				vals := make([]*item, r.Intn(4))
				for j := range vals {
					if r.Intn(5) == 0 {
						vals[j] = nil
					} else {
						vals[j] = mkItem(r)
					}
				}
				act.SetItems(vals)
				mdl.items = copyItems(vals)
			}
		case 6: // 通过活视图修改 blob
			if act.ItemCount() > 0 {
				idx := r.Intn(act.ItemCount())
				if it := act.ItemAt(idx); it != nil && len(it.Blob) > 0 {
					it.Blob[0] ^= 0x5a
					mdl.items[idx].Blob[0] ^= 0x5a
				}
			}
		case 7: // PutIndex
			id := int32(r.Intn(20))
			var it *item
			if r.Intn(6) != 0 {
				it = mkItem(r)
			}
			act.PutIndex(id, it)
			if mdl.index == nil {
				mdl.index = make(map[int32]*item)
			}
			mdl.index[id] = copyItem(it)
		case 8: // RemoveIndex
			id := int32(r.Intn(20))
			act.RemoveIndex(id)
			if mdl.index != nil {
				delete(mdl.index, id)
			}
		case 9: // ClearContainers
			if r.Intn(4) == 0 {
				act.ClearContainers()
				mdl.tags, mdl.labels, mdl.items, mdl.index = nil, nil, nil, nil
			}
		default: // 写入 map 访问器
			if r.Intn(2) == 0 {
				v := fmt.Sprintf("%d", i)
				act.PutLabel("view", v)
				if mdl.labels == nil {
					mdl.labels = make(map[string]string)
				}
				mdl.labels["view"] = v
			} else {
				id := int32(100 + r.Intn(5))
				it := &item{ID: id, Name: "view"}
				act.PutIndex(id, it)
				if mdl.index == nil {
					mdl.index = make(map[int32]*item)
				}
				mdl.index[id] = copyItem(it)
			}
		}
		ops++
		ckSeed(dump(act) == dump(mdl), fmt.Sprintf("random op %d/%d state mismatch", i, ops))
		if i%43 == 0 {
			gcStress()
			ckSeed(dump(act) == dump(mdl), fmt.Sprintf("random op %d after GC", i))
		}
		if i%101 == 0 {
			s := act.Snapshot()
			after := dump(act)
			gcStress()
			ckSeed(dump(s) == after && dump(act) == after, fmt.Sprintf("random op %d snapshot/GC", i))
		}
	}

	finish("l10_map_slice_accessors")
}
