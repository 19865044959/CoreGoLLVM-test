// L07: struct 数组指针算术 + 指针置换。用 unsafe.Add 按 Sizeof 步长把元素地址收进
// []*rec，排序/反转/旋转后回读；丢弃数组基址，仅靠元素指针跨 GC 保活。
package main

import (
	"fmt"
	"os"
	"runtime"
	"sort"
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
			b := make([]byte, 5120+i*13)
			b[0] = byte(i)
			b[len(b)-1] = byte(240 - i)
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

type pLeaf struct {
	v   int64
	tag string
}

type pRec struct {
	id    int32
	key   uint64
	name  string
	child *pLeaf
	data  []byte
	pad   uint16
}

func newRec(i int, s int64) pRec {
	n := 7 + i%6
	data := make([]byte, n)
	for k := range data {
		data[k] = byte((i*31 + k*7 + int(s)) & 0xff)
	}
	return pRec{
		id:    int32(i),
		key:   uint64(i)*1000003 + uint64(s%97)*7919,
		name:  "rec-" + strconv.Itoa(i),
		child: &pLeaf{v: int64(i)*17 + s, tag: "leaf-" + strconv.Itoa(i)},
		data:  data,
		pad:   uint16(i*13 + int(s%251)),
	}
}

func checkRec(p *pRec, i int, s int64, stage string) {
	ck(p != nil, stage+": nil rec i="+strconv.Itoa(i))
	if p == nil {
		return
	}
	ck(p.id == int32(i), stage+": id i="+strconv.Itoa(i))
	ck(p.key == uint64(i)*1000003+uint64(s%97)*7919, stage+": key i="+strconv.Itoa(i))
	ck(p.name == "rec-"+strconv.Itoa(i), stage+": name i="+strconv.Itoa(i))
	ck(p.pad == uint16(i*13+int(s%251)), stage+": pad i="+strconv.Itoa(i))
	if p.child == nil {
		ck(false, stage+": nil child i="+strconv.Itoa(i))
	} else {
		ck(p.child.v == int64(i)*17+s && p.child.tag == "leaf-"+strconv.Itoa(i), stage+": child i="+strconv.Itoa(i))
	}
	n := 7 + i%6
	ck(len(p.data) == n, stage+": data len i="+strconv.Itoa(i))
	for k := 0; k < n && k < len(p.data); k++ {
		ck(p.data[k] == byte((i*31+k*7+int(s))&0xff), stage+": data byte i="+strconv.Itoa(i)+" k="+strconv.Itoa(k))
	}
}

func ids(ps []*pRec) []int32 {
	out := make([]int32, len(ps))
	for i, p := range ps {
		out[i] = p.id
	}
	return out
}

func equalIDs(a, b []int32) bool {
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
	if s := os.Getenv("UAF_SEED"); s != "" {
		if v, err := strconv.ParseInt(s, 10, 64); err == nil {
			seed = v
		}
	}
	if seed < 0 {
		seed = -seed
	}

	n := 16 + int(seed%5)
	arr := new([20]pRec)
	for i := 0; i < n; i++ {
		arr[i] = newRec(i, seed)
	}
	stride := unsafe.Sizeof(pRec{})
	ck(stride == unsafe.Sizeof(arr[0]), "stride equals element size")
	base := unsafe.Pointer(&arr[0])

	// 1) 单个表达式完成 GEP：元素指针与类型化地址一致。
	perm := make([]*pRec, n)
	for i := 0; i < n; i++ {
		perm[i] = (*pRec)(unsafe.Add(base, uintptr(i)*stride))
	}
	view := unsafe.Slice((*pRec)(base), n)
	for i := 0; i < n; i++ {
		ck(perm[i] == &arr[i], "GEP identity i="+strconv.Itoa(i))
		ck(&view[i] == &arr[i], "unsafe.Slice identity i="+strconv.Itoa(i))
		ck(uintptr(unsafe.Pointer(perm[i]))-uintptr(base) == uintptr(i)*stride, "GEP delta i="+strconv.Itoa(i))
		checkRec(perm[i], i, seed, "initial")
	}

	// 2) 物理邻居关系：元素指针 + stride 指向 id+1。
	for i := 0; i < n; i++ {
		p := perm[i]
		if p.id+1 < int32(n) {
			nxt := (*pRec)(unsafe.Add(unsafe.Pointer(p), stride))
			ck(nxt.id == p.id+1, "physical next from id="+strconv.Itoa(int(p.id)))
			ck(nxt == &arr[p.id+1], "physical next identity")
		}
		if p.id > 0 {
			prv := (*pRec)(unsafe.Add(unsafe.Pointer(p), ^uintptr(stride-1)))
			ck(prv.id == p.id-1, "physical prev from id="+strconv.Itoa(int(p.id)))
		}
	}

	// 3) 指针切片排序：原数组顺序不变。
	sort.Slice(perm, func(a, b int) bool { return perm[a].key < perm[b].key })
	sortedIDs := ids(perm)
	for i := 0; i < n; i++ {
		ck(i+1 == n || perm[i].key < perm[i+1].key, "sorted keys i="+strconv.Itoa(i))
		ck(arr[i].id == int32(i), "array order unchanged after sort i="+strconv.Itoa(i))
	}
	seen := make([]bool, n)
	for _, id := range sortedIDs {
		ck(id >= 0 && int(id) < n, "sorted id range")
		if id >= 0 && int(id) < n {
			ck(!seen[id], "sorted id unique "+strconv.Itoa(int(id)))
			seen[id] = true
		}
	}

	// 4) 反转 + 旋转，逐个校验期望序列。
	for l, r := 0, n-1; l < r; l, r = l+1, r-1 {
		perm[l], perm[r] = perm[r], perm[l]
	}
	reversed := make([]int32, n)
	for i := 0; i < n; i++ {
		reversed[i] = sortedIDs[n-1-i]
	}
	ck(equalIDs(ids(perm), reversed), "reverse order")

	rot := int(seed % int64(n))
	rotated := append([]*pRec(nil), perm[rot:]...)
	rotated = append(rotated, perm[:rot]...)
	wantRot := append(append([]int32(nil), reversed[rot:]...), reversed[:rot]...)
	ck(equalIDs(ids(rotated), wantRot), "rotate order rot="+strconv.Itoa(rot))
	for _, p := range rotated {
		checkRec(p, int(p.id), seed, "after permute")
	}

	// 5) 丢弃数组与视图，只保留指针切片，跨 GC 校验。
	arr = nil
	view = nil
	gcStress()
	for _, p := range rotated {
		checkRec(p, int(p.id), seed, "after GC")
	}

	// 6) 只保留单个内部元素指针，跨 GC 反推数组基址与邻居。
	keep := rotated[n/2]
	keepID := int(keep.id)
	rotated = nil
	gcStress()
	checkRec(keep, keepID, seed, "interior only")
	base2 := (*pRec)(unsafe.Pointer(uintptr(unsafe.Pointer(keep)) - uintptr(keep.id)*stride))
	ck(base2.id == 0, "reconstructed base id")
	ck(base2 == (*pRec)(unsafe.Add(unsafe.Pointer(keep), ^uintptr(uintptr(keep.id)*stride-1))), "reconstructed base identity")
	if keepID+1 < n {
		ck((*pRec)(unsafe.Add(unsafe.Pointer(keep), stride)).id == int32(keepID+1), "neighbor after GC")
	}

	finish("l07_struct_array_pointer_permute")
}
