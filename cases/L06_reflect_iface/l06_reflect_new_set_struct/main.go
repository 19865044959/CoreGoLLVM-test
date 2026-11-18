// L06: reflect.New/Field.Set/MakeSlice/MakeMap 构造复杂 struct，GC 后与类型化视图互证。
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

type part struct {
	Weight int64
	Tag    *node
	Codes  []*node
}

type namedNode struct {
	*node
}

func (n *namedNode) String() string { return n.Label }

type complexS struct {
	ID     int64
	Name   string
	Tag    *node
	Part   part
	Items  []*node
	Attr   map[string]*node
	Grid   [2][3]int64
	Iface  fmt.Stringer
	Fn     func(int64) int64
	Flag   bool
	hidden int64
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

func main() {
	uafSeed = envSeed()
	const rounds = 16

	for r := 0; r < rounds; r++ {
		rnd := rand.New(rand.NewSource(uafSeed + int64(r)*131))
		base := int64(r * 100)
		t := reflect.TypeOf(complexS{})
		rv := reflect.New(t)
		e := rv.Elem()

		tag := &node{V: base + 7, Label: fmt.Sprintf("tag%d", rnd.Intn(9))}
		e.FieldByName("Tag").Set(reflect.ValueOf(tag))
		e.FieldByName("ID").SetInt(base + 11)
		e.FieldByName("Name").SetString(fmt.Sprintf("obj-%d", rnd.Intn(5)))
		e.FieldByName("Flag").SetBool(true)

		pt := reflect.TypeOf(part{})
		pv := reflect.New(pt).Elem()
		pv.FieldByName("Weight").SetInt(base + 13)
		pv.FieldByName("Tag").Set(reflect.ValueOf(&node{V: base + 17, Label: "part"}))
		ct := reflect.TypeOf([]*node{})
		codes := reflect.MakeSlice(ct, 0, 2)
		codes = reflect.Append(codes, reflect.ValueOf(&node{V: base + 19, Label: "c0"}))
		codes = reflect.Append(codes, reflect.ValueOf(&node{V: base + 23, Label: "c1"}))
		pv.FieldByName("Codes").Set(codes)
		e.FieldByName("Part").Set(pv)

		items := reflect.MakeSlice(ct, 0, 6)
		var itemsSum int64
		for i := 0; i < 4; i++ {
			p := &node{V: base + int64(31+i*3), Label: fmt.Sprintf("it%d", i)}
			itemsSum += p.V
			items = reflect.Append(items, reflect.ValueOf(p))
		}
		e.FieldByName("Items").Set(items)

		mt := reflect.TypeOf(map[string]*node{})
		am := reflect.MakeMapWithSize(mt, 4)
		var attrSum int64
		keys := []string{"a", "b", "c"}
		for i, k := range rnd.Perm(len(keys)) {
			p := &node{V: base + int64(101+i*7), Label: keys[k]}
			am.SetMapIndex(reflect.ValueOf(keys[k]), reflect.ValueOf(p))
			attrSum += p.V
		}
		e.FieldByName("Attr").Set(am)

		for row := 0; row < 2; row++ {
			for col := 0; col < 3; col++ {
				e.FieldByName("Grid").Index(row).Index(col).SetInt(int64(row*10 + col))
			}
		}
		e.FieldByName("Iface").Set(reflect.ValueOf(&namedNode{&node{V: base + 41, Label: "iface"}}))
		e.FieldByName("Fn").Set(reflect.ValueOf(func(x int64) int64 { return x*10 + tag.V }))

		ck(!e.FieldByName("hidden").CanSet(), "unexported field not settable via reflect")

		obj := rv.Interface().(*complexS)
		anySink = obj
		gcStress()

		ck(obj.ID == base+11 && obj.Flag, "scalar fields set via reflect")
		ck(obj.Tag != nil && obj.Tag.V == base+7 && obj.Tag.Label == tag.Label, "pointer field set via reflect")
		ck(obj.Part.Weight == base+13 && obj.Part.Tag.V == base+17, "nested struct set via reflect")
		ck(len(obj.Part.Codes) == 2 && obj.Part.Codes[1].Label == "c1", "nested slice of pointers after GC")
		ck(len(obj.Items) == 4 && obj.Items[3].V == base+40, "slice built with reflect.Append after GC")
		var gotItems int64
		for _, p := range obj.Items {
			gotItems += p.V
		}
		ck(gotItems == itemsSum, "slice element values survive GC")
		ck(len(obj.Attr) == 3, "map set via reflect")
		var gotAttr int64
		for _, p := range obj.Attr {
			gotAttr += p.V
		}
		ck(gotAttr == attrSum, "map pointer values survive GC")
		ck(obj.Grid[1][2] == 12 && obj.Grid[0][1] == 1, "array field via reflect.Index")
		ck(obj.Iface.String() == "iface", "interface field set via reflect")
		ck(obj.Fn(4) == 40+base+7, "func field closure survives GC")
		ck(obj.hidden == 0, "unexported field remains zero")

		// 通过反射改字段，再与类型化视图互证。
		e.FieldByName("Name").SetString("changed")
		e.FieldByName("Tag").Elem().FieldByName("V").SetInt(base + 70)
		ck(obj.Name == "changed" && obj.Tag.V == base+70, "reflect write visible in typed view")
		obj.Items[0].Label = "typed"
		ck(e.FieldByName("Items").Index(0).Elem().FieldByName("Label").String() == "typed", "typed write visible via reflect")

		// 整体 Set 另一个复杂 struct：值拷贝但指针字段共享。
		src := complexS{
			ID:     base + 501,
			Name:   "src",
			Tag:    &node{V: base + 502, Label: "s-tag"},
			Part:   part{Weight: base + 503, Tag: &node{V: base + 504}, Codes: []*node{{V: base + 505}}},
			Items:  []*node{{V: base + 506}, {V: base + 507}},
			Attr:   map[string]*node{"x": {V: base + 508}},
			Grid:   [2][3]int64{{1, 2, 3}, {4, 5, 6}},
			Iface:  &namedNode{&node{Label: "s-iface"}},
			Fn:     func(x int64) int64 { return x - 1 },
			Flag:   true,
			hidden: 4242,
		}
		rv2 := reflect.New(t)
		rv2.Elem().Set(reflect.ValueOf(src))
		obj2 := rv2.Interface().(*complexS)
		gcStress()
		ck(obj2.ID == src.ID && obj2.hidden == 4242, "whole-struct Set copies scalars incl. unexported")
		ck(obj2.Tag == src.Tag && obj2.Items[1] == src.Items[1], "whole-struct Set shares pointer fields")
		ck(obj2.Attr["x"] == src.Attr["x"] && obj2.Part.Codes[0] == src.Part.Codes[0], "map/slice pointers shared")
		obj2.Tag.V = -1
		obj2.Attr["x"].V = -2
		ck(src.Tag.V == -1 && src.Attr["x"].V == -2, "mutation through copy affects shared pointers")
		obj2.Fn = nil
		ck(src.Fn(5) == 4, "func field copied independently")

		// reflect.Zero 清空指针字段。
		rv3 := reflect.New(t)
		rv3.Elem().FieldByName("Tag").Set(reflect.ValueOf(&node{V: 1}))
		ck(rv3.Elem().FieldByName("Tag").IsNil() == false, "pointer field pre-set")
		rv3.Elem().FieldByName("Tag").Set(reflect.Zero(rv3.Elem().FieldByName("Tag").Type()))
		ck(rv3.Elem().FieldByName("Tag").IsNil(), "reflect.Zero clears pointer field")

		anySink = obj2
		gcStress()
		ck(obj2.Tag.V == -1 && obj2.Attr["x"].V == -2, "shared pointers survive GC")
		ck(obj2.Part.Codes[0].V == base+505, "nested pointer slice survives GC")
	}
	finish("l06_reflect_new_set_struct")
}
