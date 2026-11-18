// L07: 泛型 unsafe 环的多圈回绕。grRing[T] 以 unsafe.Pointer + Sizeof 步长寻址堆数组，
// 每圈反复覆写槽位；每次审计物理槽位：逻辑存活槽必须内容自洽，已腾空槽必须零化。
package main

import (
	"fmt"
	"os"
	"runtime"
	"strconv"
	"sync/atomic"
	"unsafe"
)

var (
	failed int32
	gcSink [][]byte
	seed   int64
)

func ck(ok bool, msg string) {
	if !ok {
		atomic.AddInt32(&failed, 1)
		fmt.Printf("FAIL: %s (seed=%d)\n", msg, seed)
	}
}

func gcStress() {
	for r := 0; r < 2; r++ {
		for i := 0; i < 12; i++ {
			b := make([]byte, 7168+i*33)
			b[0] = byte(i)
			b[len(b)-1] = byte(230 - i)
			gcSink = append(gcSink[:0], b)
		}
		gcSink = gcSink[:0]
		runtime.GC()
	}
}

func finish(name string) {
	if n := atomic.LoadInt32(&failed); n != 0 {
		fmt.Printf("FAILED %s (%d failed checks)\n", name, n)
		os.Exit(1)
	}
	fmt.Println("ALL PASS " + name)
}

func tortureRounds(def int) int {
	if s := os.Getenv("TORTURE_ROUNDS"); s != "" {
		if v, err := strconv.Atoi(s); err == nil && v > 0 {
			if v > 12 {
				v = 12
			}
			return v
		}
	}
	return def
}

type grLeaf struct {
	v   int64
	tag string
}

type grCell struct {
	id   int64
	name string
	link *grLeaf
	data []byte
}

type grPair struct {
	a *grLeaf
	b *grLeaf
}

type grRing[T any] struct {
	data   unsafe.Pointer
	stride uintptr
	capa   int
	length int
	head   int
}

func newGRRing[T any](c int) *grRing[T] {
	if c < 1 {
		c = 1
	}
	backing := make([]T, c)
	var zero T
	r := &grRing[T]{
		data:   unsafe.Pointer(&backing[0]),
		stride: unsafe.Sizeof(zero),
		capa:   c,
	}
	runtime.KeepAlive(backing)
	return r
}

func (r *grRing[T]) at(i int) *T {
	return (*T)(unsafe.Add(r.data, uintptr(i)*r.stride))
}

func (r *grRing[T]) push(v T) bool {
	if r.length == r.capa {
		return false
	}
	*r.at((r.head + r.length) % r.capa) = v
	r.length++
	return true
}

func (r *grRing[T]) pop() (T, bool) {
	var zero T
	if r.length == 0 {
		return zero, false
	}
	v := *r.at(r.head)
	*r.at(r.head) = zero
	r.head = (r.head + 1) % r.capa
	r.length--
	return v, true
}

func newGRCell(id int64, s int64) grCell {
	n := 6 + int(id%5)
	data := make([]byte, n)
	for k := range data {
		data[k] = byte((id*47 + int64(k*11) + s) & 0xff)
	}
	return grCell{
		id:   id,
		name: "c-" + strconv.FormatInt(id, 10),
		link: &grLeaf{v: id*29 + s, tag: "gl-" + strconv.FormatInt(id, 10)},
		data: data,
	}
}

func checkGRCell(p *grCell, id int64, s int64, stage string) {
	ck(p.id == id, stage+": id got="+strconv.FormatInt(p.id, 10))
	ck(p.name == "c-"+strconv.FormatInt(id, 10), stage+": name id="+strconv.FormatInt(id, 10))
	if p.link == nil {
		ck(false, stage+": nil link id="+strconv.FormatInt(id, 10))
	} else {
		ck(p.link.v == id*29+s && p.link.tag == "gl-"+strconv.FormatInt(id, 10), stage+": link id="+strconv.FormatInt(id, 10))
	}
	ck(len(p.data) == 6+int(id%5), stage+": data len id="+strconv.FormatInt(id, 10))
	for k := range p.data {
		ck(p.data[k] == byte((id*47+int64(k*11)+s)&0xff), stage+": data id="+strconv.FormatInt(id, 10)+" k="+strconv.Itoa(k))
	}
}

func slotZero(p *grCell) bool {
	return p.id == 0 && p.name == "" && p.link == nil && p.data == nil
}

// auditGR 按物理槽位审计：逻辑位置 < length 的槽必须对应 want[pos]，其余必须零化。
func auditGR(r *grRing[grCell], want []int64, s int64, stage string) {
	ck(r.length == len(want), stage+": logical len")
	for i := 0; i < r.capa; i++ {
		pos := (i - r.head + r.capa) % r.capa
		p := r.at(i)
		if pos < r.length {
			ck(p.id == want[pos], stage+": live id i="+strconv.Itoa(i))
			checkGRCell(p, want[pos], s, stage+" slot")
		} else {
			ck(slotZero(p), stage+": dead slot not zero i="+strconv.Itoa(i))
		}
	}
}

