// L16: reflect 值操作：带环指针图深拷贝、MapRange/SetMapIndex、Slice/Slice3/Swapper、
// Convert/Overflow/Comparable/Equal 与 reflect.StructOf 动态结构；全部跨 GC 复核。
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
	Name  string
	Value int64
	Next  *node
	Tags  map[string]string
	Kids  []*node
	Extra any
}

var (
	failed  int32
	gcSink  [][]byte
	live    []any
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
		b := make([]byte, 8192)
		b[0] = byte(i)
		gcSink = append(gcSink[:0], b)
	}
	for i := 0; i < 64; i++ {
		_ = &node{Name: strconv.Itoa(i), Tags: map[string]string{"i": strconv.Itoa(i)}}
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

func panics(f func()) (p bool) {
	defer func() {
		if recover() != nil {
			p = true
		}
	}()
	f()
	return false
}

func deepCopy(v reflect.Value, seen map[uintptr]reflect.Value) reflect.Value {
	if !v.IsValid() {
		return v
	}
	switch v.Kind() {
	case reflect.Ptr:
		if v.IsNil() {
			return reflect.Zero(v.Type())
		}
		p := v.Pointer()
		if c, ok := seen[p]; ok {
			return c
		}
		cp := reflect.New(v.Type().Elem())
		seen[p] = cp
		cp.Elem().Set(deepCopy(v.Elem(), seen))
		return cp
	case reflect.Struct:
		cp := reflect.New(v.Type()).Elem()
		for i := 0; i < v.NumField(); i++ {
			cp.Field(i).Set(deepCopy(v.Field(i), seen))
		}
		return cp
	case reflect.Slice:
		if v.IsNil() {
			return reflect.Zero(v.Type())
		}
		cp := reflect.MakeSlice(v.Type(), v.Len(), v.Cap())
		for i := 0; i < v.Len(); i++ {
			cp.Index(i).Set(deepCopy(v.Index(i), seen))
		}
		return cp
	case reflect.Map:
		if v.IsNil() {
			return reflect.Zero(v.Type())
		}
		cp := reflect.MakeMapWithSize(v.Type(), v.Len())
		it := v.MapRange()
		for it.Next() {
			cp.SetMapIndex(deepCopy(it.Key(), seen), deepCopy(it.Value(), seen))
		}
		return cp
	case reflect.Interface:
		if v.IsNil() {
			return reflect.Zero(v.Type())
		}
		cp := reflect.New(v.Type()).Elem()
		cp.Set(deepCopy(v.Elem(), seen))
		return cp
	default:
		return v
	}
}

func main() {
	uafSeed = envSeed()
	rng := rand.New(rand.NewSource(uafSeed))

	a := &node{Name: "a", Value: 1, Tags: map[string]string{"k": "v"}}
	b := &node{Name: "b", Value: 2, Tags: map[string]string{"x": "y"}}
	root := &node{Name: "root", Value: 10, Next: a, Kids: []*node{a, b}, Extra: b}
	root.Tags = map[string]string{"r": "1"}
	a.Next = root // 环：root -> a -> root

	// 1) 反射深拷贝：指针共享/环结构保留，原图不受影响。
	cpRootV := deepCopy(reflect.ValueOf(root), map[uintptr]reflect.Value{})
	ck(cpRootV.Kind() == reflect.Ptr && cpRootV.Type() == reflect.TypeOf(root), "deep copy type")
	cpRoot := cpRootV.Interface().(*node)
	ck(cpRoot != root, "deep copy must allocate new root")
	ck(cpRoot.Next != nil && cpRoot.Next != root.Next, "deep copy next must be new")
	ck(cpRoot.Next.Next == cpRoot, "cycle must be preserved in copy")
	ck(len(cpRoot.Kids) == 2 && cpRoot.Kids[0] == cpRoot.Next, "shared pointer must stay shared in copy")
	ck(cpRoot.Kids[1] != b && cpRoot.Kids[1].Name == "b", "second kid copied independently")
	extra, ok := cpRoot.Extra.(*node)
	ck(ok && extra == cpRoot.Kids[1], "interface field copy must share copied kid")
	ck(cpRoot.Tags["r"] == "1" && root.Tags["r"] == "1", "map copied")
	cpRoot.Tags["r"] = "copy"
	cpRoot.Next.Value = 99
	cpRoot.Kids = append(cpRoot.Kids, &node{Name: "c", Value: 3})
	ck(root.Tags["r"] == "1" && a.Value == 1 && len(root.Kids) == 2, "original mutated by deep copy")
	live = append(live, root, cpRoot)

	rv := reflect.ValueOf(cpRoot).Elem()
	ck(rv.Kind() == reflect.Struct && rv.CanSet(), "root elem settable")
	nameF := rv.FieldByName("Value")
	ck(nameF.IsValid() && nameF.Kind() == reflect.Int64 && nameF.Int() == 10, "Value field")
	nameF.SetInt(11)
	ck(cpRoot.Value == 11, "SetInt did not write through")
	nextName := rv.FieldByName("Next").Elem().FieldByName("Name").String()
	ck(nextName == "a", fmt.Sprintf("FieldByName chain=%q", nextName))
	kid := rv.Field(4).Index(0)
	ck(kid.Kind() == reflect.Ptr && kid.Interface().(*node) == cpRoot.Next, "Kids[0]")
	ck(rv.FieldByName("Extra").Kind() == reflect.Interface, "Extra kind")
	if e := rv.FieldByName("Extra").Elem(); e.IsValid() {
		ck(e.Interface().(*node) == cpRoot.Kids[1], "Extra dynamic value")
	}
	ck(!panics(func() { rv.FieldByName("Missing") }), "missing field returns zero Value")
	ck(!rv.FieldByName("Missing").IsValid(), "missing field invalid")

	// 2) MapRange / SetMapIndex：键排序后确定，nil map 为空。
	m := map[string]*node{"b": b, "a": a, "nil": nil}
	keys := []string{}
	vals := map[string]*node{}
	it := reflect.ValueOf(m).MapRange()
	for it.Next() {
		k := it.Key().String()
		keys = append(keys, k)
		nv := it.Value()
		if nv.IsNil() {
			vals[k] = nil
			continue
		}
		vals[k] = nv.Interface().(*node)
	}
	sort.Strings(keys)
	ck(fmt.Sprint(keys) == "[a b nil]", fmt.Sprintf("map keys=%v", keys))
	ck(vals["a"] == a && vals["b"] == b && vals["nil"] == nil, "map values")
	ck(!reflect.ValueOf(map[string]*node(nil)).MapRange().Next(), "nil map range must be empty")
	mv := reflect.ValueOf(m)
	mv.SetMapIndex(reflect.ValueOf("c"), reflect.ValueOf(a))
	ck(m["c"] == a, "SetMapIndex insert")
	mv.SetMapIndex(reflect.ValueOf("c"), reflect.Value{})
	_, stillThere := m["c"]
	ck(!stillThere, "SetMapIndex delete")
	live = append(live, m)

	// 3) Slice/Slice3/Swapper。
	s := []int{1, 2, 3, 4, 5}
	sv := reflect.ValueOf(s)
	sv.Index(0).SetInt(9)
	ck(s[0] == 9, "Index SetInt shares backing array")
	mid := sv.Slice(1, 3)
	ck(mid.Len() == 2 && mid.Index(0).Int() == 2 && mid.Index(1).Int() == 3, "Slice(1,3)")
	tri := sv.Slice3(1, 4, 5)
	ck(tri.Len() == 3 && tri.Cap() == 4, fmt.Sprintf("Slice3 len/cap=%d/%d", tri.Len(), tri.Cap()))
	sw := reflect.Swapper(s)
	sw(0, 4)
	ck(s[0] == 5 && s[4] == 9, fmt.Sprintf("Swapper result=%v", s))
	sw(0, 4)
	ck(s[0] == 9 && s[4] == 5, "Swapper restore")
	ck(sv.Len() == 5 && sv.Cap() == 5, "slice len/cap")
	live = append(live, s)

	// 4) Convert / Overflow / Comparable / Equal。
	intType := reflect.TypeOf(int(0))
	f64ToInt := reflect.ValueOf(3.9).Convert(intType)
	ck(f64ToInt.Int() == 3, fmt.Sprintf("float64->int convert=%d", f64ToInt.Int()))
	bigI64 := int64(1) << 40
	i64ToI32 := reflect.ValueOf(bigI64).Convert(reflect.TypeOf(int32(0)))
	ck(i64ToI32.Int() == int64(int32(bigI64)), "int64->int32 truncation")
	bs := reflect.ValueOf([]byte("hi")).Convert(reflect.TypeOf(""))
	ck(bs.Kind() == reflect.String && bs.String() == "hi", "[]byte->string convert")
	back := reflect.ValueOf("hé").Convert(reflect.TypeOf([]byte(nil)))
	ck(back.Kind() == reflect.Slice && len(back.Bytes()) == 3, fmt.Sprintf("string->[]byte len=%d", len(back.Bytes())))
	ck(reflect.TypeOf(0).ConvertibleTo(reflect.TypeOf(0.0)), "int convertible to float64")
	ck(!reflect.TypeOf([]int{}).ConvertibleTo(reflect.TypeOf("")), "slice not convertible to string")
	ck(reflect.ValueOf(int8(1)).OverflowInt(100) == false, "int8 100 overflows?")
	ck(reflect.ValueOf(int8(1)).OverflowInt(1000) == true, "int8 1000 must overflow")
	ck(reflect.TypeOf(1).Comparable() && !reflect.TypeOf([]int{}).Comparable(), "Comparable matrix")
	ck(!reflect.TypeOf(node{}).Comparable(), "node with map/slice not comparable")
	ck(reflect.ValueOf("a").Equal(reflect.ValueOf("a")), "Equal strings")
	ck(!reflect.ValueOf("a").Equal(reflect.ValueOf("b")), "Equal different strings")
	ck(panics(func() { reflect.ValueOf([]int{1}).Equal(reflect.ValueOf([]int{1})) }), "Equal slice must panic")
	ck(!reflect.ValueOf(node{Name: "x"}).Comparable(), "node Value.Comparable false")

	// 5) StructOf 动态结构体与 tag。
	typ := reflect.StructOf([]reflect.StructField{
		{Name: "ID", Type: reflect.TypeOf(int64(0)), Tag: `json:"id"`},
		{Name: "Label", Type: reflect.TypeOf("")},
		{Name: "Next", Type: reflect.TypeOf((*node)(nil))},
	})
	ck(typ.NumField() == 3, fmt.Sprintf("dynamic struct fields=%d", typ.NumField()))
	ck(typ.Field(0).Tag.Get("json") == "id", "dynamic tag")
	dv := reflect.New(typ).Elem()
	dv.FieldByName("ID").SetInt(7)
	dv.FieldByName("Label").SetString("dyn")
	dv.FieldByName("Next").Set(reflect.ValueOf(a))
	ck(typ.Field(0).Offset < typ.Field(1).Offset && typ.Field(1).Offset < typ.Field(2).Offset, "dynamic field offsets")
	ck(dv.FieldByName("ID").Int() == 7 && dv.FieldByName("Label").String() == "dyn", "dynamic field values")
	ck(dv.FieldByName("Next").Interface().(*node) == a, "dynamic pointer field")
	dynIface := dv.Interface()
	dt := reflect.TypeOf(dynIface)
	ck(dt == typ && reflect.ValueOf(dynIface).FieldByName("ID").Int() == 7, "dynamic struct roundtrip")
	live = append(live, dv.Interface(), dynIface)

	// 6) 每轮 GC 后检查拷贝图与反射值仍有效。
	for round := 0; round < 3; round++ {
		gcStress()
		ck(cpRoot.Next.Next == cpRoot, fmt.Sprintf("round %d cycle after GC", round))
		ck(cpRoot.Kids[0] == cpRoot.Next, fmt.Sprintf("round %d sharing after GC", round))
		ck(cpRoot.Next.Value == 99, fmt.Sprintf("round %d copied value after GC", round))
		ck(root.Tags["r"] == "1" && a.Value == 1, fmt.Sprintf("round %d original after GC", round))
		ck(reflect.ValueOf(cpRoot).Elem().FieldByName("Value").Int() == 11, fmt.Sprintf("round %d reflect read after GC", round))
		it2 := reflect.ValueOf(m).MapRange()
		n := 0
		for it2.Next() {
			n++
		}
		ck(n == 3, fmt.Sprintf("round %d map len=%d", round, n))
		// 种子决定本轮 map 抽查顺序。
		probe := []string{"a", "b"}
		for _, i := range rng.Perm(2) {
			ck(m[probe[i]] == a || m[probe[i]] == b, fmt.Sprintf("round %d map probe %s", round, probe[i]))
		}
	}

	finish("l16_reflect_value_ops")
}
