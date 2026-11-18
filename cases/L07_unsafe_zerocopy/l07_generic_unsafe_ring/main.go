// L07: 泛型 unsafe 容器。gring[T] 用 unsafe.Pointer + Sizeof 步长在堆数组上做槽位寻址，
// 分别实例化为标量、含指针 struct、指针元素与自定义字节类型；覆盖回绕与 GC 扫描。
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
			b := make([]byte, 7168+i*37)
			b[0] = byte(i)
			b[len(b)-1] = byte(215 - i)
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

type gLeaf struct {
	v   int64
	tag string
}

type gCell struct {
	id   int64
	name string
	link *gLeaf
	data []byte
}

type myByte byte

type gring[T any] struct {
	data   unsafe.Pointer
	stride uintptr
	capa   int
	length int
	head   int
}

func newGRing[T any](capa int) *gring[T] {
	if capa < 1 {
		capa = 1
	}
	backing := make([]T, capa)
	var zero T
	r := &gring[T]{
		data:   unsafe.Pointer(&backing[0]),
		stride: unsafe.Sizeof(zero),
		capa:   capa,
	}
	runtime.KeepAlive(backing)
	return r
}

func (r *gring[T]) at(i int) *T {
	return (*T)(unsafe.Add(r.data, uintptr(i)*r.stride))
}

func (r *gring[T]) push(v T) bool {
	if r.length == r.capa {
		return false
	}
	idx := (r.head + r.length) % r.capa
	*r.at(idx) = v
	r.length++
	return true
}

