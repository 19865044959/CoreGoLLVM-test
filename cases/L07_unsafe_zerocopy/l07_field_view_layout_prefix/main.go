// L07: 字段零拷贝访问与前缀布局视图。通过 Offsetof/Add 读写 fExt 的嵌入 fBase 字段，
// 再以 *fBase 视图跨 GC 校验扩展尾部（data/tail）仍被完整扫描。
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
			b := make([]byte, 6144+i*5)
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

type fLeaf struct {
	v   int64
	tag string
}

type fMeta struct {
	code  uint64
	label string
}

type fBase struct {
	id   int64
	name string
	link *fLeaf
	meta fMeta
	n    int32
}

type fExt struct {
	fBase
	extra [24]byte
	data  []byte
	tail  *fLeaf
}

func fieldString(p unsafe.Pointer, off uintptr) string {
	return *(*string)(unsafe.Add(p, off))
}

func fieldPtr[T any](p unsafe.Pointer, off uintptr) *T {
	return (*T)(unsafe.Add(p, off))
}

func makeExt(i int, s int64) *fExt {
	e := &fExt{
		fBase: fBase{
			id:   int64(i),
			name: "user-" + strconv.Itoa(i),
			link: &fLeaf{v: int64(i)*19 + s, tag: "l-" + strconv.Itoa(i)},
			meta: fMeta{code: uint64(i)*1009 + uint64(s%71), label: "meta-" + strconv.Itoa(i)},
			n:    int32(i*37 + int(s%17)),
		},
		data: make([]byte, 10+i%7),
		tail: &fLeaf{v: int64(i)*53 - s, tag: "tail-" + strconv.Itoa(i)},
	}
	for k := range e.extra {
		e.extra[k] = byte((i*29 + k*17 + int(s)) & 0xff)
	}
	for k := range e.data {
		e.data[k] = byte((i*43 + k*7 + int(s)) & 0xff)
	}
	return e
}

