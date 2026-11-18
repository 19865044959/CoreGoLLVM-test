// L06: reflect 驱动 slice/map 扩容：Append/MakeMap/SetMapIndex 跨 GC 保活。
package main

import (
	"fmt"
	"math/rand"
	"os"
	"reflect"
	"runtime"
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
	anySink any
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

func envInt(name string, def, max int) int {
	s := os.Getenv(name)
	if s == "" {
		return def
	}
	n, err := strconv.Atoi(s)
	if err != nil || n <= 0 {
		return def
	}
	if n > max {
		n = max
	}
	return n
}

func main() {
	uafSeed = envSeed()
	rounds := envInt("TORTURE_ROUNDS", 8, 2000)
	items := envInt("TORTURE_ITEMS", 128, 20000)
	if items < 8 {
		items = 8
	}
	st := reflect.TypeOf([]*node{})
	mt := reflect.TypeOf(map[string]*node{})

	for r := 0; r < rounds; r++ {
		rnd := rand.New(rand.NewSource(uafSeed + int64(r)*131))
		base := int64(r) * 100000

		// 逐个 Append 强制 slice 多次扩容。
		sv := reflect.MakeSlice(st, 0, 1)
		lastCap := sv.Cap()
		growths := 0
		var want int64
		for j := 0; j < items; j++ {
			v := base + int64(j)*3 + 1
			p := &node{V: v, Label: fmt.Sprintf("s%02d", j%10)}
			sv = reflect.Append(sv, reflect.ValueOf(p))
			want += v
			if sv.Cap() != lastCap {
				growths++
				lastCap = sv.Cap()
			}
			if j%128 == 127 {
				gcStress()
			}
		}
		ck(sv.Len() == items, "slice len after growth")
		ck(sv.Cap() >= items, "slice cap covers len")
		ck(growths > 0, "slice reallocated at least once")
		var got int64
		for i := 0; i < sv.Len(); i++ {
			got += sv.Index(i).Elem().FieldByName("V").Int()
		}
		ck(got == want, "reflect sum after slice growth")

		typed := sv.Interface().([]*node)
		ck(len(typed) == items && typed[items-1].V == base+int64(items-1)*3+1, "slice interface view")
		typed[0].Label = "head"
		sv.Index(items - 1).Elem().FieldByName("Label").SetString("tail")
		ck(sv.Index(0).Elem().FieldByName("Label").String() == "head", "typed write visible via reflect")
		ck(typed[items-1].Label == "tail", "reflect write visible in typed slice")

		extra := reflect.MakeSlice(st, 2, 2)
		extra.Index(0).Set(reflect.ValueOf(&node{V: base + 77, Label: "x"}))
		extra.Index(1).Set(reflect.ValueOf(&node{V: base + 78, Label: "y"}))
		sv = reflect.AppendSlice(sv, extra)
		ck(sv.Len() == items+2, "AppendSlice length")
		ck(sv.Index(items+1).Elem().FieldByName("V").Int() == base+78, "AppendSlice element")

		// 重叠 Copy：dest 在 src 之后，必须按 memmove 语义。
		ov := reflect.MakeSlice(st, 6, 6)
		for i := 0; i < 6; i++ {
			ov.Index(i).Set(reflect.ValueOf(&node{V: int64(i + 1), Label: "o"}))
		}
		reflect.Copy(ov.Slice(1, 6), ov.Slice(0, 5))
		for i := 1; i < 6; i++ {
			ck(ov.Index(i).Elem().FieldByName("V").Int() == int64(i), "overlapping reflect.Copy")
		}

		gcStress()
		ck(typed[0].Label == "head" && typed[items-1].Label == "tail", "grown slice elements survive GC")
		ck(typed[1].V == base+4, "middle element survives GC")

		// map 逐个插入触发扩容，随后删除/再插入。
		mv := reflect.MakeMapWithSize(mt, 1)
		var wantMap int64
		order := rnd.Perm(items)
		for j, i := range order {
			key := fmt.Sprintf("k%05d", i)
			v := base + int64(i)*7 + 3
			mv.SetMapIndex(reflect.ValueOf(key), reflect.ValueOf(&node{V: v, Label: "m"}))
			wantMap += v
			if j%128 == 127 {
				gcStress()
			}
		}
		ck(mv.Len() == items, "map grew to expected size")
		var gotMap int64
		for _, k := range mv.MapKeys() {
			gotMap += mv.MapIndex(k).Elem().FieldByName("V").Int()
		}
		ck(gotMap == wantMap, "reflect map sum after growth")

		var delSum int64
		for i := 0; i < items; i += 3 {
			key := fmt.Sprintf("k%05d", i)
			old := mv.MapIndex(reflect.ValueOf(key))
			delSum += old.Elem().FieldByName("V").Int()
			mv.SetMapIndex(reflect.ValueOf(key), reflect.Value{})
		}
		ck(mv.Len() == items-(items+2)/3, "map size after delete")
		var gotAfter int64
		for _, k := range mv.MapKeys() {
			gotAfter += mv.MapIndex(k).Elem().FieldByName("V").Int()
		}
		ck(gotAfter == wantMap-delSum, "map sum after delete")

		for i := 0; i < 5; i++ {
			mv.SetMapIndex(reflect.ValueOf(fmt.Sprintf("n%d", i)), reflect.ValueOf(&node{V: base + int64(i), Label: "new"}))
		}
		ck(mv.Len() == items-(items+2)/3+5, "map size after reinsert")
		mv.MapIndex(reflect.ValueOf("k00001")).Elem().FieldByName("Label").SetString("changed")
		mm := mv.Interface().(map[string]*node)
		ck(mm["k00001"].Label == "changed", "map pointer field write via reflect")
		ck(mm["n4"].V == base+4, "new map entries")

		// 嵌套 []any 扩容，元素含指针与 map。
		lt := reflect.TypeOf([]any{})
		lv := reflect.MakeSlice(lt, 0, 1)
		lv = reflect.Append(lv, reflect.ValueOf(&node{V: base + 901, Label: "any"}))
		lv = reflect.Append(lv, reflect.ValueOf(map[string]*node{"x": {V: base + 902, Label: "in"}}))
		list := lv.Interface().([]any)
		gcStress()
		ck(len(list) == 2, "[]any length after reflect growth")
		ck(list[0].(*node).V == base+901, "[]any pointer element survives GC")
		im := list[1].(map[string]*node)
		ck(im["x"].V == base+902 && im["x"].Label == "in", "[]any map element survives GC")

		anySink = []any{sv, mv, ov, list}
		gcStress()
		sink := anySink.([]any)
		grown := sink[0].(reflect.Value).Interface().([]*node)
		ck(len(grown) == items+2 && grown[0].Label == "head", "grown slice held via any survives GC")
		gm := sink[1].(reflect.Value).Interface().(map[string]*node)
		ck(len(gm) == items-(items+2)/3+5 && gm["k00001"].Label == "changed", "grown map held via any survives GC")
	}
	finish("l06_reflect_grow_slice_map")
}
