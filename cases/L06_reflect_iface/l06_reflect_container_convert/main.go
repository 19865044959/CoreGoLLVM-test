// L06: reflect 容器转换：[]any/map[string]any 逐元素 Convert 到强类型容器、同名底层容器整体
// Convert、key 重建、nil/非法元素错误路径与指针值跨 GC 保活。
package main

import (
	"fmt"
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

type myString string
type myInt int
type intMap map[string]int
type namedInts []int

var (
	failed  int32
	gcSink  [][]byte
	anySink []any
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

func convertSlice(src reflect.Value, dstT reflect.Type) (reflect.Value, error) {
	if src.Kind() != reflect.Slice || dstT.Kind() != reflect.Slice {
		return reflect.Value{}, fmt.Errorf("not slice")
	}
	out := reflect.MakeSlice(dstT, 0, src.Len())
	for i := 0; i < src.Len(); i++ {
		v := src.Index(i)
		if v.Kind() == reflect.Interface {
			if v.IsNil() {
				return reflect.Value{}, fmt.Errorf("nil element %d", i)
			}
			v = v.Elem()
		}
		if !v.IsValid() || !v.Type().ConvertibleTo(dstT.Elem()) {
			return reflect.Value{}, fmt.Errorf("element %d not convertible", i)
		}
		out = reflect.Append(out, v.Convert(dstT.Elem()))
	}
	return out, nil
}

func convertMap(src reflect.Value, dstT reflect.Type) (reflect.Value, error) {
	if src.Kind() != reflect.Map || dstT.Kind() != reflect.Map {
		return reflect.Value{}, fmt.Errorf("not map")
	}
	out := reflect.MakeMapWithSize(dstT, src.Len())
	iter := src.MapRange()
	for iter.Next() {
		k, v := iter.Key(), iter.Value()
		if k.Kind() == reflect.Interface {
			if k.IsNil() {
				return reflect.Value{}, fmt.Errorf("nil map key")
			}
			k = k.Elem()
		}
		if v.Kind() == reflect.Interface {
			if v.IsNil() {
				return reflect.Value{}, fmt.Errorf("nil map value")
			}
			v = v.Elem()
		}
		if !k.IsValid() || !v.IsValid() || !k.Type().ConvertibleTo(dstT.Key()) || !v.Type().ConvertibleTo(dstT.Elem()) {
			return reflect.Value{}, fmt.Errorf("map entry not convertible")
		}
		out.SetMapIndex(k.Convert(dstT.Key()), v.Convert(dstT.Elem()))
	}
	return out, nil
}

func main() {
	uafSeed = envSeed()
	rounds := envInt("TORTURE_ROUNDS", 20, 2000)
	items := envInt("TORTURE_ITEMS", 64, 20000)
	if items < 8 {
		items = 8
	}

	// 整体 Convert 只接受底层类型一致的命名容器。
	si := []int{1, 2, 3}
	ck(!reflect.TypeOf(si).ConvertibleTo(reflect.TypeOf([]int64{})), "[]int to []int64 requires element conversion")
	ck(reflect.TypeOf(si).ConvertibleTo(reflect.TypeOf(namedInts{})), "[]int convertible to named []int")
	nsv := reflect.ValueOf(si).Convert(reflect.TypeOf(namedInts{}))
	ck(nsv.Type() == reflect.TypeOf(namedInts{}) && nsv.Len() == 3 && nsv.Index(2).Int() == 3, "named slice convert")
	m := map[string]int{"a": 1, "b": 2}
	ck(reflect.TypeOf(m).ConvertibleTo(reflect.TypeOf(intMap{})), "map convertible to named same-underlying map")
	nmv := reflect.ValueOf(m).Convert(reflect.TypeOf(intMap{}))
	ck(nmv.Type() == reflect.TypeOf(intMap{}) && nmv.Len() == 2, "named map convert")
	ck(!reflect.TypeOf(m).ConvertibleTo(reflect.TypeOf(map[string]int64{})), "map elem change not whole-convertible")
	ck(!reflect.TypeOf(map[int]string{1: "x"}).ConvertibleTo(reflect.TypeOf(map[myInt]string{})), "map key change not whole-convertible")

	// nil 容器整体 Convert 保留 nil。
	var nilSlice []int
	ck(reflect.ValueOf(nilSlice).Convert(reflect.TypeOf(namedInts{})).IsNil(), "nil slice convert preserves nil")
	var nilMap map[string]int
	ck(reflect.ValueOf(nilMap).Convert(reflect.TypeOf(intMap{})).IsNil(), "nil map convert preserves nil")

	// 逐元素转换。
	conv, err := convertSlice(reflect.ValueOf([]any{1, int64(2), int(3)}), reflect.TypeOf([]int64{}))
	ck(err == nil && conv.Len() == 3 && conv.Index(1).Int() == 2, "elementwise any to int64")
	whole, err := convertSlice(reflect.ValueOf([]int{4, 5}), reflect.TypeOf([]int64{}))
	ck(err == nil && whole.Len() == 2 && whole.Index(1).Int() == 5, "elementwise int to int64")
	ss, err := convertSlice(reflect.ValueOf([]string{"a", "b"}), reflect.TypeOf([]myString{}))
	ck(err == nil && ss.Len() == 2 && ss.Index(0).String() == "a" && ss.Index(0).Type() == reflect.TypeOf(myString("")), "elementwise string to named string")
	_, err = convertSlice(reflect.ValueOf([]any{int64(1), "x"}), reflect.TypeOf([]int64{}))
	ck(err != nil, "string element conversion fails")
	_, err = convertSlice(reflect.ValueOf([]any{nil}), reflect.TypeOf([]int64{}))
	ck(err != nil, "nil interface element fails")
	empty, err := convertSlice(reflect.ValueOf(nilSlice), reflect.TypeOf(namedInts{}))
	ck(err == nil && empty.Len() == 0 && !empty.IsNil(), "nil slice elementwise conversion yields empty")

	mc, err := convertMap(reflect.ValueOf(map[string]any{"a": 1, "b": int64(2)}), reflect.TypeOf(map[string]int64{}))
	ck(err == nil && mc.Len() == 2 && mc.MapIndex(reflect.ValueOf("a")).Int() == 1, "map any to int64")
	kc, err := convertMap(reflect.ValueOf(map[int]string{1: "x", 2: "y"}), reflect.TypeOf(map[myInt]string{}))
	ck(err == nil && kc.Len() == 2 && kc.MapIndex(reflect.ValueOf(myInt(1))).String() == "x", "map key conversion rebuild")
	_, err = convertMap(reflect.ValueOf(map[string]any{"a": "x"}), reflect.TypeOf(map[string]int64{}))
	ck(err != nil, "map value conversion fails")
	_, err = convertMap(reflect.ValueOf(map[string]any{"a": nil}), reflect.TypeOf(map[string]int64{}))
	ck(err != nil, "nil map value fails")
	nmc, err := convertMap(reflect.ValueOf(nilMap), reflect.TypeOf(intMap{}))
	ck(err == nil && nmc.Len() == 0 && !nmc.IsNil(), "nil map elementwise conversion yields empty")

	for r := 0; r < rounds; r++ {
		base := uafSeed*100000 + int64(r)*1000

		srcSlice := make([]any, items)
		want := int64(0)
		for i := 0; i < items; i++ {
			v := base + int64(i)
			if i%2 == 0 {
				srcSlice[i] = int(v)
			} else {
				srcSlice[i] = v
			}
			want += v
		}
		ds, err := convertSlice(reflect.ValueOf(srcSlice), reflect.TypeOf([]int64{}))
		ck(err == nil && ds.Len() == items, "torture slice conversion shape")
		var got int64
		for i := 0; i < ds.Len(); i++ {
			got += ds.Index(i).Int()
		}
		ck(got == want, "torture slice conversion sum")

		srcMap := make(map[string]any, items)
		for i := 0; i < items; i++ {
			srcMap[fmt.Sprintf("k%05d", i)] = srcSlice[i]
		}
		dm, err := convertMap(reflect.ValueOf(srcMap), reflect.TypeOf(map[string]int64{}))
		ck(err == nil && dm.Len() == items, "torture map conversion shape")
		var msum int64
		iter := dm.MapRange()
		for iter.Next() {
			msum += iter.Value().Int()
		}
		ck(msum == want, "torture map conversion sum")

		pm := map[string]any{
			"p0": &node{V: base, Label: "p0"},
			"p1": &node{V: base + 1, Label: "p1"},
		}
		pc, err := convertMap(reflect.ValueOf(pm), reflect.TypeOf(map[string]*node{}))
		ck(err == nil && pc.Len() == 2, "pointer map conversion")
		anySink = []any{ds, dm, pc, pm}
		if r%2 == 1 {
			gcStress()
		}
		pv := pc.MapIndex(reflect.ValueOf("p0"))
		ck(pv.IsValid() && pv.Elem().FieldByName("V").Int() == base, "pointer map value after GC")
		ck(pv.Elem().FieldByName("Label").String() == "p0", "pointer map label after GC")

		im := map[string]int{"x": int(base), "y": int(base + 1)}
		nmv := reflect.ValueOf(im).Convert(reflect.TypeOf(intMap{}))
		ck(nmv.Type() == reflect.TypeOf(intMap{}) && nmv.Len() == 2, "named map each round")
		nm2 := nmv.Interface().(intMap)
		ck(nm2["x"] == int(base) && nm2["y"] == int(base+1), "named map typed view after GC")
	}
	finish("l06_reflect_container_convert")
}
