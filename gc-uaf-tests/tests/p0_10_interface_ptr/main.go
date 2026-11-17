// P0-10 (报告风险 B14 + A4): 接口/itab/类型断言中的指针。
package main

import (
	"fmt"
	"os"
	"runtime"
	"sync/atomic"
)

type node struct{ v int64 }

type shape interface{ Area() int64 }

type rect struct {
	w, h int64
	tag  *node
}

func (r *rect) Area() int64 { return r.w*r.h + r.tag.v }

type wrap struct {
	P *node
	S string
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
func ifaceGC(i interface{}, s shape) int64 {
	gcStress()
	return s.Area() + i.(*rect).tag.v
}

func main() {
	for round := 0; round < 10; round++ {
		r := &rect{w: 3, h: 4, tag: &node{v: 7}}
		var i interface{} = r
		var s shape = r
		gcStress()
		ck(i.(*rect).Area() == 19, "empty iface assertion after GC")
		ck(s.Area() == 19, "non-empty iface method after GC")
		ck(ifaceGC(r, s) == 26, "iface args across GC")

		sw := interface{}(&rect{w: 5, h: 6, tag: &node{v: 1}})
		gcStress()
		switch v := sw.(type) {
		case *rect:
			ck(v.Area() == 31, "type switch")
		default:
			ck(false, "type switch default")
		}

		var ew interface{} = wrap{P: &node{v: 66}, S: "boxed"}
		gcStress()
		w := ew.(wrap)
		ck(w.P.v == 66 && w.S == "boxed", "struct value boxed in interface")

		var es interface{} = []*node{{v: 1}, {v: 2}, {v: 3}}
		gcStress()
		ck(es.([]*node)[2].v == 3, "slice in interface")

		var em interface{} = map[string]*node{"q": {v: 44}}
		gcStress()
		ck(em.(map[string]*node)["q"].v == 44, "map in interface")

		var conv interface{} = s
		gcStress()
		ck(conv.(shape).Area() == 19, "interface-to-interface conversion")

		var st interface{} = "interface-string"
		gcStress()
		ck(st.(string) == "interface-string", "string in interface")

		var nilp interface{}
		_, ok := nilp.(shape)
		ck(!ok, "nil interface assertion")
	}
	finish("p0_10_interface_ptr")
}
