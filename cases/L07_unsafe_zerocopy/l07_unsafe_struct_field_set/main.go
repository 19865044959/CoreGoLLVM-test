// L07: 通过 unsafe 指针写入 struct 的 string/ptr/slice 字段，GC 后字段正确且旧值安全。
package main

import (
	"bytes"
	"fmt"
	"os"
	"runtime"
	"strconv"
	"sync/atomic"
	"unsafe"
)

type node struct {
	v    int64
	name string
}

type meta struct {
	code  int64
	label string
	ref   *node
}

type record struct {
	Pad0 int64
	Name string
	Pad1 [16]byte
	Link *node
	Data []byte
	M    meta
	Pad2 int64
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
		for i := 0; i < 12; i++ {
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

func main() {
	if s := os.Getenv("UAF_SEED"); s != "" {
		if v, err := strconv.ParseInt(s, 10, 64); err == nil {
			seed = v
		}
	}
	pad := seed % 100

	var (
		offName  = unsafe.Offsetof(record{}.Name)
		offLink  = unsafe.Offsetof(record{}.Link)
		offData  = unsafe.Offsetof(record{}.Data)
		offMeta  = unsafe.Offsetof(record{}.M)
		offCode  = unsafe.Offsetof(meta{}.code)
		offLabel = unsafe.Offsetof(meta{}.label)
		offRef   = unsafe.Offsetof(meta{}.ref)
	)

	oldLink := &node{v: -1, name: "old-link"}
	oldRef := &node{v: -2, name: "old-ref"}
	r := &record{
		Pad0: 1,
		Name: "old-name",
		Link: oldLink,
		Data: []byte("old-data"),
		M:    meta{code: 5, label: "old-label", ref: oldRef},
	}

	name := fmt.Sprintf("name-%02d-payload", pad)
	labelSrc := []byte(fmt.Sprintf("label-%02d-zerocopy", pad))
	label := unsafe.String(unsafe.SliceData(labelSrc), len(labelSrc))
	n1 := &node{v: 1000 + seed, name: "unsafe-link"}
	n2 := &node{v: 2000 + seed, name: "unsafe-ref"}
	data := []byte(fmt.Sprintf("data-%02d", pad))

	// 每个字段写入都是一条 unsafe.Add 表达式，避免 uintptr 跨 GC 保存。
	*(*string)(unsafe.Add(unsafe.Pointer(r), offName)) = name
	*(*string)(unsafe.Add(unsafe.Pointer(r), offMeta+offLabel)) = label
	*(*int64)(unsafe.Add(unsafe.Pointer(r), offMeta+offCode)) = 4242 + seed
	*(**node)(unsafe.Add(unsafe.Pointer(r), offLink)) = n1
	*(**node)(unsafe.Add(unsafe.Pointer(r), offMeta+offRef)) = n2
	*(*[]byte)(unsafe.Add(unsafe.Pointer(r), offData)) = data

	gcStress()
	ck(r.Name == name, "string field written via unsafe")
	ck(r.M.label == string(labelSrc), "nested string field written via unsafe")
	ck(unsafe.StringData(r.M.label) == unsafe.SliceData(labelSrc), "label field aliases source bytes")
	ck(r.M.code == 4242+seed, "nested int64 field written via unsafe")
	ck(r.Link == n1 && r.Link.name == "unsafe-link", "pointer field written via unsafe")
	ck(r.M.ref == n2 && r.M.ref.v == 2000+seed, "nested pointer field written via unsafe")
	ck(bytes.Equal(r.Data, data), "slice field written via unsafe")
	ck(oldLink.name == "old-link" && oldRef.name == "old-ref", "old pointer targets survive GC")

	ck(uintptr(unsafe.Pointer(&r.Name))-uintptr(unsafe.Pointer(r)) == offName, "Name offset")
	ck(uintptr(unsafe.Pointer(&r.Link))-uintptr(unsafe.Pointer(r)) == offLink, "Link offset")
	ck(uintptr(unsafe.Pointer(&r.M.label))-uintptr(unsafe.Pointer(r)) == offMeta+offLabel, "meta.label offset")

	// 覆盖 string/ptr 字段：旧值由类型化局部变量保活，新值跨 GC 有效。
	oldName := r.Name
	oldN1 := r.Link
	*(*string)(unsafe.Add(unsafe.Pointer(r), offName)) = "rewritten-name"
	*(**node)(unsafe.Add(unsafe.Pointer(r), offLink)) = &node{v: 3000, name: "second-link"}
	gcStress()
	ck(r.Name == "rewritten-name", "overwritten string field")
	ck(oldName == name, "previous string value remains valid")
	ck(oldN1 == n1 && oldN1.name == "unsafe-link", "previous pointer value remains valid")
	ck(r.Link != nil && r.Link.v == 3000 && r.Link.name == "second-link", "overwritten pointer field")

	// 清零字段后 GC：等价于池化 reset 断开引用。
	*(*string)(unsafe.Add(unsafe.Pointer(r), offName)) = ""
	*(*string)(unsafe.Add(unsafe.Pointer(r), offMeta+offLabel)) = ""
	*(*[]byte)(unsafe.Add(unsafe.Pointer(r), offData)) = nil
	*(**node)(unsafe.Add(unsafe.Pointer(r), offLink)) = nil
	*(**node)(unsafe.Add(unsafe.Pointer(r), offMeta+offRef)) = nil
	gcStress()
	ck(r.Name == "" && r.M.label == "" && r.Data == nil && r.Link == nil && r.M.ref == nil,
		"unsafe field zeroing")
	ck(oldName == name, "zeroed string data still valid via copy")
	ck(n1.name == "unsafe-link" && n2.name == "unsafe-ref", "nodes survive field zeroing via typed refs")

	runtime.KeepAlive(r)
	runtime.KeepAlive(oldName)
	runtime.KeepAlive(n1)
	runtime.KeepAlive(n2)
	finish("l07_unsafe_struct_field_set")
}
