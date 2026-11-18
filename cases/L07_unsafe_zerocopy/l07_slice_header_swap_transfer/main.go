// L07: slice header 交换与所有权转移。手工读写 {data,len,cap} 头在两个切片间交换，
// 覆盖 cap 内追加、满 cap 追加重分配、恢复头以及 GC 后的元素存活。
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
			b := make([]byte, 7168+i*11)
			b[0] = byte(i)
			b[len(b)-1] = byte(210 - i)
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

type sLeaf struct {
	v   int64
	tag string
}

type sRec struct {
	id   int64
	name string
	link *sLeaf
	data []byte
}

type shdr struct {
	data unsafe.Pointer
	len  int
	cap  int
}

func newSRec(i int, s int64) sRec {
	n := 6 + i%5
	data := make([]byte, n)
	for k := range data {
		data[k] = byte((i*41 + k*13 + int(s)) & 0xff)
	}
	return sRec{
		id:   int64(i),
		name: "s-" + strconv.Itoa(i),
		link: &sLeaf{v: int64(i)*23 + s, tag: "t-" + strconv.Itoa(i)},
		data: data,
	}
}

func checkSRec(r sRec, i int, s int64, stage string) {
	ck(r.id == int64(i), stage+": id i="+strconv.Itoa(i))
	ck(r.name == "s-"+strconv.Itoa(i), stage+": name i="+strconv.Itoa(i))
	if r.link == nil {
		ck(false, stage+": nil link i="+strconv.Itoa(i))
	} else {
		ck(r.link.v == int64(i)*23+s && r.link.tag == "t-"+strconv.Itoa(i), stage+": link i="+strconv.Itoa(i))
	}
	n := 6 + i%5
	ck(len(r.data) == n, stage+": data len i="+strconv.Itoa(i))
	for k := 0; k < n && k < len(r.data); k++ {
		ck(r.data[k] == byte((i*41+k*13+int(s))&0xff), stage+": data i="+strconv.Itoa(i)+" k="+strconv.Itoa(k))
	}
}

func headerOf[T any](s []T) shdr {
	return *(*shdr)(unsafe.Pointer(&s))
}

func storeHeader[T any](sp *[]T, h shdr) {
	*(*shdr)(unsafe.Pointer(sp)) = h
}

func ptrOf[T any](s []T) unsafe.Pointer {
	return unsafe.Pointer(unsafe.SliceData(s))
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

	const la, ca = 4, 8
	const lb, cb = 6, 6
	a := make([]sRec, la, ca)
	b := make([]sRec, lb, cb)
	for i := 0; i < la; i++ {
		a[i] = newSRec(i, seed)
	}
	for i := 0; i < lb; i++ {
		b[i] = newSRec(100+i, seed)
	}
	a0 := a
	b0 := b
	a0c := a0[:cap(a0)]

	ha := headerOf(a)
	hb := headerOf(b)
	ck(ha.len == la && ha.cap == ca, "header A len/cap")
	ck(hb.len == lb && hb.cap == cb, "header B len/cap")
	ck(ha.data == ptrOf(a), "header A data pointer")
	ck(hb.data == ptrOf(b), "header B data pointer")

	// 1) 交换头：内容、len、cap、data 指针全部随头转移。
	storeHeader(&a, hb)
	storeHeader(&b, ha)
	ck(headerOf(a) == hb && headerOf(b) == ha, "header values swapped")
	ck(len(a) == lb && cap(a) == cb, "A takes B len/cap")
	ck(len(b) == la && cap(b) == ca, "B takes A len/cap")
	ck(ptrOf(a) == hb.data && ptrOf(b) == ha.data, "data pointers swapped")
	for i := 0; i < lb; i++ {
		checkSRec(a[i], 100+i, seed, "A after swap")
	}
	for i := 0; i < la; i++ {
		checkSRec(b[i], i, seed, "B after swap")
	}
	ck(a0[0].id == 0 && b0[0].id == 100, "saved headers still refer to original backing")

	// 2) 恢复头，原对象完全复原。
	storeHeader(&a, ha)
	storeHeader(&b, hb)
	ck(headerOf(a) == ha && headerOf(b) == hb, "header values restored")
	for i := 0; i < la; i++ {
		checkSRec(a[i], i, seed, "A restored")
	}
	for i := 0; i < lb; i++ {
		checkSRec(b[i], 100+i, seed, "B restored")
	}

	// 3) 再次交换后：对 A 的原 cap 内 append 应原地写 A 的底层数组。
	storeHeader(&a, hb)
	storeHeader(&b, ha)
	b = append(b, newSRec(200, seed), newSRec(201, seed))
	ck(len(b) == 6 && cap(b) == 8, "append within cap keeps cap")
	ck(ptrOf(b) == ha.data, "append within cap keeps data pointer")
	checkSRec(b[4], 200, seed, "append in place 4")
	checkSRec(b[5], 201, seed, "append in place 5")
	checkSRec(a0c[4], 200, seed, "saved original A sees in-place append")
	checkSRec(a0c[5], 201, seed, "saved original A sees in-place append 5")
	ck(b0[0].id == 100 && len(b0) == 6, "original B unaffected by append to A")

	// 4) 对 B 的原满 cap append 应重新分配，旧 B 数组不被改写。
	a = append(a, newSRec(300, seed))
	ck(len(a) == 7 && cap(a) >= 7, "append beyond cap grows")
	ck(ptrOf(a) != hb.data, "append beyond cap reallocates")
	checkSRec(a[6], 300, seed, "reallocated element")
	for i := 0; i < lb; i++ {
		checkSRec(a[i], 100+i, seed, "A after realloc")
		checkSRec(b0[i], 100+i, seed, "old B backing intact")
	}

	// 5) 丢弃保存的旧头，跨 GC 校验两个切片及其指针字段。
	a0 = nil
	b0 = nil
	gcStress()
	for i := 0; i < len(a); i++ {
		id := 100 + i
		if i == 6 {
			id = 300
		}
		checkSRec(a[i], id, seed, "A after GC")
	}
	for i := 0; i < len(b); i++ {
		id := i
		if i == 4 {
			id = 200
		} else if i == 5 {
			id = 201
		}
		checkSRec(b[i], id, seed, "B after GC")
	}

	// 6) 只保留元素指针，切片头全部丢弃。
	keepA := &a[3]
	keepB := &b[4]
	a = nil
	b = nil
	gcStress()
	checkSRec(*keepA, 103, seed, "interior A after GC")
	checkSRec(*keepB, 200, seed, "interior B after GC")
	ck(keepA != keepB, "interior pointers distinct")

	finish("l07_slice_header_swap_transfer")
}
