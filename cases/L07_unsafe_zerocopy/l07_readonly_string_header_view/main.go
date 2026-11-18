// L07: 只读 string header 视图：data/len 拆解、子串偏移、header 跨 GC 保活、只读字节视图。
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

//go:noinline
func headerOf(s string) strHeader {
	return *(*strHeader)(unsafe.Pointer(&s))
}

// bytesOf 返回只读字节视图；调用方不得写入。
func bytesOf(h strHeader) []byte {
	if h.len == 0 {
		return nil
	}
	return unsafe.Slice((*byte)(h.data), h.len)
}

func fnv1a(b []byte) uint32 {
	var h uint32 = 2166136261
	for _, c := range b {
		h ^= uint32(c)
		h *= 16777619
	}
	return h
}

//go:noinline
func heapString(tag int64, n int) string {
	b := make([]byte, n)
	for i := range b {
		b[i] = byte('a' + (tag+int64(i))%26)
	}
	return string(b)
}

func contentOf(tag int64, n int) []byte {
	b := make([]byte, n)
	for i := range b {
		b[i] = byte('a' + (tag+int64(i))%26)
	}
	return b
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

	// 1) 字面量 string 的 header：data 与 StringData 一致，len 为字节长度。
	base := "0123456789abcdefghijklmnopqrstuvwxyz"
	hb := headerOf(base)
	ck(hb.len == len(base), "literal header len")
	ck(hb.data == unsafe.Pointer(unsafe.StringData(base)), "literal header data")
	ck(string(bytesOf(hb)) == base, "literal header bytes")

	// 2) 每个子串 header 的 data = base.data + i，len 递减。
	for i := 0; i < len(base); i++ {
		sub := base[i:]
		hs := headerOf(sub)
		ck(hs.len == len(sub), "substring len i="+strconv.Itoa(i))
		ck(hs.data == unsafe.Add(hb.data, i), "substring data offset i="+strconv.Itoa(i))
		ck(string(bytesOf(hs)) == sub, "substring bytes i="+strconv.Itoa(i))
	}

	// 3) 空串 header：len=0，不做任何 data 解引用。
	he := headerOf("")
	ck(he.len == 0, "empty header len")
	ck(len(bytesOf(he)) == 0, "empty header bytes")

	// 4) 堆 string 转 header，丢弃原 string 后 header 独立保活其数据。
	n := 24 + int(seed%24)
	s := heapString(seed, n)
	h := headerOf(s)
	want := contentOf(seed, n)
	ck(h.len == n, "heap header len")
	ck(fnv1a(bytesOf(h)) == fnv1a(want), "heap header checksum")
	s = ""
	gcStress()
	ck(h.len == n, "heap header len after parent drop")
	ck(fnv1a(bytesOf(h)) == fnv1a(want), "heap header survives parent drop")
	// 从 header 重建零拷贝 string，跨 GC 使用。
	res := unsafe.String((*byte)(h.data), h.len)
	gcStress()
	ck(res == string(want), "rebuilt string from header after GC")
	ck(unsafe.Pointer(unsafe.StringData(res)) == h.data, "rebuilt string same data")

	// 5) 结构体 string 字段的 header：只保留 header，结构体本身可回收。
	type rec struct {
		name string
		tags []string
		note string
	}
	r := &rec{name: "rec-" + strconv.Itoa(int(seed)), tags: []string{"a", "b"}, note: strings.Repeat("z", 8)}
	rh := headerOf(r.name)
	wantName := "rec-" + strconv.Itoa(int(seed))
	r = nil
	gcStress()
	ck(string(bytesOf(rh)) == wantName, "struct field header after owner drop")

	// 6) 含 NUL 与 UTF-8 的字节序列：header 按字节计长，只读视图逐字节一致。
	raw := []byte{'a', 0, 0xE4, 0xB8, 0x96, 'z'}
	zs := unsafe.String(&raw[0], len(raw))
	zh := headerOf(zs)
	ck(zh.len == 6, "NUL/UTF8 header len")
	got := bytesOf(zh)
	ck(len(got) == 6 && got[0] == 'a' && got[1] == 0 && got[2] == 0xE4 &&
		got[3] == 0xB8 && got[4] == 0x96 && got[5] == 'z', "NUL/UTF8 header bytes")
	raw = nil
	zs = ""
	gcStress()
	got = bytesOf(zh)
	ck(len(got) == 6 && got[1] == 0 && got[2] == 0xE4 && got[5] == 'z', "NUL/UTF8 after GC")

	// 7) 批量 header：只保留 header 切片，源 string 全部离开作用域。
	var headers []strHeader
	var checksums []uint32
	for i := 0; i < 64; i++ {
		m := 16 + int((seed+int64(i))%32)
		tag := int64(i%7) + 1
		hs := headerOf(heapString(tag, m))
		headers = append(headers, hs)
		checksums = append(checksums, fnv1a(contentOf(tag, m)))
		if i%16 == 15 {
			gcStress()
		}
	}
	gcStress()
	ck(len(headers) == 64, "header batch count")
	for i := range headers {
		ck(headers[i].len > 0, "batch header len i="+strconv.Itoa(i))
		ck(fnv1a(bytesOf(headers[i])) == checksums[i], "batch header checksum i="+strconv.Itoa(i))
	}

	// 8) 只读性：多次读取后内容与校验和不变，且不写入任何 string 数据。
	first := fnv1a(bytesOf(h))
	for i := 0; i < 32; i++ {
		ck(fnv1a(bytesOf(h)) == first, "read-only repeated checksum")
	}
	ck(res == string(want), "read-only rebuilt string stable")

	runtime.KeepAlive(headers)
	runtime.KeepAlive(checksums)
	runtime.KeepAlive(h)
	runtime.KeepAlive(res)
	runtime.KeepAlive(rh)
	runtime.KeepAlive(zh)
	finish("l07_readonly_string_header_view")
}
