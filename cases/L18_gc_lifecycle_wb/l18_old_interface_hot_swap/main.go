// L18: 老 holder 的 interface 字段热替换为不同类型/新分配对象；
// setter noinline，返回后年轻对象只被老 interface 字段（及老 interface 数组）引用，再 GC 校验。
package main

import (
	"fmt"
	"os"
	"runtime"
	"sync/atomic"
)

type node struct {
	v    int64
	name string
}

type probe interface {
	kind() int
	value() int64
	valid() bool
}

type alpha struct {
	v   int64
	tag string
}

func (a *alpha) kind() int    { return 1 }
func (a *alpha) value() int64 { return a.v }
func (a *alpha) valid() bool {
	return a != nil && a.tag == "alpha" && a.v%16 == 7
}

type beta struct {
	v    int64
	tag  string
	data []byte
}

func (b *beta) kind() int    { return 2 }
func (b *beta) value() int64 { return b.v }
func (b *beta) valid() bool {
	if b == nil || b.tag != "beta" || b.v%16 != 7 || len(b.data) != 16 {
		return false
	}
	for i := range b.data {
		if b.data[i] != byte((int(b.v)+i*3)%251) {
			return false
		}
	}
	return true
}

type gamma struct {
	v     int64
	child *node
}

func (g *gamma) kind() int    { return 3 }
func (g *gamma) value() int64 { return g.v }
func (g *gamma) valid() bool {
	return g != nil && g.child != nil && g.child.v == g.v+1 && g.child.name == "gc"
}

type holder struct {
	cur  probe
	hist [6]probe
	raw  interface{}
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
func promoteHolder() *holder {
	h := &holder{}
	h.cur = &alpha{v: 7, tag: "alpha"}
	for i := range h.hist {
		h.hist[i] = &alpha{v: 7, tag: "alpha"}
	}
	h.raw = &alpha{v: 7, tag: "alpha"}
	gcStress()
	gcStress()
	return h
}

//go:noinline
func setAlpha(h *holder, v int64) {
	h.cur = &alpha{v: v, tag: "alpha"}
}

//go:noinline
func setBeta(h *holder, v int64) {
	b := &beta{v: v, tag: "beta", data: make([]byte, 16)}
	for i := range b.data {
		b.data[i] = byte((int(v) + i*3) % 251)
	}
	h.cur = b
}

//go:noinline
func setGamma(h *holder, v int64) {
	h.cur = &gamma{v: v, child: &node{v: v + 1, name: "gc"}}
}

// setRaw 只写老 interface 字段，返回后新对象仅由 h.raw 引用。
//
//go:noinline
func setRaw(h *holder, v int64, kind int) {
	switch kind {
	case 1:
		h.raw = &alpha{v: v, tag: "alpha"}
	case 2:
		b := &beta{v: v, tag: "beta", data: make([]byte, 16)}
		for i := range b.data {
			b.data[i] = byte((int(v) + i*3) % 251)
		}
		h.raw = b
	default:
		h.raw = &gamma{v: v, child: &node{v: v + 1, name: "gc"}}
	}
}

//go:noinline
func pushHist(h *holder, i int) {
	h.hist[i%len(h.hist)] = h.cur
}

func checkProbe(p probe, wantKind int, wantV int64, where string) {
	if p == nil {
		ck(false, where+": nil interface")
		return
	}
	ck(p.kind() == wantKind, where+": dynamic type lost")
	ck(p.value() == wantV, where+": value corrupted")
	ck(p.valid(), where+": payload corrupted")
}

func checkRaw(raw interface{}, wantKind int, wantV int64) {
	var p probe
	switch x := raw.(type) {
	case *alpha:
		p = x
	case *beta:
		p = x
	case *gamma:
		p = x
	default:
		ck(false, "raw interface dynamic type lost")
		return
	}
	checkProbe(p, wantKind, wantV, "raw")
}

func verifyHist(h *holder) {
	for i := range h.hist {
		p := h.hist[i]
		if p == nil {
			ck(false, "hist interface slot became nil")
			continue
		}
		ck(p.kind() >= 1 && p.kind() <= 3, "hist dynamic type corrupted")
		ck(p.valid(), "hist payload corrupted")
	}
}

func main() {
	h := promoteHolder()
	for i := 0; i < 30; i++ {
		v := int64(i*16 + 7)
		wantKind := i%3 + 1
		switch wantKind {
		case 1:
			setAlpha(h, v)
		case 2:
			setBeta(h, v)
		default:
			setGamma(h, v)
		}
		setRaw(h, v, wantKind)
		gcStress()
		checkProbe(h.cur, wantKind, v, "cur")
		checkRaw(h.raw, wantKind, v)

		pushHist(h, i)
		gcStress()
		verifyHist(h)
	}
	runtime.KeepAlive(h)
	finish("l18_old_interface_hot_swap")
}
