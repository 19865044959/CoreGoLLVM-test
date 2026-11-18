// L07: 内部指针（&arr[i]、slice 视图、结构体字段指针）单独持有堆对象时，GC 后对象仍存活。
package main

import (
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

type cell struct {
	id   int64
	tag  string
	link *node
}

type bigHolder struct {
	pad  int64
	junk []byte
	arr  [8]cell
	node *node
}

var (
	failed    int32
	gcSink    [][]byte
	keepCell  *cell
	keepView  []cell
	keepBytes []byte
	seed      int64
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

//go:noinline
func makeCells() *[32]cell {
	full := new([32]cell)
	for i := range full {
		full[i] = cell{
			id:   int64(i),
			tag:  "cell-" + strconv.Itoa(i),
			link: &node{v: int64(i * 3), name: "n-" + strconv.Itoa(i)},
		}
	}
	return full
}

//go:noinline
func deriveInterior(full *[32]cell, start, n int) (*cell, []cell) {
	ip := &full[start]
	view := unsafe.Slice(&full[start], n)
	return ip, view
}

//go:noinline
func makeBytes(s int64) *[4096]byte {
	b := new([4096]byte)
	for i := range b {
		b[i] = byte((int64(i)*31 + s) & 0xff)
	}
	return b
}

//go:noinline
func makeHolder() *bigHolder {
	h := &bigHolder{pad: 99, junk: make([]byte, 1024)}
	for i := range h.arr {
		h.arr[i] = cell{
			id:   int64(100 + i),
			tag:  "holder-cell-" + strconv.Itoa(i),
			link: &node{v: int64(1000 + i), name: "hn-" + strconv.Itoa(i)},
		}
	}
	h.node = &node{v: 7, name: "field-node"}
	return h
}

func main() {
	if s := os.Getenv("UAF_SEED"); s != "" {
		if v, err := strconv.ParseInt(s, 10, 64); err == nil {
			seed = v
		}
	}

	// 1) 指向堆数组元素的内部指针 + 从同一元素起始的 slice 视图。
	start := int(seed % 8)
	n := 12 + int(seed%5)
	full := makeCells()
	ip, view := deriveInterior(full, start, n)
	want := make([]cell, n)
	for i := range want {
		want[i] = full[start+i]
	}
	full = nil // 只保留内部指针与视图，不再有指向数组基址的变量
	keepCell, keepView = ip, view

	gcStress()

	ck(keepCell.id == want[0].id && keepCell.tag == want[0].tag, "interior *cell keeps element alive")
	if keepCell.link != nil {
		ck(keepCell.link.v == want[0].link.v && keepCell.link.name == want[0].link.name,
			"pointer field through interior pointer")
	} else {
		ck(false, "interior element link became nil")
	}
	ck(&keepView[0] == keepCell, "slice view starts at interior element")
	ck(len(keepView) == n, "slice view length")
	for i := 0; i < n; i++ {
		if keepView[i].id != want[i].id || keepView[i].tag != want[i].tag ||
			keepView[i].link == nil || keepView[i].link.v != want[i].link.v ||
			keepView[i].link.name != want[i].link.name {
			ck(false, fmt.Sprintf("interior view element %d corrupted after GC", i))
			break
		}
	}
	if mid := &keepView[n/2]; mid.link != nil {
		ck(mid.link.name == want[n/2].link.name, "middle element pointer after GC")
	} else {
		ck(false, "middle element link became nil")
	}

	// 2) 只保留 []byte 视图，堆数组本身没有其他引用。
	ba := makeBytes(seed)
	bv := unsafe.Slice(&ba[0], len(ba))
	ba = nil
	keepBytes = bv
	gcStress()
	for i := 0; i < len(keepBytes); i += 97 {
		if keepBytes[i] != byte((int64(i)*31+seed)&0xff) {
			ck(false, fmt.Sprintf("byte view element %d corrupted after GC", i))
			break
		}
	}
	keepBytes[3] ^= 0x5a
	ck(keepBytes[3] == (byte((int64(3)*31+seed)&0xff)^0x5a), "write through interior byte view")
	gcStress()
	ck(keepBytes[4095] == byte((int64(4095)*31+seed)&0xff), "tail byte after GC")

	// 3) 指向结构体字段数组元素的内部指针单独保活整个 holder。
	h := makeHolder()
	fp := &h.arr[7]
	h = nil
	keepCell = fp
	gcStress()
	ck(fp.id == 107 && fp.tag == "holder-cell-7", "field interior pointer keeps holder alive")
	if fp.link != nil {
		ck(fp.link.v == 1007 && fp.link.name == "hn-7", "holder element pointer field after GC")
	} else {
		ck(false, "holder element link became nil")
	}

	runtime.KeepAlive(keepView)
	runtime.KeepAlive(keepBytes)
	runtime.KeepAlive(keepCell)
	finish("l07_interior_pointer_liveness")
}
