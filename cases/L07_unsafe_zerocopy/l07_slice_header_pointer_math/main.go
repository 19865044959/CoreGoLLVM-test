// L07: 用 unsafe 从结构体字段/数组元素做指针算术与 slice 视图，GC 后仍有效。
package main

import (
	"fmt"
	"os"
	"runtime"
	"sync/atomic"
	"unsafe"
)

type node struct {
	v    int64
	name string
}

type holder struct {
	pad int64
	arr [8]*node
	tag string
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

//go:noinline
func makeHolder() *holder {
	h := &holder{tag: "holder-tag"}
	for i := range h.arr {
		h.arr[i] = &node{v: int64(i), name: "elem"}
	}
	return h
}

func main() {
	h := makeHolder()

	// 指向结构体字段数组首元素的类型化指针 + 全数组视图。
	pa := (*[8]*node)(unsafe.Pointer(&h.arr[0]))
	view := pa[:]
	gcStress()
	for i := range view {
		if view[i] == nil || view[i].v != int64(i) || view[i].name != "elem" {
			ck(false, "pointer-arithmetic array view corrupted")
			break
		}
	}
	ck(len(view) == 8, "array view length")

	// uintptr 单表达式算术：取 arr[3] 的槽位并解引用。
	slot3 := (**node)(unsafe.Pointer(uintptr(unsafe.Pointer(&h.arr[0])) + 3*unsafe.Sizeof(h.arr[0])))
	gcStress()
	ck((*slot3) != nil && (*slot3).v == 3, "uintptr offset into array")

	// 取字符串字段的头部指针，GC 后读取。
	tagPtr := (*string)(unsafe.Pointer(uintptr(unsafe.Pointer(&h.tag))))
	gcStress()
	ck(*tagPtr == "holder-tag", "pointer to string field after GC")

	// 数组元素槽位指针数组（**node）在 GC 后仍指向有效元素。
	slots := make([]**node, 0, 8)
	for i := range h.arr {
		slots = append(slots, &h.arr[i])
	}
	gcStress()
	for i, sp := range slots {
		if sp == nil || (*sp) == nil || (*sp).v != int64(i) {
			ck(false, "slot pointer array corrupted")
			break
		}
	}

	// 结构体内存布局换算：arr 相对 h 的偏移与实际取址一致。
	off := uintptr(unsafe.Pointer(&h.arr)) - uintptr(unsafe.Pointer(h))
	ck(off == unsafe.Offsetof(holder{}.arr), "array field offset")

	runtime.KeepAlive(h)
	runtime.KeepAlive(view)
	finish("l07_slice_header_pointer_math")
}
