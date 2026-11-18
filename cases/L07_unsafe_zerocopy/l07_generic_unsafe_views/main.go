// L07: 泛型 unsafe helper：用类型参数 T 计算 Sizeof 步长、构造 []T 视图与
// {data,len,cap} 头，实例化为 byte/uint32/含指针 struct 与自定义字节类型。
package main

import (
	"fmt"
	"os"
	"runtime"
	"strconv"
	"sync/atomic"
	"unsafe"
)

type gnode struct {
	v   int64
	tag string
}

type grecl struct {
	id   int64
	name string
	link *gnode
	data []byte
}

type gslot[T any] struct {
	p   *T
	tag string
}

type myByte byte

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

func gSlice[T any](p *T, n int) []T {
	return unsafe.Slice(p, n)
}

func gAt[T any](p *T, i int) *T {
	var zero T
	return (*T)(unsafe.Add(unsafe.Pointer(p), uintptr(i)*unsafe.Sizeof(zero)))
}

func gHeaderSlice[T any](data unsafe.Pointer, n int) []T {
	h := struct {
		data unsafe.Pointer
		len  int
		cap  int
	}{data: data, len: n, cap: n}
	return *(*[]T)(unsafe.Pointer(&h))
}

func gBytesString[T ~byte](s []T) string {
	if len(s) == 0 {
		return ""
	}
	return unsafe.String((*byte)(unsafe.Pointer(unsafe.SliceData(s))), len(s))
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

	// 1) T=byte：泛型视图、元素地址、header 构造与跨 GC。
	barr := new([160]byte)
	for i := range barr {
		barr[i] = byte(i*7 + int(seed))
	}
	bv := gSlice(&barr[0], len(barr))
	ck(len(bv) == 160, "byte view length")
	k := int(seed) % len(barr)
	ck(gAt(&barr[0], k) == &barr[k], "byte gAt identity")
	ck(uintptr(unsafe.Pointer(gAt(&barr[0], k)))-uintptr(unsafe.Pointer(&barr[0])) == uintptr(k)*unsafe.Sizeof(barr[0]),
		"byte gAt stride")
	gh := gHeaderSlice[byte](unsafe.Pointer(&barr[0]), 96)
	ck(len(gh) == 96 && cap(gh) == 96 && unsafe.SliceData(gh) == &barr[0], "generic header slice shape")
	barr = nil
	gcStress()
	for i := 0; i < len(bv); i += 13 {
		ck(bv[i] == byte(i*7+int(seed)), "byte view after GC i="+strconv.Itoa(i))
	}
	for i := 0; i < len(gh); i += 17 {
		ck(gh[i] == byte(i*7+int(seed)), "header view after GC i="+strconv.Itoa(i))
	}

	// 2) T=uint32：步长等于 Sizeof。
	uarr := new([40]uint32)
	for i := range uarr {
		uarr[i] = uint32(i)*3 + uint32(seed%11)
	}
	uv := gSlice(&uarr[0], len(uarr))
	ck(unsafe.Sizeof(uarr[0]) == 4, "uint32 width")
	for j := 0; j < len(uarr); j += 7 {
		ck(gAt(&uarr[0], j) == &uarr[j], "uint32 gAt identity")
		ck(*gAt(&uv[0], j) == uint32(j)*3+uint32(seed%11), "uint32 value")
	}
	uarr = nil
	gcStress()
	for j := 0; j < len(uv); j += 5 {
		ck(uv[j] == uint32(j)*3+uint32(seed%11), "uint32 view after GC j="+strconv.Itoa(j))
	}

	// 3) T=含指针 struct：泛型视图/元素指针保活数组与字段。
	rarr := new([14]grecl)
	for i := range rarr {
		rarr[i] = grecl{
			id:   int64(i),
			name: "g-" + strconv.Itoa(i),
			link: &gnode{v: int64(i)*5 + seed, tag: "gn-" + strconv.Itoa(i)},
			data: []byte("gd-" + strconv.Itoa(i)),
		}
	}
	rv := gSlice(&rarr[0], len(rarr))
	ri := int(seed) % len(rarr)
	rp := gAt(&rarr[0], ri)
	ck(rp == &rarr[ri], "record gAt identity")
	ck(uintptr(unsafe.Pointer(rp))-uintptr(unsafe.Pointer(&rarr[0])) == uintptr(ri)*unsafe.Sizeof(grecl{}),
		"record stride")
	oldName := rp.name
	oldLink := rp.link
	rarr = nil
	gcStress()
	ck(rp.id == int64(ri) && rp.name == oldName, "record fields after GC")
	ck(rp.link != nil && rp.link.v == oldLink.v && rp.link.tag == oldLink.tag, "record link after GC")
	ck(string(rp.data) == "gd-"+strconv.Itoa(ri), "record data after GC")
	for j := 0; j < len(rv); j += 3 {
		ck(rv[j].id == int64(j) && rv[j].name == "g-"+strconv.Itoa(j), "record view after GC j="+strconv.Itoa(j))
	}

	// 4) 泛型指针槽位数组。
	sarr := new([9]gslot[gnode])
	for i := range sarr {
		sarr[i] = gslot[gnode]{p: &gnode{v: int64(i) + seed, tag: "sp-" + strconv.Itoa(i)}, tag: "st-" + strconv.Itoa(i)}
	}
	sv := gSlice(&sarr[0], len(sarr))
	si := int(seed) % len(sarr)
	sp := gAt(&sarr[0], si)
	ck(sp == &sarr[si], "slot gAt identity")
	keepSlot := sp
	sarr = nil
	gcStress()
	ck(keepSlot.p != nil && keepSlot.p.tag == "sp-"+strconv.Itoa(si), "generic pointer slot after GC")
	ck(keepSlot.tag == "st-"+strconv.Itoa(si), "generic slot tag after GC")
	for j := 0; j < len(sv); j += 2 {
		ck(sv[j].p != nil && sv[j].p.tag == "sp-"+strconv.Itoa(j), "slot view after GC j="+strconv.Itoa(j))
	}

	// 5) ~byte 约束：[]byte 与自定义字节类型的零拷贝 string 视图。
	src := []byte("generic-bytes-" + strconv.FormatInt(seed, 10) + "-tail")
	sview := gBytesString(src)
	ck(sview == string(src), "generic bytes->string content")
	ck(unsafe.StringData(sview) == unsafe.SliceData(src), "generic bytes->string alias")
	src[0] = 'G'
	ck(sview[0] == 'G', "generic string observes source mutation")
	copyStr := string(src)
	ck(copyStr[0] == 'G' && unsafe.StringData(copyStr) != unsafe.SliceData(src), "copy string disjoint")
	mb := []myByte{'m', 'y', '-', 'b', 'y', 't', 'e'}
	ms := gBytesString(mb)
	ck(ms == "my-byte", "custom byte type string")
	mb[0] = 'M'
	ck(ms[0] == 'M', "custom byte type alias")
	ck(gBytesString([]byte(nil)) == "", "empty generic bytes string")
	ck(gBytesString([]myByte{}) == "", "empty custom bytes string")

	// 6) 丢弃源切片，仅保留 string 视图与元素指针，GC 后仍有效。
	keepStr := ms
	keepBytesView := bv
	src = nil
	mb = nil
	gcStress()
	ck(keepStr == "My-byte", "custom byte string after GC")
	ck(keepBytesView[3] == byte(3*7+int(seed)), "byte view retained after GC")
	ck(string(keepBytesView[:8]) == string([]byte{byte(0*7 + int(seed)), byte(1*7 + int(seed)), byte(2*7 + int(seed)), byte(3*7 + int(seed)), byte(4*7 + int(seed)), byte(5*7 + int(seed)), byte(6*7 + int(seed)), byte(7*7 + int(seed))}), "byte view prefix")

	runtime.KeepAlive(rp)
	runtime.KeepAlive(keepSlot)
	runtime.KeepAlive(keepStr)
	runtime.KeepAlive(keepBytesView)
	finish("l07_generic_unsafe_views")
}
