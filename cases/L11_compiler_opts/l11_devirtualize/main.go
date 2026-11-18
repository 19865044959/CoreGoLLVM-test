// L11: 直接调用与接口动态分派（潜在去虚化）结果必须一致；
// 接口内保存的指针对象、方法值在 GC 后仍能正确派发。
package main

import (
	"fmt"
	"os"
	"runtime"
	"sync/atomic"
)

var (
	failed int32
	gcSink [][]byte
)

type shape interface {
	Area() int64
	Name() string
}

type rect struct{ w, h int64 }

func (r *rect) Area() int64 { return r.w * r.h }
func (r *rect) Name() string { return "rect" }

type square struct{ s int64 }

func (s square) Area() int64 { return s.s * s.s }
func (s square) Name() string { return "square" }

type circle struct{ r int64 }

func (c *circle) Area() int64 { return 3 * c.r * c.r }
func (c *circle) Name() string { return "circle" }

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
		b[len(b)-1] = byte(255 - i)
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

func directRect(w, h int64) int64 {
	r := rect{w: w, h: h}
	return r.Area()
}

func ifaceRect(w, h int64) int64 {
	var sh shape = &rect{w: w, h: h}
	return sh.Area()
}

func expectRuntimePanic(name string, fn func()) {
	defer func() {
		r := recover()
		if r == nil {
			ck(false, name+": expected panic")
			return
		}
		_, ok := r.(runtime.Error)
		ck(ok, name+": panic is not runtime.Error")
	}()
	fn()
}

func main() {
	shapes := make([]shape, 0, 30)
	for i := 0; i < 30; i++ {
		v := int64(i + 1)
		switch i % 3 {
		case 0:
			shapes = append(shapes, &rect{w: v, h: 2})
		case 1:
			shapes = append(shapes, square{s: v})
		default:
			shapes = append(shapes, &circle{r: v})
		}
	}

	var want int64
	for i := range shapes {
		v := int64(i + 1)
		switch i % 3 {
		case 0:
			want += v * 2
		case 1:
			want += v * v
		default:
			want += 3 * v * v
		}
	}

	gcStress()
	var got int64
	for _, sh := range shapes {
		got += sh.Area()
	}
	ck(got == want, "interface dispatch sum mismatch after GC")

	for _, w := range []int64{1, 2, 7, 100} {
		for _, h := range []int64{1, 3, 9, 50} {
			ck(directRect(w, h) == ifaceRect(w, h), "direct vs interface rect mismatch")
			ck(directRect(w, h) == w*h, "direct rect area mismatch")
		}
	}

	fns := make([]func() int64, 0, len(shapes))
	for _, sh := range shapes {
		fns = append(fns, sh.Area)
	}
	gcStress()
	var msum int64
	for _, f := range fns {
		msum += f()
	}
	ck(msum == want, "method value dispatch sum mismatch after GC")

	for i, sh := range shapes {
		var code int
		switch sh.(type) {
		case *rect:
			code = 1
		case square:
			code = 2
		case *circle:
			code = 3
		default:
			code = 0
		}
		ck(code == i%3+1, "type switch dispatch mismatch")
	}

	var loopSum int64
	for k := 0; k < 1000; k++ {
		for _, sh := range shapes {
			loopSum += sh.Area()
		}
	}
	ck(loopSum == want*1000, "hot interface dispatch sum mismatch")
	ck(len(shapes) == 30, "shape slice length changed")

	wantNames := []string{"rect", "square", "circle"}
	for i, sh := range shapes {
		ck(sh.Name() == wantNames[i%3], "interface Name dispatch mismatch")
	}

	var nilShape shape
	ck(nilShape == nil, "nil interface must be nil")
	expectRuntimePanic("nil interface call", func() {
		_ = nilShape.Area()
	})

	var nilRect *rect
	var typedNil shape = nilRect
	ck(typedNil != nil, "typed nil interface must not equal nil")
	expectRuntimePanic("typed nil receiver call", func() {
		_ = typedNil.Area()
	})

	ck(got == want && msum == want, "dispatch results changed after GC")

	finish("l11_devirtualize")
}