func checkExtViaBase(pb *fBase, i int, s int64, name string, linkV int64, linkTag, stage string) {
	ex := (*fExt)(unsafe.Pointer(pb))
	ck(&ex.fBase == pb, stage+": base view address")
	ck(pb.id == int64(i), stage+": id")
	ck(pb.name == name, stage+": name")
	ck(pb.link != nil && pb.link.v == linkV && pb.link.tag == linkTag, stage+": link")
	ck(pb.meta.code == uint64(i)*1009+uint64(s%71) && pb.meta.label == "meta-"+strconv.Itoa(i), stage+": meta")
	ck(pb.n == int32(i*37+int(s%17)), stage+": n")
	ck(ex.tail != nil && ex.tail.v == int64(i)*53-s && ex.tail.tag == "tail-"+strconv.Itoa(i), stage+": tail")
	ck(len(ex.data) == 10+i%7, stage+": data len")
	for k := range ex.data {
		ck(ex.data[k] == byte((i*43+k*7+int(s))&0xff), stage+": data k="+strconv.Itoa(k))
	}
	for k := range ex.extra {
		ck(ex.extra[k] == byte((i*29+k*17+int(s))&0xff), stage+": extra k="+strconv.Itoa(k))
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

	offBase := unsafe.Offsetof(fExt{}.fBase)
	offName := offBase + unsafe.Offsetof(fBase{}.name)
	offLink := offBase + unsafe.Offsetof(fBase{}.link)
	offMeta := offBase + unsafe.Offsetof(fBase{}.meta)
	offLabel := offMeta + unsafe.Offsetof(fMeta{}.label)
	offN := offBase + unsafe.Offsetof(fBase{}.n)
	offData := unsafe.Offsetof(fExt{}.data)
	offTail := unsafe.Offsetof(fExt{}.tail)
	offExtra := unsafe.Offsetof(fExt{}.extra)

	ck(offBase == 0, "embedded base at offset 0")
	ck(unsafe.Sizeof(fBase{}) <= unsafe.Sizeof(fExt{}), "base not larger than extended")
	ck(offName == unsafe.Offsetof(fBase{}.name), "prefix name offset")
	ck(offLink == unsafe.Offsetof(fBase{}.link), "prefix link offset")
	ck(offN == unsafe.Offsetof(fBase{}.n), "prefix n offset")

	const objID = 7
	e := makeExt(objID, seed)
	p := unsafe.Pointer(e)
	pb := (*fBase)(p)
	ck(pb == &e.fBase, "downcast base pointer identity")

	// 1) 通过字段偏移零拷贝读取：string 头直接引用同一字段。
	nameOff := fieldString(p, offName)
	ck(nameOff == e.name, "field string value")
	ck(unsafe.StringData(nameOff) == unsafe.StringData(e.name), "field string data pointer shared")
	linkSlot := fieldPtr[*fLeaf](p, offLink)
	ck(*linkSlot == e.link && (*linkSlot).tag == "l-"+strconv.Itoa(objID), "field link slot")
	metaSlot := fieldPtr[fMeta](p, offMeta)
	ck(metaSlot.label == "meta-"+strconv.Itoa(objID), "field meta label")
	labelSlot := fieldPtr[string](p, offLabel)
	ck(*labelSlot == metaSlot.label, "nested label field pointer")
	dataSlot := fieldPtr[[]byte](p, offData)
	ck(len(*dataSlot) == len(e.data) && &(*dataSlot)[0] == &e.data[0], "data slice header alias")
	nSlot := fieldPtr[int32](p, offN)
	ck(*nSlot == e.n, "scalar field pointer")

	// 2) 标量字段的字节视图别名。
	oldN := e.n
	nb := (*[4]byte)(unsafe.Pointer(nSlot))
	nb[0] ^= 0xff
	ck(e.n != oldN, "byte view write visible through field")
	nb[0] ^= 0xff
	ck(e.n == oldN, "scalar restored")

	// 3) 通过字段指针写入 string/指针字段，验证写屏障后的值。
	newName := "renamed-" + strconv.Itoa(objID)
	*fieldPtr[string](p, offName) = newName
	ck(e.name == newName, "write string field via offset")
	newLink := &fLeaf{v: 900 + seed, tag: "new-link"}
	*linkSlot = newLink
	ck(e.link == newLink && e.link.tag == "new-link", "write link field via offset")
	e.link = newLink
	linkV, linkTag := newLink.v, newLink.tag
	newLink = nil

	// 4) 丢弃扩展类型变量，只保留 base 视图与字段指针，跨 GC 校验尾部指针。
	tailV, tailTag := e.tail.v, e.tail.tag
	extraView := unsafe.Slice((*byte)(unsafe.Add(p, offExtra)), 24)
	namePtr := fieldPtr[string](p, offName)
	dataPtr := dataSlot
	tailPtr := fieldPtr[*fLeaf](p, offTail)
	e = nil
	gcStress()

	ck(pb.name == newName, "base view name after GC")
	ck(*namePtr == newName, "name field pointer after GC")
	ck(pb.link != nil && pb.link.v == linkV && pb.link.tag == linkTag, "link after GC")
	ck(pb.meta.label == "meta-"+strconv.Itoa(objID), "meta label after GC")
	ck(pb.n == oldN, "scalar after GC")
	ck((*dataPtr)[0] == byte((objID*43+0*7+int(seed))&0xff), "data field after GC")
	for k := 0; k < 24; k++ {
		ck(extraView[k] == byte((objID*29+k*17+int(seed))&0xff), "extra after GC k="+strconv.Itoa(k))
	}
	ck((*tailPtr).v == tailV && (*tailPtr).tag == tailTag, "tail pointer after GC")
	checkExtViaBase(pb, objID, seed, newName, linkV, linkTag, "base view after GC")

	// 5) 只保留 string 字段内部指针，反推对象并验证扩展尾部。
	e2 := makeExt(objID+1, seed+5)
	np2 := fieldPtr[string](unsafe.Pointer(e2), offName)
	wantTail := *e2.tail
	e2 = nil
	gcStress()
	ck(*np2 == "user-"+strconv.Itoa(objID+1), "lone string field pointer after GC")
	base2 := (*fExt)(unsafe.Pointer(uintptr(unsafe.Pointer(np2)) - offName))
	ck(base2.id == int64(objID+1), "reconstructed extended object id")
	ck(base2.tail != nil && *base2.tail == wantTail, "reconstructed tail")
	ck(len(base2.data) == 10+(objID+1)%7, "reconstructed data")

	finish("l07_field_view_layout_prefix")
}
