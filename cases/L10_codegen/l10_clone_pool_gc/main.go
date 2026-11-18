// L10: 生成风格 Clone/CloneInto + sync.Pool 临时缓冲：GC 生命周期、
// 池化缓冲别名、复用目标容量、保留副本与源释放后的存活。
// P2：TORTURE_ROUNDS 放大克隆/GC 轮次。
package main

import (
	"fmt"
	"math/rand"
	"os"
	"reflect"
	"runtime"
	"sort"
	"strconv"
	"sync"
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

func envInt(name string, def int) int {
	s := os.Getenv(name)
	if s == "" {
		return def
	}
	v, err := strconv.Atoi(s)
	if err != nil || v < 1 {
		return def
	}
	return v
}

// ---- 生成风格 Clone ----

var scratchPool = sync.Pool{
	New: func() any {
		b := make([]byte, 0, 4096)
		return &b
	},
}

func cloneBytes(p []byte) []byte {
	if p == nil {
		return nil
	}
	bp := scratchPool.Get().(*[]byte)
	b := (*bp)[:0]
	b = append(b, p...)
	out := make([]byte, len(b))
	copy(out, b)
	for i := range b {
		b[i] = 0xee // 归还前污染，克隆若别名到池缓冲会暴露
	}
	*bp = b[:0]
	scratchPool.Put(bp)
	return out
}

type row struct {
	ID      int64
	Name    string
	Payload []byte
	Tags    []string
	Index   map[string]int64
	Kids    []*row
}

func (r *row) Clone() *row {
	if r == nil {
		return nil
	}
	out := new(row)
	r.CloneInto(out)
	return out
}

func (r *row) CloneInto(out *row) {
	if out == nil {
		panic("clone: CloneInto on nil destination")
	}
	out.ID = r.ID
	out.Name = r.Name
	out.Payload = cloneBytes(r.Payload)
	if r.Tags == nil {
		out.Tags = nil
	} else {
		out.Tags = append(out.Tags[:0], r.Tags...)
		if out.Tags == nil {
			out.Tags = []string{}
		}
	}
	if r.Index == nil {
		out.Index = nil
	} else {
		if out.Index == nil {
			out.Index = make(map[string]int64, len(r.Index))
		} else {
			for k := range out.Index {
				delete(out.Index, k)
			}
		}
		for k, v := range r.Index {
			out.Index[k] = v
		}
	}
	if r.Kids == nil {
		out.Kids = nil
	} else {
		if cap(out.Kids) < len(r.Kids) {
			out.Kids = make([]*row, len(r.Kids))
		} else {
			out.Kids = out.Kids[:len(r.Kids)]
		}
		for i, k := range r.Kids {
			out.Kids[i] = k.Clone()
		}
		if len(out.Kids) == 0 && out.Kids == nil {
			out.Kids = []*row{}
		}
	}
}

func dump(r *row) string {
	if r == nil {
		return "<nil>"
	}
	s := fmt.Sprintf("id=%d name=%s payload=", r.ID, r.Name)
	if r.Payload == nil {
		s += "<nil>"
	} else {
		s += fmt.Sprintf("%x", r.Payload)
	}
	s += ";tags="
	if r.Tags == nil {
		s += "<nil>"
	} else {
		s += fmt.Sprint(r.Tags)
	}
	s += ";index="
	if r.Index == nil {
		s += "<nil>"
	} else {
		keys := make([]string, 0, len(r.Index))
		for k := range r.Index {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			s += fmt.Sprintf("%s:%d,", k, r.Index[k])
		}
	}
	s += ";kids="
	if r.Kids == nil {
		s += "<nil>"
	} else {
		s += fmt.Sprintf("%d[", len(r.Kids))
		for _, k := range r.Kids {
			s += dump(k) + ";"
		}
		s += "]"
	}
	return s
}

func sliceAliased(a, b []byte) bool {
	if len(a) == 0 || len(b) == 0 {
		return false
	}
	return &a[0] == &b[0]
}

func strSliceAliased(a, b []string) bool {
	if len(a) == 0 || len(b) == 0 {
		return false
	}
	return &a[0] == &b[0]
}

func noShared(a, b *row) bool {
	if a == nil || b == nil {
		return a == b
	}
	if a == b {
		return false
	}
	if sliceAliased(a.Payload, b.Payload) || strSliceAliased(a.Tags, b.Tags) {
		return false
	}
	if a.Index != nil && b.Index != nil && reflect.ValueOf(a.Index).Pointer() == reflect.ValueOf(b.Index).Pointer() {
		return false
	}
	if (a.Kids == nil) != (b.Kids == nil) || len(a.Kids) != len(b.Kids) {
		return false
	}
	for i := range a.Kids {
		if !noShared(a.Kids[i], b.Kids[i]) {
			return false
		}
	}
	return true
}

func randRow(r *rand.Rand, depth int) *row {
	n := &row{ID: r.Int63n(1 << 40), Name: fmt.Sprintf("r%d", r.Intn(100000))}
	switch r.Intn(4) {
	case 0:
		n.Payload = nil
	case 1:
		n.Payload = []byte{}
	default:
		n.Payload = make([]byte, r.Intn(40))
		for i := range n.Payload {
			n.Payload[i] = byte(r.Intn(256))
		}
	}
	switch r.Intn(4) {
	case 0:
		n.Tags = nil
	case 1:
		n.Tags = []string{}
	default:
		n.Tags = make([]string, r.Intn(4))
		for i := range n.Tags {
			n.Tags[i] = fmt.Sprintf("t%d", r.Intn(100))
		}
	}
	switch r.Intn(4) {
	case 0:
		n.Index = nil
	case 1:
		n.Index = map[string]int64{}
	default:
		n.Index = make(map[string]int64)
		for i := 0; i < r.Intn(4); i++ {
			n.Index[fmt.Sprintf("k%d", r.Intn(10))] = r.Int63n(1000)
		}
	}
	if depth > 0 {
		switch r.Intn(4) {
		case 0:
			n.Kids = nil
		case 1:
			n.Kids = []*row{}
		default:
			n.Kids = make([]*row, r.Intn(4))
			for i := range n.Kids {
				if r.Intn(6) != 0 {
					n.Kids[i] = randRow(r, depth-1)
				}
			}
		}
	}
	return n
}

func mutateRow(r *row, rng *rand.Rand) {
	if r == nil {
		return
	}
	r.Name += "!"
	if len(r.Payload) > 0 {
		r.Payload[0] ^= 0x5a
	}
	if len(r.Tags) > 0 {
		r.Tags[0] += "!"
	}
	if r.Index != nil {
		r.Index["mut"] = 1
	}
	for _, k := range r.Kids {
		mutateRow(k, rng)
	}
	_ = rng
}

func main() {
	curSeed = 1
	if s := os.Getenv("UAF_SEED"); s != "" {
		if v, err := strconv.ParseInt(s, 10, 64); err == nil {
			curSeed = v
		}
	}
	rounds := envInt("TORTURE_ROUNDS", 1)
	if rounds > 100 {
		rounds = 100
	}
	r := rand.New(rand.NewSource(curSeed))

	// 1. 固定树的深拷贝、nil/空语义、双向隔离。
	src := &row{
		ID: 1, Name: "root", Payload: []byte{1, 2, 3}, Tags: []string{"a", "b"},
		Index: map[string]int64{"x": 1, "y": 2},
		Kids: []*row{
			{ID: 2, Name: "c1", Payload: []byte{}, Tags: []string{}, Index: map[string]int64{}, Kids: []*row{}},
			{ID: 3, Name: "c2", Payload: nil, Tags: nil, Index: nil, Kids: nil},
			nil,
		},
	}
	cp := src.Clone()
	ck(dump(cp) == dump(src), "fixed clone equal")
	ck(noShared(src, cp), "fixed clone no shared refs")
	ck(cp.Kids[0].Payload != nil && len(cp.Kids[0].Payload) == 0, "empty payload preserved non-nil")
	ck(cp.Kids[0].Tags != nil && len(cp.Kids[0].Tags) == 0, "empty tags preserved non-nil")
	ck(cp.Kids[0].Index != nil && len(cp.Kids[0].Index) == 0, "empty index preserved non-nil")
	ck(cp.Kids[0].Kids != nil && len(cp.Kids[0].Kids) == 0, "empty kids preserved non-nil")
	ck(cp.Kids[1].Payload == nil && cp.Kids[1].Tags == nil && cp.Kids[1].Index == nil && cp.Kids[1].Kids == nil,
		"nil containers preserved")
	ck(cp.Kids[2] == nil, "nil child preserved")
	ck(reflect.ValueOf(cp.Index).Pointer() != reflect.ValueOf(src.Index).Pointer(), "index map duplicated")

	snap := dump(cp)
	mutateRow(src, nil)
	ck(dump(cp) == snap, "clone isolated from source mutation")
	cpSnap := dump(src)
	cp.Payload[0] = 0xee
	cp.Tags[0] = "mut"
	cp.Index["z"] = 3
	cp.Kids[0].Name = "mut"
	ck(dump(src) == cpSnap, "source isolated from clone mutation")
	ck(src.Payload[0] != 0xee, "payload copied not aliased")

	// 2. 源释放、只保留副本与视图，多轮 GC 后仍完整。
	retSrc := &row{ID: 9, Name: "ret", Payload: make([]byte, 128), Tags: []string{"z1", "z2"},
		Index: map[string]int64{"a": 7}, Kids: []*row{{ID: 10, Payload: []byte{5, 6, 7}}}}
	for i := range retSrc.Payload {
		retSrc.Payload[i] = byte(i)
	}
	ret := retSrc.Clone()
	wantRet := dump(retSrc)
	payloadView := ret.Payload
	tagsView := ret.Tags
	kidView := ret.Kids[0]
	retSrc = nil
	gcSink = append(gcSink, payloadView, kidView.Payload)
	gcStress()
	ck(dump(ret) == wantRet, "retained clone intact after source release + GC")
	ck(len(payloadView) == 128 && payloadView[127] == 127, "retained payload view survives GC")
	ck(tagsView[1] == "z2" && kidView.ID == 10 && kidView.Payload[1] == 6, "retained child/tags views survive GC")
	gcStress()
	ck(dump(ret) == wantRet, "retained clone intact after second GC")

	// 3. CloneInto 复用目标：覆盖不追加、旧对象不再被引用、容量可复用。
	dst := &row{
		ID: 99, Name: "dirty", Payload: make([]byte, 200), Tags: make([]string, 1, 16),
		Index: map[string]int64{"old": 1, "x": 9}, Kids: make([]*row, 2, 8),
	}
	dst.Tags[0] = "old"
	dst.Kids[0] = &row{Name: "oldKid0"}
	dst.Kids[1] = &row{Name: "oldKid1"}
	oldTagSlot := &dst.Tags[0]
	oldKid0 := dst.Kids[0]
	src2 := &row{ID: 5, Name: "new", Payload: []byte{9, 9}, Tags: []string{"n1"}, Index: map[string]int64{"n": 2},
		Kids: []*row{{Name: "newKid"}}}
	src2.CloneInto(dst)
	ck(dump(dst) == dump(src2), "CloneInto overwrites dirty destination")
	ck(len(dst.Index) == 1 && dst.Index["old"] == 0, "destination index cleared not merged")
	ck(len(dst.Kids) == 1 && dst.Kids[0] != oldKid0 && oldKid0.Name == "oldKid0", "old destination child unreferenced")
	ck(&dst.Tags[0] == oldTagSlot, "destination tags capacity reused")
	ck(dump(dst) == dump(src2), "reused destination independent of old content")
	oldKid0.Name = "mut"
	ck(dump(dst) == dump(src2), "old destination mutation does not affect clone")

	// 4. sync.Pool 临时缓冲：克隆不别名池缓冲，GC 前后都安全。
	for i := 0; i < 500; i++ {
		in := make([]byte, r.Intn(64)+1)
		for j := range in {
			in[j] = byte(j + i)
		}
		out := cloneBytes(in)
		ckSeed(len(out) == len(in) && &out[0] != &in[0], fmt.Sprintf("scratch clone %d isolated", i))
		for j := range out {
			if out[j] != in[j] {
				ckSeed(false, fmt.Sprintf("scratch clone %d content mismatch", i))
				break
			}
		}
		for j := range in {
			in[j] ^= 0x7f
		}
		ckSeed(out[0] == byte(i), fmt.Sprintf("scratch clone %d unaffected by input mutation", i))
		ckSeed(out[0] != 0xee || byte(i) == 0xee, fmt.Sprintf("scratch clone %d not poisoned", i))
		if i%17 == 0 {
			gcStress()
		}
	}
	// empty 非 nil / nil 语义。
	ck(cloneBytes(nil) == nil, "cloneBytes(nil) nil")
	e := cloneBytes([]byte{})
	ck(e != nil && len(e) == 0, "cloneBytes(empty) non-nil empty")

	// 5. P2：TORTURE_ROUNDS 轮克隆 + GC，保留副本跨轮存活。
	var keep []*row
	var keepWant []string
	total := 4 * rounds
	for round := 0; round < total; round++ {
		batch := make([]*row, 60)
		for i := range batch {
			batch[i] = randRow(r, 3)
		}
		for i, b := range batch {
			c := b.Clone()
			if i%10 == 0 {
				keep = append(keep, c)
				keepWant = append(keepWant, dump(b))
			}
		}
		batch = nil
		gcStress()
		for i := range keep {
			ckSeed(dump(keep[i]) == keepWant[i], fmt.Sprintf("round %d retained clone %d", round, i))
		}
	}
	ck(len(keep) == total*6, "retained clone count")
	// 保留副本再克隆一次，跨 GC 比较。
	again := make([]*row, len(keep))
	for i := range keep {
		again[i] = keep[i].Clone()
	}
	gcStress()
	for i := range again {
		ckSeed(dump(again[i]) == keepWant[i], fmt.Sprintf("second clone retention %d", i))
	}

	// 6. UAF_SEED 随机树：Clone/CloneInto、无共享、GC、源修改隔离。
	rr := r
	for i := 0; i < 160; i++ {
		s := randRow(rr, 3)
		c := s.Clone()
		ckSeed(dump(c) == dump(s), fmt.Sprintf("round %d random clone equal", i))
		ckSeed(noShared(s, c), fmt.Sprintf("round %d random no shared", i))
		snap := dump(c)
		mutateRow(s, rr)
		ckSeed(dump(c) == snap, fmt.Sprintf("round %d random source mutation isolation", i))
		dirty := randRow(rr, 2)
		s.CloneInto(dirty)
		ckSeed(dump(dirty) == dump(s), fmt.Sprintf("round %d random CloneInto", i))
		ckSeed(noShared(s, dirty), fmt.Sprintf("round %d random CloneInto no shared", i))
		if i%23 == 0 {
			gcSink = append(gcSink, []byte(s.Name), []byte(c.Name))
			gcStress()
			ckSeed(dump(c) == snap, fmt.Sprintf("round %d random clone after GC", i))
			ckSeed(dump(dirty) == dump(s), fmt.Sprintf("round %d dirty clone after GC", i))
		}
	}

	finish("l10_clone_pool_gc")
}
