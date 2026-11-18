// L07: string↔[]byte 零拷贝/复制语义在 GC 后仍然正确。
package main

import (
	"fmt"
	"os"
	"reflect"
	"runtime"
	"sync/atomic"
	"unsafe"
)

type node struct {
	v    int64
	name string
}

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

// 零拷贝：返回的 string 与 b 共享底层数组。
//
//go:noinline
func bytesToString(b []byte) string { return *(*string)(unsafe.Pointer(&b)) }

// 零拷贝：返回的 []byte 与 s 共享底层数组；调用方必须保证 s 在使用期内存活。
//
//go:noinline
func stringToBytes(s string) []byte {
	var b []byte
	bh := (*reflect.SliceHeader)(unsafe.Pointer(&b))
	sh := (*reflect.StringHeader)(unsafe.Pointer(&s))
	bh.Data = sh.Data
	bh.Len = sh.Len
	bh.Cap = sh.Len
	runtime.KeepAlive(s)
	return b
}

func main() {
	// 堆上构造的 []byte，转成零拷贝 string 后 GC。
	src := []byte("persistent-payload-0123456789")
	s := bytesToString(src)
	gcStress()
	ck(s == "persistent-payload-0123456789", "zero-copy bytes->string after GC")
	ck(len(src) == 29 && src[0] == 'p', "source slice after GC")

	// string(src) 是复制语义：改 src 不影响副本。
	cp := string(src)
	src[0] = 'P'
	gcStress()
	ck(cp == "persistent-payload-0123456789", "copied string unaffected by source mutation")
	ck(s == "Persistent-payload-0123456789", "zero-copy string aliases source")
	ck(src[0] == 'P', "source mutation visible")

	// 堆上 string -> 零拷贝 []byte，GC 后仍可读。
	hs := string([]byte("heap-built-string-content"))
	b := stringToBytes(hs)
	gcStress()
	ck(string(b) == "heap-built-string-content", "zero-copy string->bytes after GC")
	ck(len(b) == 25 && b[0] == 'h' && b[24] == 't', "byte view shape")
	runtime.KeepAlive(hs)

	// 结构体字段里的 string 与 []byte 混合，反复 GC。
	type rec struct {
		name string
		data []byte
		p    *node
	}
	r := &rec{name: "record-name", data: []byte("record-data"), p: &node{v: 7, name: "n"}}
	bs := bytesToString(r.data)
	nb := stringToBytes(r.name)
	gcStress()
	ck(bs == "record-data" && string(nb) == "record-name", "struct field zero-copy views after GC")
	ck(r.p.v == 7, "struct pointer after GC")
	runtime.KeepAlive(r)
	runtime.KeepAlive(bs)

	finish("l07_string_bytes_zero_copy")
}
