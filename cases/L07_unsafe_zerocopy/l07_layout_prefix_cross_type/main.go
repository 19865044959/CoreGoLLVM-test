// L07: 跨类型的前缀布局。两个扩展类型不通过嵌入、但前缀字段类型与顺序完全一致，
// 断言 Offsetof 相等后用 *prefix 视图读写公共前缀，尾部各自不同的指针字段仍被扫描。
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
			b := make([]byte, 6144+i*27)
			b[0] = byte(i)
			b[len(b)-1] = byte(220 - i)
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

type lfLeaf struct {
	v   int64
	tag string
}

type lfPrefix struct {
	id   int64
	name string
	link *lfLeaf
	code int32
}

type lfExtA struct {
	id   int64
	name string
	link *lfLeaf
	code int32
	data []byte
	tail *lfLeaf
	mark uint64
}

type lfExtB struct {
	id    int64
	name  string
	link  *lfLeaf
	code  int32
	table [4]uint64
	tailB *lfLeaf
}

func newLFLeaf(v int64, tag string) *lfLeaf {
	return &lfLeaf{v: v, tag: tag}
}

func checkLFPrefix(p *lfPrefix, id int, name string, leafV int64, leafTag string, code int32, stage string) {
	ck(p.id == int64(id), stage+": id")
	ck(p.name == name, stage+": name got="+p.name)
	ck(p.code == code, stage+": code")
	if p.link == nil {
		ck(false, stage+": nil link")
	} else {
		ck(p.link.v == leafV && p.link.tag == leafTag, stage+": link")
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

	// 1) 两种扩展类型与 prefix 的前缀布局逐字段一致，且尾部起点相同。
	offName := unsafe.Offsetof(lfPrefix{}.name)
	offLink := unsafe.Offsetof(lfPrefix{}.link)
	offCode := unsafe.Offsetof(lfPrefix{}.code)
	prefixSize := unsafe.Sizeof(lfPrefix{})
	ck(unsafe.Offsetof(lfExtA{}.id) == unsafe.Offsetof(lfExtB{}.id), "id offset")
	ck(unsafe.Offsetof(lfExtA{}.name) == offName, "A name offset")
	ck(unsafe.Offsetof(lfExtB{}.name) == offName, "B name offset")
	ck(unsafe.Offsetof(lfExtA{}.link) == offLink, "A link offset")
	ck(unsafe.Offsetof(lfExtB{}.link) == offLink, "B link offset")
	ck(unsafe.Offsetof(lfExtA{}.code) == offCode, "A code offset")
	ck(unsafe.Offsetof(lfExtB{}.code) == offCode, "B code offset")
	ck(unsafe.Offsetof(lfExtA{}.data) == prefixSize, "A suffix at prefix end")
	ck(unsafe.Offsetof(lfExtB{}.table) == prefixSize, "B suffix at prefix end")
	ck(unsafe.Offsetof(lfExtA{}.tail) > prefixSize, "A tail after prefix")
	ck(unsafe.Offsetof(lfExtB{}.tailB) > prefixSize, "B tail after prefix")
	ck(unsafe.Sizeof(lfExtA{}) == unsafe.Sizeof(lfExtB{}), "same total size")

	leafVA := int64(700) + seed
	leafVB := int64(800) - seed
	codeA := int32(70 + seed%17)
	codeB := int32(90 + seed%23)
	ea := &lfExtA{
		id:   7,
		name: "A-7",
		link: newLFLeaf(leafVA, "la-7"),
		code: codeA,
		data: []byte{1, 3, 5, 7, 9},
		tail: newLFLeaf(107+seed, "ta-7"),
		mark: 0x1122334455667788 ^ uint64(seed),
	}
	eb := &lfExtB{
		id:    8,
		name:  "B-8",
		link:  newLFLeaf(leafVB, "lb-8"),
		code:  codeB,
		table: [4]uint64{11 + uint64(seed), 22, 33, 44},
		tailB: newLFLeaf(108-seed, "tb-8"),
	}
	for k := range ea.data {
		ea.data[k] = byte(k*17 + int(seed))
	}
	pa := (*lfPrefix)(unsafe.Pointer(ea))
	pb := (*lfPrefix)(unsafe.Pointer(eb))
	ck(unsafe.Pointer(pa) == unsafe.Pointer(ea), "A prefix pointer identity")
	ck(unsafe.Pointer(pb) == unsafe.Pointer(eb), "B prefix pointer identity")

	// 2) 通过前缀视图读取公共字段；尾部字段通过完整类型读取。
	checkLFPrefix(pa, 7, "A-7", leafVA, "la-7", codeA, "A initial")
	checkLFPrefix(pb, 8, "B-8", leafVB, "lb-8", codeB, "B initial")
	ck(ea.data[0] == byte(int(seed)) && len(ea.data) == 5, "A data initial")
	ck(ea.tail.v == 107+seed && ea.tail.tag == "ta-7", "A tail initial")
	ck(eb.table[0] == 11+uint64(seed) && eb.table[3] == 44, "B table initial")
	ck(eb.tailB.v == 108-seed && eb.tailB.tag == "tb-8", "B tail initial")

	// 3) 通过前缀视图写 string/指针字段，完整类型立即可见。
	pa.name = "renamed-a"
	nlA := newLFLeaf(900+seed, "new-a")
	pa.link = nlA
	ck(ea.name == "renamed-a" && ea.link == nlA, "A write via prefix visible")

	// 4) 通过字段偏移直接写另一个类型的前缀字段。
	*(*string)(unsafe.Add(unsafe.Pointer(eb), offName)) = "renamed-b"
	nlB := newLFLeaf(901-seed, "new-b")
	*(**lfLeaf)(unsafe.Add(unsafe.Pointer(eb), offLink)) = nlB
	checkLFPrefix(pb, 8, "renamed-b", 901-seed, "new-b", codeB, "B after offset write")
	ck(eb.link == nlB, "B link via offset visible")

	// 5) 混合类型放进 []*prefix，按后缀偏移校验各自尾部。
	type entry struct {
		p    *lfPrefix
		kind byte
	}
	entries := []entry{{pa, 'A'}, {pb, 'B'}}
	for i := range entries {
		if entries[i].kind == 'A' {
			x := (*lfExtA)(unsafe.Pointer(entries[i].p))
			ck(len(x.data) == 5 && x.data[4] == byte(4*17+int(seed)), "A suffix via slice")
			ck(x.tail != nil && x.tail.v == 107+seed, "A tail via slice")
			ck(x.mark == 0x1122334455667788^uint64(seed), "A mark via slice")
		} else {
			x := (*lfExtB)(unsafe.Pointer(entries[i].p))
			ck(x.table[1] == 22 && x.table[2] == 33, "B table via slice")
			ck(x.tailB != nil && x.tailB.v == 108-seed, "B tail via slice")
		}
	}

	// 6) 丢弃完整类型变量，只留 []*prefix；GC 时仍需扫描两种后缀指针。
	tailA := ea.tail
	dataA := ea.data
	tableB := &eb.table
	tailB := eb.tailB
	ea = nil
	eb = nil
	pa = nil
	pb = nil
	gcStress()

	for i := range entries {
		p := entries[i].p
		if entries[i].kind == 'A' {
			checkLFPrefix(p, 7, "renamed-a", 900+seed, "new-a", codeA, "A after GC")
			x := (*lfExtA)(unsafe.Pointer(p))
			ck(x.tail != nil && x.tail.v == tailA.v && x.tail.tag == tailA.tag, "A tail after GC")
			ck(x.mark == 0x1122334455667788^uint64(seed), "A mark after GC")
			for k := range x.data {
				ck(x.data[k] == dataA[k], "A data after GC k="+strconv.Itoa(k))
			}
		} else {
			checkLFPrefix(p, 8, "renamed-b", 901-seed, "new-b", codeB, "B after GC")
			x := (*lfExtB)(unsafe.Pointer(p))
			ck(x.tailB != nil && x.tailB.v == tailB.v && x.tailB.tag == tailB.tag, "B tail after GC")
			for k := range x.table {
				ck(x.table[k] == tableB[k], "B table after GC k="+strconv.Itoa(k))
			}
		}
	}
	ck(tailA.v == 107+seed && tailB.v == 108-seed, "kept leaf pointers after GC")

	// 7) GC 后继续通过 *prefix 写前缀，再验证后缀未被破坏。
	entries[0].p.name = "post-gc-a"
	entries[1].p.code = codeB + 1
	checkLFPrefix(entries[0].p, 7, "post-gc-a", 900+seed, "new-a", codeA, "A post-GC write")
	ck((*lfExtA)(unsafe.Pointer(entries[0].p)).tail.v == tailA.v, "A tail intact after post-GC write")
	ck((*lfExtB)(unsafe.Pointer(entries[1].p)).table[3] == 44, "B table intact after post-GC write")

	// 8) 只保留尾部指针，丢弃前缀视图。
	entries = nil
	gcStress()
	ck(tailA != nil && tailA.v == 107+seed && tailA.tag == "ta-7", "lone A tail after GC")
	ck(tailB != nil && tailB.v == 108-seed && tailB.tag == "tb-8", "lone B tail after GC")

	finish("l07_layout_prefix_cross_type")
}
