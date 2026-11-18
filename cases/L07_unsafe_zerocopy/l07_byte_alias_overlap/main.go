// L07: 字节别名检测：用 data 指针区间判断两个 []byte 是否共享内存，
// 与子切片/追加/复制/string 视图的实际写可见性逐一对照，跨 GC 保持判断。
package main

import (
	"fmt"
	"os"
	"runtime"
	"strconv"
	"sync/atomic"
	"unsafe"
)

type strHeader struct {
	data unsafe.Pointer
	len  int
}

var (
	failed   int32
	gcSink   [][]byte
	seed     int64
	keepHdr  strHeader
	keepStr  string
	keepByte []byte
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

// aliases 判断两个非空 []byte 的字节区间是否相交；uintptr 只在单个返回表达式中出现。
//
//go:noinline
func aliases(a, b []byte) bool {
	return len(a) > 0 && len(b) > 0 &&
		uintptr(unsafe.Pointer(unsafe.SliceData(a))) < uintptr(unsafe.Pointer(unsafe.SliceData(b)))+uintptr(len(b)) &&
		uintptr(unsafe.Pointer(unsafe.SliceData(b))) < uintptr(unsafe.Pointer(unsafe.SliceData(a)))+uintptr(len(a))
}

// bytesOfString 构造只读字节视图，不写入。
func bytesOfString(s string) []byte {
	if len(s) == 0 {
		return nil
	}
	return unsafe.Slice(unsafe.StringData(s), len(s))
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

	size := 128 + int(seed%32)
	buf := make([]byte, size)
	for i := range buf {
		buf[i] = byte(i*13 + int(seed))
	}
	base := 8 + int(seed%8)
	a := buf[base : base+32]
	b := buf[base+16 : base+64]
	c := buf[base+64 : base+96]

	// 1) 共享底层数组的子切片：相交/相邻/自身/空视图。
	ck(aliases(a, b), "overlapping sub-slices alias")
	ck(aliases(b, a), "alias detection symmetric")
	ck(!aliases(a, c), "disjoint sub-slices do not alias")
	ck(aliases(a, a), "self aliases")
	ck(!aliases(a, a[:0]), "empty slice never aliases")
	ck(!aliases(nil, a), "nil slice never aliases")
	ck(aliases(a, buf), "sub-slice aliases parent")

	// 2) 显式复制不共享内存；改写源不影响副本。
	cp := append([]byte(nil), a...)
	ck(!aliases(a, cp), "copy does not alias source")
	old := a[0]
	a[0] = old ^ 0x5a
	ck(cp[0] == old, "copy unaffected by source write")
	a[0] = old
	ck(cp[0] == old, "restored source matches copy")

	// 3) cap 内 append 复用底层数组，仍与源别名。
	s := make([]byte, 4, 16)
	s[0] = 1
	s2 := s[:12]
	ck(aliases(s, s2), "slices within cap alias")
	s3 := append(s, 9, 9, 9, 9)
	ck(len(s3) == 8 && cap(s3) == 16, "append within cap keeps capacity")
	ck(aliases(s, s3), "append within cap aliases source")
	ck(s2[4] == 9 && s2[7] == 9, "append write visible through sibling view")

	// 4) 满 cap append 重新分配，与原切片不再别名。
	u := make([]byte, 4, 4)
	u[0] = 7
	v := append(u, 8)
	ck(len(v) == 5, "reallocating append length")
	ck(!aliases(u, v), "reallocating append does not alias source")
	v[0] = 0x33
	ck(u[0] == 7, "reallocated append write isolated")

	// 5) string 零拷贝视图与源 []byte 别名；复制 string 不别名。
	sv := unsafe.String(unsafe.SliceData(buf[base:]), 32)
	sb := bytesOfString(sv)
	ck(aliases(sb, a), "string view aliases source bytes")
	ck(aliases(sb, buf), "string view aliases parent buffer")
	buf[base] ^= 0xff
	ck(sv[0] == buf[base], "string view observes source mutation")
	buf[base] ^= 0xff
	copied := string(sb)
	ck(!aliases(bytesOfString(copied), buf), "copied string does not alias")
	ck(copied == sv, "copied string content equal")

	// 6) 手工 {data,len} string 头与直接 unsafe.String 指向同一地址。
	h := strHeader{data: unsafe.Pointer(&buf[base]), len: 32}
	msv := *(*string)(unsafe.Pointer(&h))
	ck(msv == sv, "manual header string content")
	ck(aliases(bytesOfString(msv), a), "manual header string aliases source")
	ck(aliases(bytesOfString(msv), sb), "two string views alias")

	// 7) 丢弃 buf 变量，只保留视图与头；GC 后判断与可见性不变。
	keepHdr = h
	keepStr = msv
	keepByte = a[:24:24]
	buf = nil
	gcStress()
	ck(aliases(keepByte, a), "retained byte views alias after GC")
	ck(aliases(bytesOfString(keepStr), keepByte), "retained string/bytes alias after GC")
	ck(!aliases(keepByte, bytesOfString(copied)), "copy still disjoint after GC")
	ck(aliases(bytesOfString(keepStr), bytesOfString(sv)), "derived string views alias after GC")
	keepByte[0] ^= 0x11
	ck(keepStr[0] == keepByte[0], "mutation through byte view visible to string after GC")
	keepByte[0] ^= 0x11
	ck(keepStr[0] == keepByte[0], "restored value visible")

	// 8) 全局 header 作为根：重建 string 后内容与别名关系仍成立。
	hg := *(*string)(unsafe.Pointer(&keepHdr))
	ck(hg == keepStr, "global header reconstructs same string")
	ck(aliases(bytesOfString(hg), keepByte), "global header keeps source alive")

	// 9) 相邻但不相交的区间（边界相接）不算别名。
	left := keepByte[:8]
	right := keepByte[8:16]
	ck(!aliases(left, right), "adjacent ranges do not alias")

	runtime.KeepAlive(keepHdr)
	runtime.KeepAlive(keepStr)
	runtime.KeepAlive(keepByte)
	finish("l07_byte_alias_overlap")
}
