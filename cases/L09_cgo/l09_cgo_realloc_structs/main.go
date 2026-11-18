// L09: C realloc 维护结构体数组：rs_rec_t{id,flags,len,pad,name[16]} 从
// realloc(NULL,n) 开始，依次增长/同尺寸/收缩/再增长到 1000 条，每次新扩展
// 区由 C 填充后 Go 才读取；Go 校验布局、max_align_t 对齐、前缀保留（含
// 收缩前哈希）、C/Go 双侧 FNV 字节哈希，并直接从 C 内存改一条 flags 由 C
// 读回。数组全程在 C 堆，显式 GC 不影响内容；C 不保留 Go 指针。
package main

/*
#include <stdint.h>
#include <stddef.h>
#include <stdlib.h>
#include "rs.h"
*/
import "C"

import (
	"fmt"
	"os"
	"runtime"
	"sync/atomic"
	"unsafe"
)

const caseName = "l09_cgo_realloc_structs"

var (
	failed int32
	gcSink [][]byte
)

func ck(ok bool, msg string) {
	if !ok {
		atomic.AddInt32(&failed, 1)
		fmt.Println("FAIL:", msg)
	}
}

func gcStress() {
	for i := 0; i < 16; i++ {
		b := make([]byte, 4096)
		b[0] = byte(i)
		gcSink = append(gcSink[:0], b)
	}
	gcSink = gcSink[:0]
	runtime.GC()
	runtime.GC()
}

func finish(name string) {
	if n := atomic.LoadInt32(&failed); n != 0 {
		fmt.Printf("FAILED %s (%d failed checks)\n", name, n)
		os.Exit(1)
	}
	fmt.Println("ALL PASS " + name)
}

func fnv64(b []byte) uint64 {
	h := uint64(1469598103934665603)
	for _, c := range b {
		h ^= uint64(c)
		h *= 1099511628211
	}
	return h
}

func wantName(i int) string { return fmt.Sprintf("rec-%03d", i%1000) }

func checkGo(tag string, p *C.rs_rec_t, n int) {
	recs := unsafe.Slice(p, n)
	for i := 0; i < n; i++ {
		r := recs[i]
		ck(int32(r.id) == int32(i), fmt.Sprintf("%s: id[%d]", tag, i))
		ck(int32(r.flags) == int32((i*37+11)&0xff), fmt.Sprintf("%s: flags[%d]", tag, i))
		ck(int32(r.len) == int32(i%15), fmt.Sprintf("%s: len[%d]", tag, i))
		ck(int32(r.pad) == 0, fmt.Sprintf("%s: pad[%d]", tag, i))
		var buf [16]byte
		nb := int(C.rs_name_at(p, C.int(i), (*C.char)(unsafe.Pointer(&buf[0])), 16))
		ck(nb == len(wantName(i)) && string(buf[:nb]) == wantName(i), fmt.Sprintf("%s: name[%d]", tag, i))
	}
}

func main() {
	alignMax := uintptr(C.rs_align_max())
	ck(uintptr(C.rs_size()) == unsafe.Sizeof(C.rs_rec_t{}), "sizeof rs_rec_t")
	ck(uintptr(C.rs_align()) == unsafe.Alignof(C.rs_rec_t{}), "alignof rs_rec_t")
	ck(uintptr(C.rs_off_name()) == unsafe.Offsetof(C.rs_rec_t{}.name), "offsetof name")
	ck(uintptr(C.rs_off_len()) == unsafe.Offsetof(C.rs_rec_t{}.len), "offsetof len")
	ck(alignMax >= 8, "alignof(max_align_t) >= 8")

	steps := []struct {
		n   int
		tag string
	}{
		{4, "initial NULL realloc"},
		{9, "grow 4->9"},
		{9, "same size 9"},
		{3, "shrink 9->3"},
		{37, "grow 3->37"},
		{257, "grow 37->257"},
		{1, "shrink 257->1"},
		{1000, "grow 1->1000"},
	}

	var p *C.rs_rec_t
	oldN := 0
	var saved3 uint64
	var hasSaved3 bool
	for _, st := range steps {
		var rerr C.int
		q := C.rs_resize(p, C.int(st.n), &rerr)
		if q == nil {
			ck(false, fmt.Sprintf("%s: realloc NULL err=%d", st.tag, int32(rerr)))
			break
		}
		p = q
		ck(uintptr(unsafe.Pointer(p))%alignMax == 0, st.tag+": max_align_t alignment")
		if st.n > oldN {
			C.rs_fill(p, C.int(oldN), C.int(st.n))
		}
		oldN = st.n

		ck(int(C.rs_verify(p, C.int(st.n))) == -1, st.tag+": C verify")
		checkGo(st.tag, p, st.n)

		raw := unsafe.Slice((*byte)(unsafe.Pointer(p)), st.n*int(unsafe.Sizeof(C.rs_rec_t{})))
		gh := fnv64(raw)
		ch := uint64(C.rs_hash(p, C.int(st.n)))
		ck(gh == ch, fmt.Sprintf("%s: FNV C=%x Go=%x", st.tag, ch, gh))

		if st.tag == "shrink 9->3" {
			saved3 = ch
			hasSaved3 = true
		}
		if hasSaved3 && st.n >= 3 {
			ck(uint64(C.rs_hash(p, 3)) == saved3, st.tag+": first 3 records preserved")
		}
		gcStress()
		ck(int(C.rs_verify(p, C.int(st.n))) == -1, st.tag+": C verify after GC")
		ck(uint64(C.rs_hash(p, C.int(st.n))) == ch, st.tag+": hash stable across GC")
	}

	if p != nil {
		// Go 直接改 C 内存中的一条记录，C 侧读回并识别出坏记录。
		unsafe.Slice(p, 1000)[500].flags = 0x5a5a
		ck(int(C.rs_flags_at(p, 500)) == 0x5a5a, "Go write visible to C")
		ck(int(C.rs_verify(p, 1000)) == 500, "C verify detects Go mutation at 500")
		C.rs_fill(p, 500, 501)
		ck(int(C.rs_verify(p, 1000)) == -1, "C refill repairs record 500")
		gcStress()
		ck(int(C.rs_verify(p, 1000)) == -1, "verify after repair+GC")

		C.free(unsafe.Pointer(p))
	}
	finish(caseName)
}
