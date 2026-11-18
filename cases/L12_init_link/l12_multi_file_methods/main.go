// L12: 同一类型的方法分散在多个文件，接口实现与派发在 GC 前后一致。
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

func main() {
	shapes := []Shape{
		circle{base: base{tag: ""}, r: 2},
		&rect{w: 2, h: 3},
		namedCircle{circle: circle{base: base{tag: "ignored"}, r: 1}, label: "unit"},
	}

	ck(totalArea(shapes) == 21, "total area via interface dispatch")
	ck(totalPerimeter(shapes) == 28, "total perimeter via interface dispatch")
	ck(describe(shapes) == "circle|rect|unit", "names via interface dispatch")

	// 方法集规则：rect 只有指针接收者方法，值类型不实现 Shape。
	var anyRect interface{} = rect{w: 1, h: 2}
	_, rectValOK := anyRect.(Shape)
	ck(!rectValOK, "rect value does not implement Shape")
	var anyRectPtr interface{} = &rect{w: 1, h: 2}
	_, rectPtrOK := anyRectPtr.(Shape)
	ck(rectPtrOK, "rect pointer implements Shape")

	var anyCircle interface{} = circle{r: 1}
	if _, ok := anyCircle.(Shape); !ok {
		ck(false, "circle value implements Shape")
	}

	// 嵌入类型 + 跨文件方法：Area 被提升，Name 被 namedCircle 覆盖。
	nc := shapes[2].(namedCircle)
	ck(nc.Area() == 3 && nc.Perimeter() == 6, "promoted methods from embedded circle")
	ck(nc.Name() == "unit", "overridden Name method")
	ck(nc.Tag() == "ignored", "promoted Tag from embedded base")

	var circles, rects, named int
	for _, s := range shapes {
		switch s.(type) {
		case circle:
			circles++
		case *rect:
			rects++
		case namedCircle:
			named++
		default:
			ck(false, "unexpected dynamic type")
		}
	}
	ck(circles == 1 && rects == 1 && named == 1, "type switch dispatch")

	byName := map[string]Shape{"c": shapes[0], "r": shapes[1], "n": shapes[2]}
	ck(byName["r"].Area() == 6 && byName["n"].Area() == 3, "interface values in map")

	gcStress()
	ck(totalArea(shapes) == 21 && totalPerimeter(shapes) == 28, "dispatch after GC")
	ck(describe(shapes) == "circle|rect|unit", "names after GC")
	ck(shapes[1].(*rect).w == 2, "pointer-receiver object survives GC")

	// 改写接口持有的指针对象，GC 后派发应看到新值。
	rp := shapes[1].(*rect)
	rp.w = 4
	gcStress()
	ck(shapes[1] == Shape(rp), "interface holds same *rect after GC")
	ck(rp.w == 4 && rp.h == 3, "mutated rect content after GC")
	ck(totalArea(shapes) == 27, "area reflects mutation after GC")
	ck(totalPerimeter(shapes) == 32, "perimeter reflects mutation after GC")

	for i := 0; i < 32; i++ {
		gcSink = append(gcSink, make([]byte, 1024))
	}
	gcSink = nil
	runtime.GC()
	ck(shapes[2].(namedCircle).Area() == 3, "embedded value interface survives GC")
	ck(byName["r"].Area() == 12, "map-held interface pointer sees mutation")

	finish("l12_multi_file_methods")
}
