// L15: 构建标签控制常量与方法集。默认构建只有 Base 方法（tagMode="base"）；
// -tags=tagExt 时 tagMode="ext" 且类型额外获得 Extra 方法，反射方法数与接口
// 断言随之变化。手动验证：go build -tags=tagExt && EXPECT_TAG=ext。
package main

import (
	"fmt"
	"os"
	"reflect"
	"runtime"
	"sort"
	"sync/atomic"
)

type tagNode struct {
	v    int64
	name string
}

type tagged struct {
	n int64
	p *tagNode
}

type baseIface interface {
	Base() string
}

type extraIface interface {
	Extra() string
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

func methodNames(t reflect.Type) []string {
	out := make([]string, 0, t.NumMethod())
	for i := 0; i < t.NumMethod(); i++ {
		out = append(out, t.Method(i).Name)
	}
	sort.Strings(out)
	return out
}

func main() {
	want := os.Getenv("EXPECT_TAG")
	if want == "" {
		want = "base"
	}
	t := tagged{n: 9, p: &tagNode{v: 9, name: "tagged"}}

	ck(tagMode == want, "build-tag controlled constant")
	wantBase := "base-method"
	if want == "ext" {
		wantBase = "ext-base"
	}
	ck(t.Base() == wantBase, "build-tag controlled method body")

	var bi baseIface = t
	ck(bi.Base() == wantBase, "value type satisfies base interface")

	_, hasExtra := any(t).(extraIface)
	ck(hasExtra == (want == "ext"), "method set matches build tag")
	wantMethods := []string{"Base"}
	wantNum := 1
	if want == "ext" {
		wantMethods = []string{"Base", "Extra"}
		wantNum = 2
	}
	names := methodNames(reflect.TypeOf(t))
	ck(len(names) == wantNum, "reflected method count")
	ck(reflect.DeepEqual(names, wantMethods), "reflected method names")

	gcStress()
	ck(t.n == 9 && t.p != nil && t.p.v == 9 && t.p.name == "tagged", "tagged payload after GC")
	finish("l15_tag_const_methodset")
}
