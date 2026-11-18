// L15: tag 控制的方法集（跨包）。impl.Record 的 Extra 方法只在 -tags=tagExtra
// 时由子包文件提供；main 通过 iface 包接口断言、方法调用与反射方法集验证
// 运行期方法集与构建标签一致。
package main

import (
	"fmt"
	"os"
	"reflect"
	"runtime"
	"sort"
	"sync/atomic"

	"l15_tag_methodset_subpkg/iface"
	"l15_tag_methodset_subpkg/impl"
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

func methodNames(t reflect.Type) []string {
	out := make([]string, 0, t.NumMethod())
	for i := 0; i < t.NumMethod(); i++ {
		out = append(out, t.Method(i).Name)
	}
	sort.Strings(out)
	return out
}

func main() {
	wantExtra := os.Getenv("EXPECT_EXTRA") == "1"

	r := impl.New(13)
	var v iface.Value = r
	ck(v.Get() == 13, "cross-package value interface satisfied")

	_, hasExtra := any(r).(iface.Extra)
	ck(hasExtra == wantExtra, "cross-package extra interface assertion follows build tag")
	if hasExtra {
		ck(any(r).(iface.Extra).Extra() == "extra:record", "tag-gated method dispatch")
	}

	wantMethods := []string{"Get"}
	if wantExtra {
		wantMethods = []string{"Extra", "Get"}
	}
	names := methodNames(reflect.TypeOf(r))
	ck(reflect.DeepEqual(names, wantMethods), "pointer method set follows build tag")
	ck(len(names) == len(wantMethods), "pointer method count follows build tag")

	gcStress()
	ck(r.N == 13 && r.P != nil && r.P.ID == 13 && r.P.Name == "record", "cross-package payload after GC")
	ck(v.Get() == 13 && reflect.DeepEqual(methodNames(reflect.TypeOf(r)), wantMethods), "method set after GC")
	finish("l15_tag_methodset_subpkg")
}
