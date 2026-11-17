// P1-17 (报告风险 A13/C16): unsafe.Pointer / uintptr / atomic 指针。
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

var (
	failed int32
	gcSink [][]byte
	gUP    unsafe.Pointer
	gAP    unsafe.Pointer
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
func setGlobalUP(p unsafe.Pointer) { gUP = p }

//go:noinline
func getGlobalUP() *node { return (*node)(gUP) }

//go:noinline
func setAtomicUP(p unsafe.Pointer) { atomic.StorePointer(&gAP, p) }

//go:noinline
func getAtomicUP() *node { return (*node)(atomic.LoadPointer(&gAP)) }

type box struct {
	up  unsafe.Pointer
	pad int64
}

//go:noinline
func setBox(b *box, p unsafe.Pointer) { b.up = p }

func main() {
	for round := 0; round < 10; round++ {
		p := &node{v: 5, name: "unsafe"}
		q := (*node)(unsafe.Pointer(uintptr(unsafe.Pointer(p))))
		gcStress()
		ck(q.v == 5 && q.name == "unsafe", "uintptr roundtrip across GC")

		s := []*node{{v: 10}, {v: 20}, {v: 30}}
		slot := (**node)(unsafe.Pointer(&s[1]))
		gcStress()
		ck((*slot).v == 20, "pointer to slice element slot across GC")

		setGlobalUP(unsafe.Pointer(&node{v: 77}))
		gcStress()
		ck(getGlobalUP().v == 77, "global unsafe.Pointer")

		setAtomicUP(unsafe.Pointer(&node{v: 88}))
		gcStress()
		ck(getAtomicUP().v == 88, "atomic pointer")

		b := &box{pad: 1}
		setBox(b, unsafe.Pointer(&node{v: 99}))
		gcStress()
		ck((*node)(b.up).v == 99, "unsafe.Pointer struct field")
	}
	ck(unsafe.Sizeof(node{}) > 0, "Sizeof")
	ck(unsafe.Offsetof(struct {
		a int64
		b int64
	}{}.b) == 8, "Offsetof")
	finish("p1_17_unsafe_uintptr")
}
