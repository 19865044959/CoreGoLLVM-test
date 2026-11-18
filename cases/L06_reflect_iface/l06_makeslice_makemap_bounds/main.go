// L06: reflect.MakeSlice/MakeMap 边界与别名语义：零值/容量/切片窗口、Append 扩容与共享底层
// 数组、SetLen/SetCap 可寻址限制、nil map/错误类型 panic、嵌套动态 map 跨 GC。
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

type item struct {
	P *node
	N int64
}

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

func mustPanic(name string, fn func()) {
	defer func() {
		if recover() == nil {
			ck(false, name+" did not panic")
		}
	}()
	fn()
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
	rounds := envInt("TORTURE_ROUNDS", 16, 2000)
	items := envInt("TORTURE_ITEMS", 64, 20000)
	if items < 8 {
		items = 8
	}

	intT := reflect.TypeOf(0)
	sliceT := reflect.TypeOf([]int{})
	mapT := reflect.TypeOf(map[string]int{})

	s := reflect.MakeSlice(sliceT, 3, 5)
	ck(s.Len() == 3 && s.Cap() == 5, "MakeSlice len cap")
	for i := 0; i < 3; i++ {
		ck(s.Index(i).Int() == 0, "MakeSlice zeroed element")
	}
	s.Index(1).SetInt(7)
	sub := s.Slice(1, 3)
	ck(sub.Len() == 2 && sub.Cap() == 4 && sub.Index(0).Int() == 7, "Slice window")
	sub3 := s.Slice3(0, 2, 4)
	ck(sub3.Len() == 2 && sub3.Cap() == 4, "Slice3 window")
	mustPanic("Slice3 high beyond cap", func() { s.Slice3(0, 2, 6) })
	mustPanic("MakeSlice negative len", func() { reflect.MakeSlice(sliceT, -1, 0) })
	mustPanic("MakeSlice len beyond cap", func() { reflect.MakeSlice(sliceT, 2, 1) })

	// MakeSlice 返回的 Value 不可寻址，SetLen/SetCap 必须作用于可寻址切片。
	mustPanic("SetLen on unaddressable MakeSlice", func() { s.SetLen(4) })
	buf := make([]int, 3, 5)
	bv := reflect.ValueOf(&buf).Elem()
	bv.SetLen(4)
	ck(len(buf) == 4 && buf[3] == 0, "SetLen extends with zero values")
	mustPanic("SetLen beyond cap", func() { bv.SetLen(6) })
	mustPanic("SetCap below len", func() { bv.SetCap(2) })
	mustPanic("SetCap above cap", func() { bv.SetCap(6) })
	bv.SetCap(4)
	ck(cap(buf) == 4, "SetCap shrinks capacity")
	mustPanic("SetLen after cap shrink", func() { bv.SetLen(5) })

	// Append 在 cap 内共享底层数组，超出 cap 后复制。
	a := reflect.MakeSlice(sliceT, 2, 4)
	a.Index(0).SetInt(1)
	a.Index(1).SetInt(2)
	b := reflect.Append(a, reflect.ValueOf(3))
	ck(a.Len() == 2 && b.Len() == 3 && a.Cap() == 4 && b.Cap() == 4, "Append within cap shape")
	b.Index(0).SetInt(99)
	ck(a.Index(0).Int() == 99, "Append within cap shares backing array")
	c := reflect.MakeSlice(sliceT, 2, 2)
	c.Index(0).SetInt(1)
	d := reflect.Append(c, reflect.ValueOf(3))
	ck(d.Len() == 3 && d.Cap() >= 3, "Append beyond cap shape")
	d.Index(0).SetInt(77)
	ck(c.Index(0).Int() == 1, "Append beyond cap copies")
	mustPanic("Append wrong element type", func() { reflect.Append(a, reflect.ValueOf(int64(1))) })
	as := reflect.AppendSlice(reflect.MakeSlice(sliceT, 0, 4), reflect.ValueOf([]int{4, 5, 6}))
	ck(as.Len() == 3 && as.Index(2).Int() == 6, "AppendSlice")

	src := reflect.ValueOf([]int{1, 2, 3, 4, 5})
	dst := reflect.MakeSlice(sliceT, 5, 5)
	n := reflect.Copy(dst, src)
	ck(n == 5 && dst.Index(4).Int() == 5, "Copy full slice")
	ov := reflect.ValueOf([]int{1, 2, 3, 4, 5})
	n = reflect.Copy(ov.Slice(1, 5), ov.Slice(0, 4))
	ck(n == 4 && ov.Index(1).Int() == 1 && ov.Index(4).Int() == 4, "Copy overlapping slices")
	bs := make([]byte, 3)
	n = reflect.Copy(reflect.ValueOf(bs), reflect.ValueOf("abc"))
	ck(n == 3 && string(bs) == "abc", "Copy string to bytes")

	// 接口元素切片：MakeSlice 零值为 nil 接口。
	anyT := reflect.TypeOf([]any{})
	ai := reflect.MakeSlice(anyT, 2, 2)
	ck(ai.Index(0).Kind() == reflect.Interface && ai.Index(0).IsNil(), "interface slice zero nil")
	ai.Index(0).Set(reflect.ValueOf(&node{V: uafSeed, Label: "i"}))
	ai.Index(1).Set(reflect.ValueOf("s"))
	is := reflect.MakeSlice(reflect.TypeOf([]item{}), 1, 1)
	is.Index(0).FieldByName("P").Set(reflect.ValueOf(&node{V: uafSeed + 1, Label: "p"}))
	is.Index(0).FieldByName("N").SetInt(5)
	anySink = []any{ai, is}
	gcStress()
	ck(ai.Index(0).Elem().Elem().FieldByName("V").Int() == uafSeed, "interface slice pointer after GC")
	ck(ai.Index(1).Elem().String() == "s", "interface slice string after GC")
	ck(is.Index(0).FieldByName("P").Elem().FieldByName("V").Int() == uafSeed+1, "struct slice pointer after GC")

	m := reflect.MakeMap(mapT)
	ck(!m.IsNil() && m.Len() == 0, "MakeMap empty non-nil")
	m.SetMapIndex(reflect.ValueOf("a"), reflect.ValueOf(1))
	m.SetMapIndex(reflect.ValueOf("b"), reflect.ValueOf(2))
	ck(m.Len() == 2 && m.MapIndex(reflect.ValueOf("a")).Int() == 1, "SetMapIndex insert")
	ck(!m.MapIndex(reflect.ValueOf("missing")).IsValid(), "MapIndex missing is invalid")
	m.SetMapIndex(reflect.ValueOf("a"), reflect.Value{})
	ck(m.Len() == 1, "SetMapIndex zero value deletes")
	mustPanic("SetMapIndex wrong key type", func() { m.SetMapIndex(reflect.ValueOf(int64(1)), reflect.ValueOf(1)) })
	mustPanic("SetMapIndex wrong value type", func() { m.SetMapIndex(reflect.ValueOf("c"), reflect.ValueOf(int64(1))) })
	mustPanic("MakeMap non-map type", func() { reflect.MakeMap(intT) })
	mn := reflect.MakeMapWithSize(mapT, -3)
	ck(!mn.IsNil() && mn.Len() == 0, "negative map hint is empty non-nil")
	var nilM map[string]int
	nmv := reflect.ValueOf(&nilM).Elem()
	mustPanic("SetMapIndex on nil map", func() { nmv.SetMapIndex(reflect.ValueOf("x"), reflect.ValueOf(1)) })
	ck(nmv.IsNil() && nmv.Len() == 0, "nil map unchanged")

	// map[string]any 持指针 + 嵌套 MapOf(SliceOf)。
	am := reflect.MakeMap(reflect.TypeOf(map[string]any{}))
	am.SetMapIndex(reflect.ValueOf("p"), reflect.ValueOf(&node{V: uafSeed + 2, Label: "am"}))
	nestedT := reflect.MapOf(reflect.TypeOf(""), reflect.SliceOf(intT))
	nested := reflect.MakeMap(nestedT)
	inner := reflect.MakeSlice(reflect.SliceOf(intT), 2, 2)
	inner.Index(0).SetInt(1)
	inner.Index(1).SetInt(2)
	nested.SetMapIndex(reflect.ValueOf("k"), inner)
	anySink = []any{am, nested, inner}
	gcStress()
	ck(am.MapIndex(reflect.ValueOf("p")).Elem().Elem().FieldByName("V").Int() == uafSeed+2, "map any pointer after GC")
	nestedInner := nested.MapIndex(reflect.ValueOf("k"))
	ck(nested.Type() == nestedT && nestedInner.Len() == 2 && nestedInner.Index(1).Int() == 2, "nested map slice after GC")

	for r := 0; r < rounds; r++ {
		base := uafSeed*1000 + int64(r)*100

		sl := reflect.MakeSlice(sliceT, 0, 1)
		var sum int64
		for j := 0; j < items; j++ {
			v := int(base) + j
			sl = reflect.Append(sl, reflect.ValueOf(v))
			sum += int64(v)
		}
		ck(sl.Len() == items && sl.Cap() >= items, "torture append shape")
		var got int64
		for j := 0; j < sl.Len(); j++ {
			got += sl.Index(j).Int()
		}
		ck(got == sum, "torture append sum")

		m := reflect.MakeMapWithSize(mapT, items)
		for j := 0; j < items; j++ {
			m.SetMapIndex(reflect.ValueOf(fmt.Sprintf("k%05d", j)), reflect.ValueOf(j))
		}
		for j := 0; j < items; j += 2 {
			m.SetMapIndex(reflect.ValueOf(fmt.Sprintf("k%05d", j)), reflect.Value{})
		}
		ck(m.Len() == items/2, "torture map delete half")
		var msum int64
		var wantOdd int64
		iter := m.MapRange()
		for iter.Next() {
			msum += iter.Value().Int()
		}
		for j := 1; j < items; j += 2 {
			wantOdd += int64(j)
		}
		ck(msum == wantOdd, "torture map sum")
		if r%2 == 1 {
			gcStress()
			ck(sl.Len() == items && m.Len() == items/2, "torture containers after GC")
		}
	}
	finish("l06_makeslice_makemap_bounds")
}
