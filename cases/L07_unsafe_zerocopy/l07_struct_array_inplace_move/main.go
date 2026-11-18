// L07: struct 数组原地移动。通过 unsafe.Add + Sizeof 步长取得元素地址，对含
// string/[]byte/*leaf 的记录做整体交换、旋转与重叠右移；移动后指针字段仍指向
// 原对象，跨显式 GC 校验写屏障与元素定位。
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
			b := make([]byte, 6144+i*23)
			b[0] = byte(i)
			b[len(b)-1] = byte(235 - i)
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

type mvLeaf struct {
	v   int64
	tag string
}

type mvRec struct {
	id   int64
	name string
	link *mvLeaf
	data []byte
	mark uint32
}

func newMvRec(id int, s int64) mvRec {
	n := 5 + int(id&3)
	data := make([]byte, n)
	for k := range data {
		data[k] = byte((id*29 + k*7 + int(s)) & 0xff)
	}
	return mvRec{
		id:   int64(id),
		name: "r-" + strconv.Itoa(id),
		link: &mvLeaf{v: int64(id)*41 + s, tag: "l-" + strconv.Itoa(id)},
		data: data,
		mark: uint32(id*17) ^ uint32(s),
	}
}

func checkMvRec(p *mvRec, id int, s int64, stage string) {
	ck(p != nil, stage+": nil rec id="+strconv.Itoa(id))
	if p == nil {
		return
	}
	ck(p.id == int64(id), stage+": id got="+strconv.FormatInt(p.id, 10))
	ck(p.name == "r-"+strconv.Itoa(id), stage+": name id="+strconv.Itoa(id))
	if p.link == nil {
		ck(false, stage+": nil link id="+strconv.Itoa(id))
	} else {
		ck(p.link.v == int64(id)*41+s && p.link.tag == "l-"+strconv.Itoa(id), stage+": link id="+strconv.Itoa(id))
	}
	n := 5 + int(id&3)
	ck(len(p.data) == n, stage+": data len id="+strconv.Itoa(id))
	for k := 0; k < n && k < len(p.data); k++ {
		ck(p.data[k] == byte((id*29+k*7+int(s))&0xff), stage+": data id="+strconv.Itoa(id)+" k="+strconv.Itoa(k))
	}
	ck(p.mark == uint32(id*17)^uint32(s), stage+": mark id="+strconv.Itoa(id))
}

func elem(base unsafe.Pointer, i int, stride uintptr) *mvRec {
	return (*mvRec)(unsafe.Add(base, uintptr(i)*stride))
}

func reverseRange(base unsafe.Pointer, lo, hi int, stride uintptr) {
	for lo < hi {
		a := elem(base, lo, stride)
		b := elem(base, hi, stride)
		*a, *b = *b, *a
		lo++
		hi--
	}
}

func reverseModel(want []int, lo, hi int) {
	for lo < hi {
		want[lo], want[hi] = want[hi], want[lo]
		lo++
		hi--
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

	n := 18 + int(seed%7)
	arr := make([]mvRec, n)
	for i := 0; i < n; i++ {
		arr[i] = newMvRec(i, seed)
	}
	stride := unsafe.Sizeof(mvRec{})
	base := unsafe.Pointer(&arr[0])
	view := unsafe.Slice((*mvRec)(base), n)
	want := make([]int, n)
	for i := range want {
		want[i] = i
	}
	verify := func(stage string) {
		for i := 0; i < n; i++ {
			ck(view[i].id == int64(want[i]), stage+": order i="+strconv.Itoa(i)+" got="+strconv.FormatInt(view[i].id, 10))
			checkMvRec(&view[i], want[i], seed, stage+" i="+strconv.Itoa(i))
		}
	}

	// 1) GEP 恒等：元素指针与 unsafe.Slice 视图指向同一地址。
	for i := 0; i < n; i++ {
		ck(elem(base, i, stride) == &arr[i], "GEP identity i="+strconv.Itoa(i))
		ck(uintptr(unsafe.Pointer(elem(base, i, stride)))-uintptr(base) == uintptr(i)*stride, "GEP delta i="+strconv.Itoa(i))
		ck(&view[i] == &arr[i], "slice view identity i="+strconv.Itoa(i))
	}
	verify("initial")

	// 2) 三次反转实现左旋 k：每次整体搬移含指针的 struct。
	k := int(seed % int64(n))
	if k == 0 {
		k = 1
	}
	reverseRange(base, 0, k-1, stride)
	reverseRange(base, k, n-1, stride)
	reverseRange(base, 0, n-1, stride)
	reverseModel(want, 0, k-1)
	reverseModel(want, k, n-1)
	reverseModel(want, 0, n-1)
	gcStress()
	verify("rotate k=" + strconv.Itoa(k))

	// 3) 重叠右移：把 [2,n) 整体后移一格，再在 2 号槽写入新记录。
	old := append([]int(nil), want...)
	for i := n - 1; i >= 3; i-- {
		*elem(base, i, stride) = *elem(base, i-1, stride)
	}
	fresh := 1000 + int(seed%97)
	*elem(base, 2, stride) = newMvRec(fresh, seed)
	want[2] = fresh
	copy(want[3:], old[2:n-1])
	gcStress()
	verify("overlap shift")

	// 4) 身份唯一性：每个 id 恰好出现一次，且指针字段编码自洽。
	seen := make(map[int]bool, n)
	for i := 0; i < n; i++ {
		id := int(view[i].id)
		ck(!seen[id], "duplicate id after move id="+strconv.Itoa(id))
		seen[id] = true
	}
	ck(len(seen) == n, "all ids unique")

	// 5) 指针置换视图：元素指针切片与物理位置一一对应。
	perm := make([]*mvRec, n)
	for i := 0; i < n; i++ {
		perm[i] = &view[i]
	}
	for i := 0; i < n; i++ {
		ck(perm[i] == elem(base, i, stride), "perm matches physical i="+strconv.Itoa(i))
	}
	for l, r := 0, n-1; l < r; l, r = l+1, r-1 {
		perm[l], perm[r] = perm[r], perm[l]
	}
	for i := 0; i < n; i++ {
		checkMvRec(perm[i], want[n-1-i], seed, "perm reversed i="+strconv.Itoa(i))
	}

	// 6) 丢弃数组/视图，只保留元素指针切片，跨 GC 校验。
	arr = nil
	view = nil
	base = nil
	gcStress()
	for i := 0; i < n; i++ {
		checkMvRec(perm[i], want[n-1-i], seed, "perm after GC i="+strconv.Itoa(i))
	}

	// 7) 只保留单个内部元素指针，反推数组基址与相邻元素。
	midIdx := n / 2
	mid := elemOf(perm, want, midIdx)
	perm = nil
	gcStress()
	checkMvRec(mid, want[midIdx], seed, "interior after GC")
	prev := (*mvRec)(unsafe.Add(unsafe.Pointer(mid), ^uintptr(stride-1)))
	next := (*mvRec)(unsafe.Add(unsafe.Pointer(mid), stride))
	checkMvRec(prev, want[midIdx-1], seed, "interior prev")
	checkMvRec(next, want[midIdx+1], seed, "interior next")
	base2 := (*mvRec)(unsafe.Pointer(uintptr(unsafe.Pointer(mid)) - uintptr(midIdx)*stride))
	ck(base2.id == int64(want[0]), "reconstructed base first id")

	finish("l07_struct_array_inplace_move")
}

func elemOf(perm []*mvRec, want []int, target int) *mvRec {
	for _, p := range perm {
		if int(p.id) == want[target] {
			return p
		}
	}
	return nil
}