func main() {
	if env := os.Getenv("UAF_SEED"); env != "" {
		if v, err := strconv.ParseInt(env, 10, 64); err == nil {
			seed = v
		}
	}
	if seed < 0 {
		seed = -seed
	}

	capa := 7 + int(seed%4)
	rounds := tortureRounds(2)
	r := newGRRing[grCell](capa)
	ck(r.stride == unsafe.Sizeof(grCell{}), "cell stride")
	ck(r.capa == capa, "ring capacity")
	model := make([]int64, 0, capa)
	seq := int64(0)

	popCell := func(stage string) grCell {
		var zero grCell
		ck(len(model) > 0, stage+": model empty")
		v, ok := r.pop()
		ck(ok, stage+": pop")
		if !ok {
			return zero
		}
		old := (r.head - 1 + r.capa) % r.capa
		ck(slotZero(r.at(old)), stage+": vacated slot not cleared")
		ck(v.id == model[0], stage+": FIFO got="+strconv.FormatInt(v.id, 10))
		checkGRCell(&v, model[0], seed, stage)
		model = model[1:]
		return v
	}

	// 1) 填满、拒绝、GC、审计物理槽位。
	for i := 0; i < capa; i++ {
		ck(r.push(newGRCell(seq, seed)), "fill push")
		model = append(model, seq)
		seq++
	}
	ck(!r.push(newGRCell(-1, seed)), "full ring rejects push")
	gcStress()
	auditGR(r, model, seed, "after fill")

	// 2) 多圈回绕：每圈反复覆写槽位，按圈审计已腾空槽是否零化。
	ops := capa * 6
	for round := 0; round < rounds; round++ {
		for i := 0; i < ops; i++ {
			if len(model) == capa {
				popCell("full before push r=" + strconv.Itoa(round))
			}
			ck(r.push(newGRCell(seq, seed)), "wrap push")
			model = append(model, seq)
			seq++
			if i%3 == 0 {
				popCell("interleave r=" + strconv.Itoa(round))
			}
			if i%capa == capa-1 {
				gcStress()
				auditGR(r, model, seed, "mid round r="+strconv.Itoa(round)+" i="+strconv.Itoa(i))
			}
		}
	}

	// 3) 排空：每个弹出槽立即零化；排空后全槽为 nil，GC 后仍为零。
	var kept []grCell
	for len(model) > 0 {
		kept = append(kept, popCell("drain"))
	}
	_, ok := r.pop()
	ck(!ok, "empty ring pop")
	gcStress()
	auditGR(r, nil, seed, "after drain")
	for i := 0; i < capa; i++ {
		ck(slotZero(r.at(i)), "all slots zero after drain i="+strconv.Itoa(i))
	}

	// 4) 指针元素环：弹出即清槽，GC 后死槽为 nil，存活元素仍有效。
	rp := newGRRing[*grLeaf](5)
	ck(rp.stride == unsafe.Sizeof((*grLeaf)(nil)), "pointer stride")
	for i := 0; i < 5; i++ {
		ck(rp.push(&grLeaf{v: int64(i)*13 + seed, tag: "p-" + strconv.Itoa(i)}), "ptr push")
	}
	gcStress()
	for i := 0; i < 5; i++ {
		p, ok := rp.pop()
		ck(ok && p != nil, "ptr pop i="+strconv.Itoa(i))
		if p != nil {
			ck(p.v == int64(i)*13+seed && p.tag == "p-"+strconv.Itoa(i), "ptr value i="+strconv.Itoa(i))
		}
		old := (rp.head - 1 + 5) % 5
		ck(*rp.at(old) == nil, "ptr slot cleared i="+strconv.Itoa(i))
	}
	gcStress()
	for i := 0; i < 5; i++ {
		ck(*rp.at(i) == nil, "ptr dead slot nil after GC i="+strconv.Itoa(i))
	}

	// 5) 双指针元素环：零值覆盖两个指针字段。
	rq := newGRRing[grPair](3)
	for i := 0; i < 3; i++ {
		ck(rq.push(grPair{
			a: &grLeaf{v: int64(i)*17 + seed, tag: "pa-" + strconv.Itoa(i)},
			b: &grLeaf{v: int64(i)*19 - seed, tag: "pb-" + strconv.Itoa(i)},
		}), "pair push i="+strconv.Itoa(i))
	}
	gcStress()
	for i := 0; i < 3; i++ {
		v, ok := rq.pop()
		ck(ok && v.a != nil && v.b != nil, "pair pop i="+strconv.Itoa(i))
		if v.a != nil && v.b != nil {
			ck(v.a.tag == "pa-"+strconv.Itoa(i) && v.b.tag == "pb-"+strconv.Itoa(i), "pair value i="+strconv.Itoa(i))
		}
		old := (rq.head - 1 + 3) % 3
		p := rq.at(old)
		ck(p.a == nil && p.b == nil, "pair slot cleared i="+strconv.Itoa(i))
	}

	// 6) 丢弃环，仅保留已弹出的 cell，跨 GC 校验。
	r = nil
	model = nil
	gcStress()
	for i := range kept {
		checkGRCell(&kept[i], kept[i].id, seed, "kept after ring drop")
	}

	finish("l07_generic_ring_multiwrap")
}
