// L07: 只读子串 header：从父串 {data,len} 头派生子串/孙串 header，
// 手工指定 data 偏移与 len，父串变量丢弃后仅由子串头保活，跨 GC 校验内容与哈希。
package main

import (
	"fmt"
	"os"
	"runtime"
	"strconv"
	"strings"
	"sync/atomic"
	"unsafe"
)

type strHeader struct {
	data unsafe.Pointer
	len  int
}

type subView struct {
	child string
	grand string
	tail  string
	wantC string
	wantG string
	sumC  uint32
	sumG  uint32
}

var (
	failed  int32
	gcSink  [][]byte
	seed    int64
	keepHdr strHeader
	keepSub []subView
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

func headerOf(s string) strHeader {
	return *(*strHeader)(unsafe.Pointer(&s))
}

func stringOf(h strHeader) string {
	return *(*string)(unsafe.Pointer(&h))
}

func fnv32(s string) uint32 {
	h := uint32(2166136261)
	for i := 0; i < len(s); i++ {
		h ^= uint32(s[i])
		h *= 16777619
	}
	return h
}

// makeParent 构造足够长的堆 string，内容含 ASCII 与多字节 UTF-8。
//
//go:noinline
func makeParent(s int64) string {
	b := make([]byte, 0, 160)
	b = append(b, "prefix-"...)
	b = strconv.AppendInt(b, s%1000, 10)
	b = append(b, "-世界-alpha-beta-gamma-Ω-"...)
	for i := 0; i < 16; i++ {
		b = append(b, 'A'+byte((i+int(s))%26))
	}
	b = append(b, "-tail-suffix-"...)
	b = strconv.AppendInt(b, s%97, 10)
	b = append(b, "-end-of-parent"...)
	return string(b)
}

// derive 从父串 header 派生子串与孙串，均只保存 {data,len} 头。
//
//go:noinline
func derive(parent string, s int64) (subView, strHeader) {
	ph := headerOf(parent)
	ck(ph.len == len(parent), "parent header len")
	ck(ph.data == unsafe.Pointer(unsafe.StringData(parent)), "parent header data")

	o1 := 8 + int(s%6)
	l1 := 28
	o2 := o1 + 9
	l2 := 16
	o3 := o1 + l1 - 5 // 与 child 尾部重叠
	l3 := 12
	ck(o1+l1 <= len(parent), "child bounds")

	ch := strHeader{data: unsafe.Add(ph.data, o1), len: l1}
	gh := strHeader{data: unsafe.Add(unsafe.Add(ph.data, o2), 4), len: l2}
	th := strHeader{data: unsafe.Add(ph.data, o3), len: l3}
	child := stringOf(ch)
	grand := stringOf(gh)
	tail := stringOf(th)

	// 与 unsafe.String 直接构造的视图指向同一地址。
	direct := unsafe.String((*byte)(unsafe.Add(ph.data, o1)), l1)
	ck(child == direct, "manual header string equals unsafe.String")
	ck(unsafe.StringData(child) == unsafe.StringData(direct), "manual header data alias direct")
	ck(unsafe.StringData(child) == (*byte)(unsafe.Add(unsafe.Pointer(unsafe.StringData(parent)), o1)),
		"child data = parent data + o1")
	ck(unsafe.StringData(grand) == (*byte)(unsafe.Add(unsafe.Pointer(unsafe.StringData(parent)), o2+4)),
		"grand data = parent data + o2 + 4")

	wantC := strings.Clone(parent[o1 : o1+l1])
	wantG := strings.Clone(parent[o2+4 : o2+4+l2])
	ck(child == wantC, "child content")
	ck(grand == wantG, "grand content")
	ck(tail == parent[o3:o3+l3], "overlapping tail content")

	// 重叠子串共享字节：child 的后 5 字节与 tail 的前 5 字节相同。
	ov := o2 + 4 - o1
	ck(ov > 0 && ov < l1, "overlap offset inside child")
	ck(child[ov] == grand[0], "overlapping first byte")
	ck(child[l1-1] == parent[o1+l1-1], "child last byte")
	ck(tail[:5] == child[l1-5:], "overlapping tail shares child suffix")

	// 多字节 UTF-8 子串：长度按字节计。
	idx := strings.Index(parent, "世界")
	ck(idx >= 0, "found UTF-8 marker")
	if idx >= 0 && idx+6 <= len(parent) {
		uh := strHeader{data: unsafe.Add(ph.data, idx), len: 6}
		us := stringOf(uh)
		ck(us == parent[idx:idx+6], "UTF-8 substring bytes")
		ck(len(us) == 6, "UTF-8 substring byte length")
	}

	// 尾部零长 header。
	eh := strHeader{data: unsafe.Add(ph.data, ph.len), len: 0}
	empty := stringOf(eh)
	ck(empty == "" && len(empty) == 0, "zero-length tail header")

	sv := subView{
		child: child, grand: grand, tail: tail,
		wantC: wantC, wantG: wantG,
		sumC: fnv32(child), sumG: fnv32(grand),
	}
	// 返回一个 header 作为全局根，父串本身不返回。
	gkeep := strHeader{data: unsafe.Add(ph.data, o1), len: l1}
	return sv, gkeep
}

func verifySub(v *subView, stage string) {
	ck(v.child == v.wantC, stage+": child content")
	ck(v.grand == v.wantG, stage+": grand content")
	ck(fnv32(v.child) == v.sumC, stage+": child hash")
	ck(fnv32(v.grand) == v.sumG, stage+": grand hash")
	ck(len(v.tail) > 0 && v.tail[:5] == v.child[len(v.child)-5:], stage+": overlapping tail")
	ck(unsafe.StringData(v.child) != nil && unsafe.StringData(v.grand) != nil, stage+": data pointers")
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
	rounds := 24
	if s := os.Getenv("TORTURE_ROUNDS"); s != "" {
		if v, err := strconv.Atoi(s); err == nil && v > 0 {
			rounds = v
		}
	}

	for r := 0; r < rounds; r++ {
		parent := makeParent(seed + int64(r))
		sv, h := derive(parent, seed+int64(r))
		// 只保留子串视图；最后一轮额外保留全局 header 作为独立根。
		if r == rounds-1 {
			keepHdr = h
		}
		keepSub = append(keepSub, sv)
		parent = ""
		gcStress()
		verifySub(&sv, "round "+strconv.Itoa(r))
	}

	// 所有父串都不可达后，保留的子串/header 仍完整。
	gcStress()
	for i := range keepSub {
		verifySub(&keepSub[i], "retained i="+strconv.Itoa(i))
	}
	hg := stringOf(keepHdr)
	ck(len(hg) == keepHdr.len, "final global header len")
	ck(hg == keepSub[len(keepSub)-1].child, "global header reconstructs last child")
	ck(fnv32(hg) == keepSub[len(keepSub)-1].sumC, "final global header hash")

	// 只读：反复读取不改变内容。
	before := fnv32(keepSub[len(keepSub)-1].child)
	for i := 0; i < 256; i++ {
		_ = fnv32(keepSub[len(keepSub)-1].child)
	}
	ck(fnv32(keepSub[len(keepSub)-1].child) == before, "read-only content stable")

	runtime.KeepAlive(keepHdr)
	runtime.KeepAlive(keepSub)
	finish("l07_substring_header_ro")
}
