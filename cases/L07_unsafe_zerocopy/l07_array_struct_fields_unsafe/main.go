// L07: struct 数组的字段访问：通过 Offsetof + unsafe.Add 定位嵌套 struct 的
// 数组字段（[3]string、[2][]byte、指针槽位），与普通字段访问对照，跨 GC 校验。
package main

import (
	"fmt"
	"os"
	"runtime"
	"strconv"
	"sync/atomic"
	"unsafe"
)

type fleaf struct {
	v   int64
	tag string
}

type attr struct {
	score int32
	tags  [3]string
	bufs  [2][]byte
	link  *fleaf
}

type row struct {
	key  string
	n    uint16
	at   attr
	refs [2]*fleaf
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

func newRow(i int, s int64) row {
	return row{
		key: "row-" + strconv.Itoa(i),
		n:   uint16(i),
		at: attr{
			score: int32(i)*11 + int32(s%7),
			tags:  [3]string{"t0-" + strconv.Itoa(i), "t1-" + strconv.Itoa(i), "t2-" + strconv.Itoa(i)},
			bufs:  [2][]byte{[]byte("b0-" + strconv.Itoa(i)), []byte("b1-" + strconv.Itoa(i))},
			link:  &fleaf{v: int64(i)*5 + s, tag: "fl-" + strconv.Itoa(i)},
		},
		refs: [2]*fleaf{
			{v: int64(i)*9 + s, tag: "r0-" + strconv.Itoa(i)},
			{v: int64(i)*9 + s + 1, tag: "r1-" + strconv.Itoa(i)},
		},
	}
}

func fingerprint(rows *[16]row, n, skip int) uint64 {
	var h uint64 = 1469598103934665603
	for i := 0; i < n; i++ {
		if i == skip {
			continue
		}
		for _, c := range []byte(rows[i].key) {
			h ^= uint64(c)
			h *= 1099511628211
		}
		h ^= uint64(rows[i].n)
		h *= 1099511628211
		h ^= uint64(rows[i].at.score)
		h *= 1099511628211
	}
	return h
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

	n := 9 + int(seed%5)
	rows := new([16]row)
	for i := 0; i < n; i++ {
		rows[i] = newRow(i, seed)
	}

	ck(unsafe.Sizeof(attr{}) == 112, "attr size")
	ck(unsafe.Alignof(attr{}) == 8, "attr align")
	ck(unsafe.Offsetof(attr{}.score) == 0, "attr score offset")
	ck(unsafe.Offsetof(attr{}.tags) == 8, "attr tags offset")
	ck(unsafe.Offsetof(attr{}.bufs) == 56, "attr bufs offset")
	ck(unsafe.Offsetof(attr{}.link) == 104, "attr link offset")
	ck(unsafe.Sizeof(row{}) == 152, "row size")
	ck(unsafe.Offsetof(row{}.at) == 24, "row at offset")
	ck(unsafe.Offsetof(row{}.refs) == 136, "row refs offset")

	i := 1 + int(seed)%(n-1)
	ep := &rows[i]
	atOff := unsafe.Offsetof(row{}.at)

	// 1) 通过 Offsetof 定位嵌套 struct 内的数组字段，与类型化地址一致。
	tp := (*[3]string)(unsafe.Add(unsafe.Pointer(ep), atOff+unsafe.Offsetof(attr{}.tags)))
	bp := (*[2][]byte)(unsafe.Add(unsafe.Pointer(ep), atOff+unsafe.Offsetof(attr{}.bufs)))
	lp := (**fleaf)(unsafe.Add(unsafe.Pointer(ep), atOff+unsafe.Offsetof(attr{}.link)))
	rp := (*[2]*fleaf)(unsafe.Add(unsafe.Pointer(ep), unsafe.Offsetof(row{}.refs)))
	kp := (*string)(unsafe.Add(unsafe.Pointer(ep), unsafe.Offsetof(row{}.key)))
	np := (*uint16)(unsafe.Add(unsafe.Pointer(ep), unsafe.Offsetof(row{}.n)))
	ck(tp == &ep.at.tags, "tags field pointer identity")
	ck(bp == &ep.at.bufs, "bufs field pointer identity")
	ck(lp == &ep.at.link, "link field pointer identity")
	ck(rp == &ep.refs, "refs field pointer identity")
	ck(kp == &ep.key, "key field pointer identity")
	ck(np == &ep.n, "n field pointer identity")
	ck(uintptr(unsafe.Pointer(&ep.at.tags))-uintptr(unsafe.Pointer(ep)) == atOff+unsafe.Offsetof(attr{}.tags),
		"nested field offset sum")
	ck(unsafe.Sizeof(ep.at.bufs[0]) == unsafe.Sizeof([]byte(nil)), "slice field header width")
	ck(tp[1] == "t1-"+strconv.Itoa(i) && string(bp[1]) == "b1-"+strconv.Itoa(i), "nested array fields")
	ck((*lp).v == int64(i)*5+seed, "nested pointer field")
	ck(rp[0].tag == "r0-"+strconv.Itoa(i) && rp[1].tag == "r1-"+strconv.Itoa(i), "refs array fields")

	// 2) 经 unsafe 字段指针写入，普通字段访问可见，且不影响邻居元素。
	before := fingerprint(rows, n, i)
	oldKey, oldTag, oldBuf := ep.key, ep.at.tags[1], append([]byte(nil), ep.at.bufs[0]...)
	oldLink, oldRef := ep.at.link, ep.refs[1]
	*kp = "mut-" + strconv.Itoa(i)
	np2 := uint16(i + 100)
	*np = np2
	tp[1] = "mut-tag-" + strconv.Itoa(i)
	bp[0] = []byte("mut-buf-" + strconv.Itoa(i))
	*lp = &fleaf{v: int64(i) + 700, tag: "mut-link"}
	rp[1] = &fleaf{v: int64(i) + 800, tag: "mut-ref"}
	ck(ep.key == "mut-"+strconv.Itoa(i) && ep.n == np2, "scalar field writes visible")
	ck(ep.at.tags[1] == "mut-tag-"+strconv.Itoa(i), "string array field write visible")
	ck(string(ep.at.bufs[0]) == "mut-buf-"+strconv.Itoa(i), "slice array field write visible")
	ck(ep.at.link.tag == "mut-link" && ep.refs[1].tag == "mut-ref", "pointer array field writes visible")
	ck(fingerprint(rows, n, i) == before, "neighbor rows untouched by field writes")

	gcStress()
	ck(ep.key == "mut-"+strconv.Itoa(i) && ep.at.tags[1] == "mut-tag-"+strconv.Itoa(i), "mutated fields after GC")
	ck(string(ep.at.bufs[0]) == "mut-buf-"+strconv.Itoa(i), "mutated buffer after GC")
	ck(ep.at.link.v == int64(i)+700 && ep.refs[1].v == int64(i)+800, "new pointer targets after GC")
	ck(oldKey == "row-"+strconv.Itoa(i) && oldTag == "t1-"+strconv.Itoa(i), "old string values remain valid")
	ck(string(oldBuf) == "b0-"+strconv.Itoa(i), "old buffer copy remains valid")
	ck(oldLink.tag == "fl-"+strconv.Itoa(i) && oldRef.tag == "r1-"+strconv.Itoa(i), "replaced pointer targets remain valid")

	// 3) 清零一个元素，邻居不受影响；旧字段值由类型化引用保活。
	zidx := (i + 1) % n
	zbase := fingerprint(rows, n, zidx)
	zp := &rows[zidx]
	zOldKey := zp.key
	zOldLink := zp.at.link
	zpOldBuf := append([]byte(nil), zp.at.bufs[1]...)
	*zp = row{}
	ck(zp.key == "" && zp.n == 0 && zp.at.tags[2] == "" && len(zp.at.bufs[1]) == 0 &&
		zp.at.link == nil && zp.refs[0] == nil, "zeroed element fields")
	ck(fingerprint(rows, n, zidx) == zbase, "neighbors untouched by zeroing")
	gcStress()
	ck(zp.at.link == nil && zp.refs[1] == nil, "zeroed pointers nil after GC")
	ck(zOldKey == "row-"+strconv.Itoa(zidx) && zOldLink.tag == "fl-"+strconv.Itoa(zidx), "zeroed old values survive")
	ck(string(zpOldBuf) == "b1-"+strconv.Itoa(zidx), "zeroed old buffer copy survives")

	// 4) 只保留字段指针，丢弃数组变量；GC 后反推元素并校验。
	keepKey := &rows[i].key
	keepTags := &rows[i].at.tags
	keepBuf := &rows[i].at.bufs[0]
	keepLink := &rows[i].at.link
	keepRefs := &rows[i].refs
	rows = nil
	gcStress()
	re := (*row)(unsafe.Pointer(uintptr(unsafe.Pointer(keepKey)) - unsafe.Offsetof(row{}.key)))
	ck(re.n == np2, "element reconstructed from field pointer after GC")
	ck((*keepKey) == "mut-"+strconv.Itoa(i), "key field pointer after GC")
	ck((*keepTags)[1] == "mut-tag-"+strconv.Itoa(i), "tags field pointer after GC")
	ck(string((*keepBuf)) == "mut-buf-"+strconv.Itoa(i), "bufs field pointer after GC")
	ck((*keepLink).tag == "mut-link" && (*keepRefs)[1].tag == "mut-ref", "pointer slots after GC")
	ck((*keepRefs)[1].v == int64(i)+800, "pointer slot payload after GC")

	runtime.KeepAlive(keepKey)
	runtime.KeepAlive(keepTags)
	runtime.KeepAlive(keepBuf)
	runtime.KeepAlive(keepLink)
	runtime.KeepAlive(keepRefs)
	finish("l07_array_struct_fields_unsafe")
}
