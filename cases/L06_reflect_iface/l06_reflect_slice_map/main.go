// L06: reflect 遍历/修改 slice 与 map of pointers，GC 后类型化视图一致。
package main

import (
	"fmt"
	"math/rand"
	"os"
	"reflect"
	"runtime"
	"sort"
	"strconv"
	"sync/atomic"
)

type node struct {
	V     int64
	Label string
}

var (
	failed  int32
	gcSink  [][]byte
	uafSeed int64 = 1
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
		fmt.Printf("FAILED %s seed=%d (%d failed checks)\n", name, uafSeed, n)
		os.Exit(1)
	}
	fmt.Println("ALL PASS " + name)
}

func envSeed() int64 {
	s := os.Getenv("UAF_SEED")
	if s == "" {
		return 1
	}
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil || n <= 0 {
		return 1
	}
	return n
}

func main() {
	uafSeed = envSeed()
	et := reflect.TypeOf((*node)(nil))
	st := reflect.SliceOf(et)
	kt := reflect.TypeOf("")
	mt := reflect.MapOf(kt, et)
	nmt := reflect.MapOf(kt, st)

	const rounds = 40
	for r := 0; r < rounds; r++ {
		base := int64(r * 10)

		sv := reflect.MakeSlice(st, 0, 12)
		for i := 0; i < 10; i++ {
			p := &node{V: base + int64(i), Label: "sl"}
			sv = reflect.Append(sv, reflect.ValueOf(p))
		}
		sv.Index(3).Elem().FieldByName("V").SetInt(999)
		sv.Index(0).Elem().FieldByName("Label").SetString("first")

		extra := reflect.MakeSlice(st, 2, 2)
		extra.Index(0).Set(reflect.ValueOf(&node{V: 1000, Label: "x"}))
		extra.Index(1).Set(reflect.ValueOf(&node{V: 1001, Label: "y"}))
		sv = reflect.AppendSlice(sv, extra)

		mv := reflect.MakeMapWithSize(mt, 12)
		order := rand.New(rand.NewSource(uafSeed + int64(r))).Perm(10)
		for _, i := range order {
			k := fmt.Sprintf("k%02d", i)
			mv.SetMapIndex(reflect.ValueOf(k), reflect.ValueOf(&node{V: base + int64(100+i), Label: "m"}))
		}
		// 通过 MapIndex 取出的指针写字段。
		mv.MapIndex(reflect.ValueOf("k05")).Elem().FieldByName("Label").SetString("changed")
		mv.SetMapIndex(reflect.ValueOf("k00"), reflect.Value{})
		mv.SetMapIndex(reflect.ValueOf("k99"), reflect.ValueOf(&node{V: 555, Label: "new"}))

		// map[string][]*node 嵌套。
		nmv := reflect.MakeMap(nmt)
		inner := reflect.MakeSlice(st, 0, 2)
		inner = reflect.Append(inner, reflect.ValueOf(&node{V: 41, Label: "in"}))
		nmv.SetMapIndex(reflect.ValueOf("group"), inner)

		// 反射容器与类型化视图共用底层数据。
		ss := sv.Interface().([]*node)
		mm := mv.Interface().(map[string]*node)
		nm := nmv.Interface().(map[string][]*node)

		gcStress()
		ck(len(ss) == 12 && ss[3].V == 999 && ss[0].Label == "first", "reflected slice after GC")
		ck(ss[10].V == 1000 && ss[11].Label == "y", "appended slice elements after GC")
		ck(len(mm) == 10, "map size after delete+insert")
		_, gone := mm["k00"]
		ck(!gone, "deleted key absent")
		ck(mm["k05"].Label == "changed", "map pointer field written via reflect")
		ck(mm["k99"].V == 555, "inserted map pointer")
		ck(len(nm["group"]) == 1 && nm["group"][0].V == 41, "nested map slice")

		// MapKeys 顺序不确定，排序后求和，验证遍历结果。
		keys := mv.MapKeys()
		names := make([]string, 0, len(keys))
		for _, k := range keys {
			names = append(names, k.String())
		}
		sort.Strings(names)
		var sum int64
		for _, k := range names {
			sum += mv.MapIndex(reflect.ValueOf(k)).Elem().FieldByName("V").Int()
		}
		var want int64
		wantNames := make([]string, 0, len(mm))
		for k := range mm {
			wantNames = append(wantNames, k)
		}
		sort.Strings(wantNames)
		for _, k := range wantNames {
			want += mm[k].V
		}
		ck(sum == want, "reflect map iteration sum")
		ck(len(names) == len(wantNames), "reflect map key count")

		// 类型化视图写入后，反射读取应看到同一指针对象。
		ss[1].V = -1
		mm["k01"].Label = "typed"
		gcStress()
		ck(sv.Index(1).Elem().FieldByName("V").Int() == -1, "typed slice write visible via reflect")
		ck(mv.MapIndex(reflect.ValueOf("k01")).Elem().FieldByName("Label").String() == "typed", "typed map write visible via reflect")
		ck(ss[3].V == 999 && mm["k05"].Label == "changed", "pointer elements survive GC")
	}
	finish("l06_reflect_slice_map")
}