func (r *gring[T]) pop() (T, bool) {
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

func (r *gring[T]) get(i int) *T {
	if i < 0 || i >= r.length {
		return nil
	}
	return r.at((r.head + i) % r.capa)
}

func (r *gring[T]) len() int { return r.length }

func newGCell(seq int64, s int64) gCell {
	n := 6 + int(seq%5)
	data := make([]byte, n)
	for k := range data {
		data[k] = byte((seq*47 + int64(k*11) + s) & 0xff)
	}
	return gCell{
		id:   seq,
		name: "c-" + strconv.FormatInt(seq, 10),
		link: &gLeaf{v: seq*31 - s, tag: "gl-" + strconv.FormatInt(seq, 10)},
		data: data,
	}
}

func checkGCell(p *gCell, seq int64, s int64, stage string) {
	ck(p != nil, stage+": nil cell seq="+strconv.FormatInt(seq, 10))
	if p == nil {
		return
	}
	ck(p.id == seq, stage+": id got="+strconv.FormatInt(p.id, 10))
	ck(p.name == "c-"+strconv.FormatInt(seq, 10), stage+": name seq="+strconv.FormatInt(seq, 10))
	if p.link == nil {
		ck(false, stage+": nil link seq="+strconv.FormatInt(seq, 10))
	} else {
		ck(p.link.v == seq*31-s && p.link.tag == "gl-"+strconv.FormatInt(seq, 10), stage+": link seq="+strconv.FormatInt(seq, 10))
	}
	ck(len(p.data) == 6+int(seq%5), stage+": data len seq="+strconv.FormatInt(seq, 10))
	for k := range p.data {
		ck(p.data[k] == byte((seq*47+int64(k*11)+s)&0xff), stage+": data seq="+strconv.FormatInt(seq, 10)+" k="+strconv.Itoa(k))
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

	// 1) 标量环：满/拒入、回绕、逻辑序。
	r64 := newGRing[int64](8)
	ck(r64.stride == unsafe.Sizeof(int64(0)), "int64 stride")
	for i := int64(0); i < 8; i++ {
		ck(r64.push(i), "int64 push i="+strconv.FormatInt(i, 10))
	}
	ck(!r64.push(99), "full ring rejects push")
	for i := int64(0); i < 3; i++ {
		v, ok := r64.pop()
		ck(ok && v == i, "int64 pop i="+strconv.FormatInt(i, 10))
	}
	for _, v := range []int64{100, 101, 102} {
		ck(r64.push(v), "int64 wrap push v="+strconv.FormatInt(v, 10))
	}
	want64 := []int64{3, 4, 5, 6, 7, 100, 101, 102}
	ck(r64.len() == len(want64), "int64 len after wrap")
	for i, w := range want64 {
		p := r64.get(i)
		ck(p != nil && *p == w, "int64 logical i="+strconv.Itoa(i))
	}
	for i, w := range want64 {
		v, ok := r64.pop()
		ck(ok && v == w, "int64 drain i="+strconv.Itoa(i))
	}
	_, ok := r64.pop()
	ck(!ok, "empty ring pop")
	ck(uintptr(unsafe.Pointer(r64.at(1)))-uintptr(unsafe.Pointer(r64.at(0))) == r64.stride, "int64 slot stride")
	ck(uintptr(unsafe.Pointer(r64.at(0)))%unsafe.Alignof(int64(0)) == 0, "int64 slot alignment")

	// 2) 含指针 struct 环：逻辑序、弹出清零、GC 扫描。
	capa := 6 + int(seed%2)
	rc := newGRing[gCell](capa)
	ck(rc.stride == unsafe.Sizeof(gCell{}), "cell stride")
	for i := int64(0); i < int64(capa); i++ {
		ck(rc.push(newGCell(i, seed)), "cell push i="+strconv.FormatInt(i, 10))
	}
	for i := 0; i < capa; i++ {
		checkGCell(rc.get(i), int64(i), seed, "cell before GC")
	}
	var popped []gCell
	for i := 0; i < 2; i++ {
		v, ok := rc.pop()
		ck(ok && v.id == int64(i), "cell pop i="+strconv.Itoa(i))
		popped = append(popped, v)
	}
	for i := int64(0); i < 2; i++ {
		ck(rc.push(newGCell(100+i, seed)), "cell wrap push i="+strconv.FormatInt(i, 10))
	}
	wantCells := make([]int64, rc.len())
	for i := range wantCells {
		if i < capa-2 {
			wantCells[i] = int64(i + 2)
		} else {
			wantCells[i] = 100 + int64(i-(capa-2))
		}
		checkGCell(rc.get(i), wantCells[i], seed, "cell after wrap")
	}
	gcStress()
	for i, id := range wantCells {
		checkGCell(rc.get(i), id, seed, "cell after GC")
	}
	for i := range popped {
		checkGCell(&popped[i], int64(i), seed, "popped cell after GC")
	}

	// 3) 再弹出一个元素清空其槽位，记录物理槽位状态后丢弃环。
	extra, ok := rc.pop()
	ck(ok && extra.id == 2, "cell extra pop clears slot")
	popped = append(popped, extra)
	head, length := rc.head, rc.len()
	ids := make([]int64, capa)
	for i := 0; i < capa; i++ {
		ids[i] = rc.at(i).id
	}
	keep := rc.at(0)
	rc = nil
	popped = nil
	gcStress()
	for i := 0; i < capa; i++ {
		p := (*gCell)(unsafe.Add(unsafe.Pointer(keep), uintptr(i)*unsafe.Sizeof(gCell{})))
		logical := (i - head + capa) % capa
		if logical < length {
			checkGCell(p, ids[i], seed, "physical live slot i="+strconv.Itoa(i))
		} else {
			ck(p.id == 0 && p.name == "" && p.link == nil && p.data == nil, "physical dead slot cleared i="+strconv.Itoa(i))
		}
	}

	// 4) 指针元素环：元素指针只经环槽位保活。
	rp := newGRing[*gLeaf](4)
	ck(rp.stride == unsafe.Sizeof((*gLeaf)(nil)), "pointer stride")
	for i := int64(0); i < 4; i++ {
		ck(rp.push(&gLeaf{v: i*13 + seed, tag: "p-" + strconv.FormatInt(i, 10)}), "ptr push i="+strconv.FormatInt(i, 10))
	}
	for i := int64(0); i < 2; i++ {
		v, ok := rp.pop()
		ck(ok && v != nil && v.v == i*13+seed && v.tag == "p-"+strconv.FormatInt(i, 10), "ptr pop i="+strconv.FormatInt(i, 10))
	}
	ck(rp.push(&gLeaf{v: 200 + seed, tag: "p-200"}), "ptr wrap push")
	ck(rp.push(&gLeaf{v: 201 + seed, tag: "p-201"}), "ptr wrap push 2")
	gcStress()
	for i := 0; i < rp.len(); i++ {
		p := rp.get(i)
		ck(p != nil && *p != nil && (*p).tag != "", "ptr elem after GC i="+strconv.Itoa(i))
	}
	for rp.len() > 0 {
		v, _ := rp.pop()
		ck(v != nil && v.tag != "", "ptr drain")
	}

	// 5) 自定义字节类型实例化。
	rb := newGRing[myByte](5)
	ck(rb.stride == 1, "myByte stride")
	for i := 0; i < 5; i++ {
		ck(rb.push(myByte(i*7+1)), "byte push")
	}
	for i := 0; i < 5; i++ {
		v, ok := rb.pop()
		ck(ok && v == myByte(i*7+1), "byte pop i="+strconv.Itoa(i))
	}

	finish("l07_generic_unsafe_ring")
}
