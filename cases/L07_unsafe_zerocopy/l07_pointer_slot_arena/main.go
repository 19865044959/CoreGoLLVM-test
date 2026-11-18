// L07: 指针槽位数组：堆上 [N]unsafe.Pointer 槽位、释放/复用、代际计数与 GC 扫描。
package main

import (
	"fmt"
	"os"
	"runtime"
	"strconv"
	"sync/atomic"
	"unsafe"
)

type anode struct {
	id   int64
	tag  string
	data []byte
	link *anode
}

type arena struct {
	slots [24]unsafe.Pointer
	gens  [24]uint64
	n     int
	free  []int
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

func newArena(n int) *arena {
	a := &arena{n: n}
	for i := n - 1; i >= 0; i-- {
		a.free = append(a.free, i)
	}
	return a
}

func (a *arena) alloc() int {
	if len(a.free) == 0 {
		return -1
	}
	i := a.free[len(a.free)-1]
	a.free = a.free[:len(a.free)-1]
	return i
}

func (a *arena) put(i int, o *anode) {
	a.gens[i]++
	a.slots[i] = unsafe.Pointer(o)
}

func (a *arena) get(i int) *anode {
	return (*anode)(a.slots[i])
}

func (a *arena) release(i int) {
	a.gens[i]++
	a.slots[i] = nil
	a.free = append(a.free, i)
}

func newAnode(id int64, tag string, link *anode) *anode {
	return &anode{
		id:   id,
		tag:  tag,
		data: []byte("d-" + strconv.FormatInt(id, 10)),
		link: link,
	}
}

func checkAnode(o *anode, id int64, tag string, linkID int64, linkTag, stage string) {
	if o == nil {
		ck(false, stage+": nil anode")
		return
	}
	ck(o.id == id && o.tag == tag, stage+": id/tag")
	ck(string(o.data) == "d-"+strconv.FormatInt(id, 10), stage+": data")
	if o.link == nil {
		ck(false, stage+": nil link")
		return
	}
	ck(o.link.id == linkID && o.link.tag == linkTag, stage+": link")
}

func ringTag(i, n int) string { return "r-" + strconv.Itoa(i%n) }

func main() {
	if s := os.Getenv("UAF_SEED"); s != "" {
		if v, err := strconv.ParseInt(s, 10, 64); err == nil {
			seed = v
		}
	}
	if seed < 0 {
		seed = -seed
	}

	n := 12 + int(seed%9) // 12..20
	a := newArena(n)
	ck(len(a.free) == n, "arena initial free count")
	ck(a.alloc() == 0, "first alloc index")
	a.free = append(a.free, 0) // 还原

	// 槽位地址与 GEP 一致。
	for _, k := range []int{0, n / 2, n - 1} {
		p := unsafe.Add(unsafe.Pointer(&a.slots[0]), k*int(unsafe.Sizeof(a.slots[0])))
		ck(p == unsafe.Pointer(&a.slots[k]), "slot GEP identity k="+strconv.Itoa(k))
	}

	// 1) 填满槽位：对象只通过 unsafe.Pointer 槽位可达，形成环形 link。
	for i := 0; i < n; i++ {
		idx := a.alloc()
		ck(idx == i, "fill alloc order i="+strconv.Itoa(i))
		linkIdx := (i + 1) % n
		a.put(idx, newAnode(int64(idx), ringTag(idx, n), newAnode(int64(linkIdx), ringTag(linkIdx, n), nil)))
	}
	ck(a.alloc() == -1, "arena full capacity")
	ck(len(a.free) == 0, "arena free empty")
	gcStress()
	for i := 0; i < n; i++ {
		o := a.get(i)
		checkAnode(o, int64(i), ringTag(i, n), int64((i+1)%n), ringTag((i+1)%n, n), "filled slot "+strconv.Itoa(i))
	}

	// 2) 释放部分槽位，保存旧对象类型化引用；槽位清空后 GC。
	step := 2 + int(seed%3)
	genBefore := a.gens
	var released []int
	var keepOld []*anode
	for i := 0; i < n; i++ {
		if i%step != 0 {
			continue
		}
		old := a.get(i)
		keepOld = append(keepOld, old)
		released = append(released, i)
		a.release(i)
	}
	ck(len(released) > 0, "released non-empty")
	for _, i := range released {
		ck(a.get(i) == nil, "released slot nil i="+strconv.Itoa(i))
		ck(a.gens[i] == genBefore[i]+1, "release generation i="+strconv.Itoa(i))
	}
	gcStress()
	for _, i := range released {
		ck(a.get(i) == nil, "released slot nil after GC i="+strconv.Itoa(i))
	}
	for k, old := range keepOld {
		idx := released[k]
		checkAnode(old, int64(idx), ringTag(idx, n), int64((idx+1)%n), ringTag((idx+1)%n, n),
			"released object kept typed "+strconv.Itoa(k))
	}
	for i := 0; i < n; i++ {
		if i%step == 0 {
			continue
		}
		o := a.get(i)
		checkAnode(o, int64(i), ringTag(i, n), int64((i+1)%n), ringTag((i+1)%n, n), "survivor "+strconv.Itoa(i))
	}

	// 3) 复用释放的槽位：LIFO 分配，代际递增；旧对象仍由类型化引用保活。
	sentinel := newAnode(-1, "sentinel", nil)
	var reused [24]int
	for i := range reused {
		reused[i] = -1
	}
	reuseCount := len(released)
	for k := 0; k < reuseCount; k++ {
		idx := a.alloc()
		ck(idx >= 0, "reuse alloc k="+strconv.Itoa(k))
		reused[idx] = k
		if k == 0 {
			// 经 unsafe.Add 得到槽位指针后写入，仍应是带写屏障的 unsafe.Pointer 存储。
			sp := (*unsafe.Pointer)(unsafe.Add(unsafe.Pointer(&a.slots[0]), idx*int(unsafe.Sizeof(a.slots[0]))))
			*sp = unsafe.Pointer(newAnode(int64(1000+k), "reuse-"+strconv.Itoa(k), sentinel))
			a.gens[idx]++
		} else {
			a.put(idx, newAnode(int64(1000+k), "reuse-"+strconv.Itoa(k), sentinel))
		}
		ck(a.gens[idx] == genBefore[idx]+2, "reuse generation idx="+strconv.Itoa(idx))
	}
	ck(a.alloc() == -1, "reuse exhausts free list")
	gcStress()
	for i := 0; i < n; i++ {
		o := a.get(i)
		if reused[i] >= 0 {
			checkAnode(o, int64(1000+reused[i]), "reuse-"+strconv.Itoa(reused[i]), -1, "sentinel", "reused slot "+strconv.Itoa(i))
		} else if i%step == 0 {
			ck(false, "unexpected reuse of slot "+strconv.Itoa(i))
		} else {
			checkAnode(o, int64(i), ringTag(i, n), int64((i+1)%n), ringTag((i+1)%n, n), "survivor after reuse "+strconv.Itoa(i))
		}
	}
	for k, old := range keepOld {
		idx := released[k]
		checkAnode(old, int64(idx), ringTag(idx, n), int64((idx+1)%n), ringTag((idx+1)%n, n),
			"released object after reuse "+strconv.Itoa(k))
	}
	ck(sentinel.tag == "sentinel" && sentinel.id == -1, "sentinel after GC")

	// 4) 再次清空全部槽位，GC 后类型化引用仍有效，free list 重建。
	for i := 0; i < n; i++ {
		if a.get(i) != nil {
			a.release(i)
		}
	}
	ck(len(a.free) == n, "free list rebuilt")
	for i := 0; i < n; i++ {
		ck(a.get(i) == nil, "all slots nil i="+strconv.Itoa(i))
	}
	gcStress()
	ck(a.alloc() == n-1, "LIFO free order after drain")
	a.put(n-1, newAnode(7, "last", sentinel))
	gcStress()
	checkAnode(a.get(n-1), 7, "last", -1, "sentinel", "single live slot")

	// 5) 槽位数组清零后，仅保留的类型化引用仍可读。
	for i := 0; i < n; i++ {
		if a.get(i) != nil {
			a.release(i)
		}
	}
	ck(len(a.free) == n, "free list after final drain")
	gcStress()
	for k, old := range keepOld {
		idx := released[k]
		checkAnode(old, int64(idx), ringTag(idx, n), int64((idx+1)%n), ringTag((idx+1)%n, n),
			"typed refs after arena drain "+strconv.Itoa(k))
	}
	ck(sentinel.id == -1 && sentinel.tag == "sentinel", "sentinel after arena drain")

	runtime.KeepAlive(a)
	runtime.KeepAlive(keepOld)
	runtime.KeepAlive(sentinel)
	finish("l07_pointer_slot_arena")
}
