// L07: 指针槽位与稳定句柄：堆上 [N]unsafe.Pointer 槽位表，删除后压缩指针、
// 重映射句柄，槽位子切片与 GC 后仍可解析；旧对象由类型化引用保活。
package main

import (
	"fmt"
	"os"
	"runtime"
	"strconv"
	"sync/atomic"
	"unsafe"
)

type pobj struct {
	id   int64
	tag  string
	data []byte
	ref  *pleaf
}

type pleaf struct {
	v   int64
	tag string
}

type slotTable struct {
	slots [32]unsafe.Pointer
	gens  [32]uint64
}

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
		for i := 0; i < 10; i++ {
			b := make([]byte, 8192)
			b[0] = byte(i)
			b[len(b)-1] = byte(255 - i)
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

func newObj(id int64, s int64) *pobj {
	return &pobj{
		id:   id,
		tag:  "o-" + strconv.FormatInt(id, 10),
		data: []byte("d-" + strconv.FormatInt(id*3+s, 10)),
		ref:  &pleaf{v: id*11 + s, tag: "r-" + strconv.FormatInt(id, 10)},
	}
}

func checkObj(p *pobj, id int64, s int64, stage string) {
	if p == nil {
		ck(false, stage+": nil obj id="+strconv.FormatInt(id, 10))
		return
	}
	ck(p.id == id, stage+": id="+strconv.FormatInt(id, 10))
	ck(p.tag == "o-"+strconv.FormatInt(id, 10), stage+": tag id="+strconv.FormatInt(id, 10))
	ck(string(p.data) == "d-"+strconv.FormatInt(id*3+s, 10), stage+": data id="+strconv.FormatInt(id, 10))
	if p.ref == nil {
		ck(false, stage+": nil ref id="+strconv.FormatInt(id, 10))
		return
	}
	ck(p.ref.v == id*11+s && p.ref.tag == "r-"+strconv.FormatInt(id, 10), stage+": ref id="+strconv.FormatInt(id, 10))
}

func slotPtr(t *slotTable, i int) *unsafe.Pointer {
	return (*unsafe.Pointer)(unsafe.Add(unsafe.Pointer(&t.slots[0]), i*int(unsafe.Sizeof(t.slots[0]))))
}

func resolve(slots []unsafe.Pointer, handles map[int64]int, id int64) *pobj {
	idx, ok := handles[id]
	if !ok || idx < 0 {
		return nil
	}
	return (*pobj)(slots[idx])
}

func main() {
	if s := os.Getenv("UAF_SEED"); s != "" {
		if v, err := strconv.ParseInt(s, 10, 64); err == nil {
			seed = v
		}
	}
	if seed < 0 {
		seed = -seed
	}

	n := 16 + int(seed%9)
	step := 3 + int(seed%2)
	t := new(slotTable)
	handles := make(map[int64]int, 48)

	// 1) 槽位地址算术与填充；对象只经槽位表可达。
	for k := 0; k < n; k++ {
		sp := slotPtr(t, k)
		ck(sp == &t.slots[k], "slot address k="+strconv.Itoa(k))
		*sp = unsafe.Pointer(newObj(int64(k), seed))
		handles[int64(k)] = k
	}
	sv := unsafe.Slice(&t.slots[0], n)
	ck(len(sv) == n, "slot slice length")
	for k := 0; k < n; k++ {
		ck(unsafe.Pointer(sv[k]) == unsafe.Pointer(t.slots[k]), "slot slice aliases table k="+strconv.Itoa(k))
	}
	gcStress()
	for k := 0; k < n; k++ {
		checkObj(resolve(sv, handles, int64(k)), int64(k), seed, "filled slot "+strconv.Itoa(k))
	}

	// 2) 删除部分槽位：槽位置 nil、句柄失效、旧对象由类型化引用保活。
	var keepOld []*pobj
	var released []int
	for i := 0; i < n; i++ {
		if i%step != 0 {
			continue
		}
		o := (*pobj)(t.slots[i])
		keepOld = append(keepOld, o)
		released = append(released, i)
		*slotPtr(t, i) = nil
		t.gens[i]++
		delete(handles, o.id)
	}
	ck(len(released) > 0, "released slots non-empty")
	gcStress()
	for _, i := range released {
		ck(t.slots[i] == nil, "released slot nil i="+strconv.Itoa(i))
	}
	for _, o := range keepOld {
		checkObj(o, o.id, seed, "released object typed ref")
	}
	for i := 0; i < n; i++ {
		if i%step == 0 {
			continue
		}
		checkObj(resolve(sv, handles, int64(i)), int64(i), seed, "survivor before compact")
	}

	// 3) 压缩存活槽位到前部：unsafe.Pointer 写入 + 句柄重映射。
	dst := 0
	for src := 0; src < n; src++ {
		o := (*pobj)(t.slots[src])
		if o == nil {
			continue
		}
		if dst != src {
			*slotPtr(t, dst) = unsafe.Pointer(o)
			t.gens[dst]++
			*slotPtr(t, src) = nil
			handles[o.id] = dst
		}
		dst++
	}
	live := dst
	ck(live == n-len(released), "live count after compact")
	for i := live; i < n; i++ {
		ck(t.slots[i] == nil, "tail slot nil i="+strconv.Itoa(i))
	}
	for id := int64(0); id < int64(n); id++ {
		if int64(id)%int64(step) == 0 {
			_, ok := handles[id]
			ck(!ok, "released handle invalid id="+strconv.FormatInt(id, 10))
		} else {
			checkObj(resolve(sv, handles, id), id, seed, "handle after compact")
		}
	}
	ck(len(sv) == n && sv[0] == t.slots[0] && sv[live-1] == t.slots[live-1], "slot slice sees compacted pointers")

	// 4) 复用尾槽位写入新对象；旧对象 typed ref 仍有效。
	free := n - live
	for k := 0; k < free && k < 5; k++ {
		id := int64(1000 + k)
		idx := live + k
		*slotPtr(t, idx) = unsafe.Pointer(newObj(id, seed))
		handles[id] = idx
		t.gens[idx]++
	}
	gcStress()
	for k := 0; k < free && k < 5; k++ {
		checkObj(resolve(sv, handles, int64(1000+k)), int64(1000+k), seed, "reused tail slot")
	}
	for _, o := range keepOld {
		checkObj(o, o.id, seed, "old object after reuse")
	}

	// 5) 只保留槽位子视图与句柄，丢弃 table 变量；槽位表整体由子视图保活。
	keepSlots := sv
	keepHandles := handles
	sub := sv[2:8]
	ck(&sub[0] == &sv[2], "sub-slice data offset")
	t = nil
	gcStress()
	for k := 0; k < n; k++ {
		o := (*pobj)(keepSlots[k])
		if o == nil {
			continue
		}
		checkObj(o, o.id, seed, "resolve after table drop k="+strconv.Itoa(k))
	}
	for _, o := range keepOld {
		checkObj(o, o.id, seed, "old object after table drop")
	}
	for k := 0; k < len(sub); k++ {
		ck(unsafe.Pointer(sub[k]) == unsafe.Pointer(keepSlots[2+k]), "sub-slice element k="+strconv.Itoa(k))
	}

	// 6) 清空槽位后 GC：typed refs 仍可读，句柄解析为 nil。
	for k := 0; k < n; k++ {
		keepSlots[k] = nil
	}
	for k := range keepHandles {
		delete(keepHandles, k)
	}
	gcStress()
	for _, o := range keepOld {
		checkObj(o, o.id, seed, "typed refs after drain")
	}
	ck(len(keepOld) == len(released), "typed refs count stable")

	runtime.KeepAlive(keepSlots)
	runtime.KeepAlive(sub)
	runtime.KeepAlive(keepHandles)
	finish("l07_pointer_slot_handles")
}
