// L06: reflect 深拷贝含指针的 struct/slice/map，浅/深语义明确，GC 后校验。
package main

import (
	"fmt"
	"os"
	"reflect"
	"runtime"
	"strconv"
	"sync/atomic"
)

type leaf struct {
	V     int64
	Label string
}

type inner struct {
	Score int64
	Tag   *leaf
	Items []*leaf
}

type doc struct {
	ID   int64
	Name string
	Tag  *leaf
	Tags []*leaf
	Grid [][]int64
	Attr map[string]*leaf
	In   inner
}

type link struct {
	V    int64
	Next *link
}

type memoKey struct {
	typ reflect.Type
	ptr uintptr
}

var (
	failed   int32
	gcSink   [][]byte
	copySink doc
	uafSeed  int64 = 1
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

func envInt(name string, def int) int {
	s := os.Getenv(name)
	if s == "" {
		return def
	}
	n, err := strconv.Atoi(s)
	if err != nil || n <= 0 {
		return def
	}
	if n > 50000 {
		n = 50000
	}
	return n
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

// deepCopy 递归复制 Ptr/Slice/Array/Map/Struct/Interface，见 memo；
// 基础类型值不可变，直接返回（与浅拷贝共享）。
func deepCopy(v reflect.Value, memo map[memoKey]reflect.Value) reflect.Value {
	if !v.IsValid() {
		return v
	}
	switch v.Kind() {
	case reflect.Interface:
		if v.IsNil() {
			return reflect.Zero(v.Type())
		}
		out := reflect.New(v.Type()).Elem()
		out.Set(deepCopy(v.Elem(), memo))
		return out
	case reflect.Ptr:
		if v.IsNil() {
			return reflect.Zero(v.Type())
		}
		key := memoKey{typ: v.Type(), ptr: v.Pointer()}
		if cp, ok := memo[key]; ok {
			return cp
		}
		out := reflect.New(v.Type().Elem())
		memo[key] = out
		out.Elem().Set(deepCopy(v.Elem(), memo))
		return out
	case reflect.Slice:
		if v.IsNil() {
			return reflect.Zero(v.Type())
		}
		out := reflect.MakeSlice(v.Type(), v.Len(), v.Len())
		for i := 0; i < v.Len(); i++ {
			out.Index(i).Set(deepCopy(v.Index(i), memo))
		}
		return out
	case reflect.Array:
		out := reflect.New(v.Type()).Elem()
		for i := 0; i < v.Len(); i++ {
			out.Index(i).Set(deepCopy(v.Index(i), memo))
		}
		return out
	case reflect.Map:
		if v.IsNil() {
			return reflect.Zero(v.Type())
		}
		out := reflect.MakeMapWithSize(v.Type(), v.Len())
		iter := v.MapRange()
		for iter.Next() {
			out.SetMapIndex(deepCopy(iter.Key(), memo), deepCopy(iter.Value(), memo))
		}
		return out
	case reflect.Struct:
		out := reflect.New(v.Type()).Elem()
		for i := 0; i < v.NumField(); i++ {
			if v.Type().Field(i).PkgPath != "" {
				continue
			}
			out.Field(i).Set(deepCopy(v.Field(i), memo))
		}
		return out
	default:
		return v
	}
}

func buildDoc(base int64, n int) doc {
	tag := &leaf{V: base + 1, Label: "tag"}
	tags := make([]*leaf, n)
	for i := 0; i < n; i++ {
		tags[i] = &leaf{V: base + 10 + int64(i), Label: "t"}
	}
	return doc{
		ID:   base,
		Name: "doc",
		Tag:  tag,
		Tags: tags,
		Grid: [][]int64{{1, 2, 3}, {4, 5, 6}, {7, 8, 9}},
		Attr: map[string]*leaf{
			"a": &leaf{V: base + 20, Label: "a"},
			"b": &leaf{V: base + 21, Label: "b"},
			"c": &leaf{V: base + 22, Label: "c"},
			"d": &leaf{V: base + 23, Label: "d"},
		},
		In: inner{
			Score: base + 2,
			Tag:   &leaf{V: base + 30, Label: "in"},
			Items: []*leaf{&leaf{V: base + 40, Label: "ii"}},
		},
	}
}

func main() {
	uafSeed = envSeed()
	rounds := envInt("TORTURE_ROUNDS", 1000)
	const nLeaf = 120

	for r := 0; r < rounds; r++ {
		base := int64(r * 1000)
		orig := buildDoc(base, nLeaf)
		cp := deepCopy(reflect.ValueOf(orig), make(map[memoKey]reflect.Value)).Interface().(doc)
		copySink = cp
		gcStress()

		ck(cp.ID == orig.ID && cp.Name == "doc", "deep copy scalars")
		ck(cp.Tag != nil && cp.Tag.V == base+1 && cp.Tag.Label == "tag", "deep copy tag")
		ck(len(cp.Tags) == nLeaf && cp.Tags[0].V == base+10, "deep copy tags length/value")
		ck(len(cp.Attr) == 4 && cp.Attr["a"].V == base+20, "deep copy map")
		ck(len(cp.Grid) == 3 && len(cp.Grid[0]) == 3 && cp.Grid[2][2] == 9, "deep copy grid")
		ck(cp.In.Score == base+2 && cp.In.Tag.V == base+30 && cp.In.Items[0].V == base+40, "deep copy inner")
		ck(cp.Tag != orig.Tag, "deep copy pointer identity differs")
		ck(&cp.Tags[0] != &orig.Tags[0], "deep copy slice element pointer differs")

		// 深拷贝后修改原对象，副本不受影响。
		orig.Tag.V += 100
		orig.Tags[0].V += 100
		orig.Attr["a"].V += 100
		orig.Grid[0][0] += 100
		orig.In.Tag.V += 100
		orig.In.Items[0].V += 100
		gcStress()
		ck(cp.Tag.V == base+1, "deep copy tag independent")
		ck(cp.Tags[0].V == base+10, "deep copy slice element independent")
		ck(cp.Attr["a"].V == base+20, "deep copy map value independent")
		ck(cp.Grid[0][0] == 1, "deep copy grid independent")
		ck(cp.In.Tag.V == base+30, "deep copy inner tag independent")
		ck(cp.In.Items[0].V == base+40, "deep copy inner items independent")

		// 浅拷贝语义：普通赋值共享指针与底层数组。
		shallow := orig
		orig.Tags[1].V += 7
		orig.Attr["b"].V += 7
		orig.Grid[1][1] += 7
		ck(shallow.Tag == orig.Tag, "shallow copy shares tag pointer")
		ck(shallow.Tags[1].V == orig.Tags[1].V, "shallow copy shares slice elements")
		ck(shallow.Attr["b"].V == orig.Attr["b"].V, "shallow copy shares map values")
		ck(shallow.Grid[1][1] == orig.Grid[1][1], "shallow copy shares grid rows")
		ck(cp.Tags[1].V != orig.Tags[1].V, "deep copy still independent after shallow mutation")
		gcStress()
		ck(copySink.Tags[0].V == base+10 && copySink.Attr["a"].V == base+20, "deep copy survives GC")
	}

	// 指针环：深拷贝必须保留环结构且不与原图共享节点。
	a := &link{V: 1}
	b := &link{V: 2}
	a.Next = b
	b.Next = a
	cc := deepCopy(reflect.ValueOf(a), make(map[memoKey]reflect.Value)).Interface().(*link)
	gcStress()
	ck(cc.V == 1 && cc.Next.V == 2 && cc.Next.Next == cc, "deep copy preserves pointer cycle")
	cc.Next.V = 20
	ck(b.V == 2, "copied cycle independent from original")
	ck(a.Next == b && b.Next == a, "original cycle unchanged")
	finish("l06_reflect_deep_copy")
}
