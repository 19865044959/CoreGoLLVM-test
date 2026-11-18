// L06: reflect 匿名嵌入字段：提升/遮蔽/歧义、FieldByIndex 路径、嵌入指针与嵌入接口，跨 GC。
package main

import (
	"fmt"
	"os"
	"reflect"
	"runtime"
	"strconv"
	"sync/atomic"
)

type Labeler interface{ Label() string }

type labelImpl struct{ S string }

func (l *labelImpl) Label() string { return l.S }

type Base struct {
	ID     int64
	Shared string
	Tag    *string
}

func (b Base) BaseVal() int64 { return b.ID * 2 }

type Mid struct {
	Base
	Extra  int64
	hidden int64
	Labeler
}

type Leaf struct {
	Level int64
	Ptr   *int64
}

type Top struct {
	Mid
	*Leaf
	Name   string
	Shared string
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

func sameIndex(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func buildTop(base int64) *Top {
	tagText := fmt.Sprintf("tag-%d", base)
	lv := base + 40
	return &Top{
		Mid: Mid{
			Base:    Base{ID: base + 11, Shared: "mid-shared", Tag: &tagText},
			Extra:   base + 23,
			hidden:  base + 37,
			Labeler: &labelImpl{S: fmt.Sprintf("label-%d", base)},
		},
		Leaf:   &Leaf{Level: base + 51, Ptr: &lv},
		Name:   "top-name",
		Shared: "outer-shared",
	}
}

func main() {
	uafSeed = envSeed()
	base := uafSeed%11 + 5

	tp := buildTop(base)
	t := reflect.TypeOf(Top{})
	ck(t.NumField() == 4, "top field count")
	ck(t.Field(0).Anonymous && t.Field(0).Name == "Mid", "embedded Mid")
	ck(t.Field(1).Anonymous && t.Field(1).Name == "Leaf" && t.Field(1).Type.Kind() == reflect.Ptr, "embedded *Leaf")
	ck(!t.Field(2).Anonymous && !t.Field(3).Anonymous, "named fields")

	// 1) FieldByName 的 Index 路径：提升字段、遮蔽、未导出字段与嵌入接口。
	idf, ok := t.FieldByName("ID")
	ck(ok && sameIndex(idf.Index, []int{0, 0, 0}), "promoted ID index path")
	extraf, ok := t.FieldByName("Extra")
	ck(ok && sameIndex(extraf.Index, []int{0, 1}), "promoted Extra index path")
	hiddenf, ok := t.FieldByName("hidden")
	ck(ok && sameIndex(hiddenf.Index, []int{0, 2}) && !hiddenf.IsExported(), "unexported embedded field index")
	labf, ok := t.FieldByName("Labeler")
	ck(ok && sameIndex(labf.Index, []int{0, 3}) && labf.Anonymous && labf.Type.Kind() == reflect.Interface, "embedded interface field")
	levf, ok := t.FieldByName("Level")
	ck(ok && sameIndex(levf.Index, []int{1, 0}), "promoted through embedded pointer")
	ptrf, ok := t.FieldByName("Ptr")
	ck(ok && sameIndex(ptrf.Index, []int{1, 1}), "pointer field through embedded pointer")
	sharedf, ok := t.FieldByName("Shared")
	ck(ok && sameIndex(sharedf.Index, []int{3}) && sharedf.Index[0] == 3, "outer Shared shadows embedded")
	if _, ok := t.FieldByName("Missing"); ok {
		ck(false, "FieldByName missing field should not resolve")
	}
	if _, ok := t.FieldByName("Label"); ok {
		ck(false, "Label is a method, not a field")
	}

	v := reflect.ValueOf(tp).Elem()
	ck(v.Kind() == reflect.Struct && v.CanSet(), "addressable struct value")

	// 2) 通过 FieldByIndex 写入提升字段，类型化视图与反射视图一致。
	v.FieldByIndex([]int{0, 0, 0}).SetInt(base + 77)
	ck(tp.Mid.Base.ID == base+77, "set promoted ID via FieldByIndex")
	ck(v.FieldByName("ID").Int() == base+77, "FieldByName sees promoted write")
	v.FieldByIndex([]int{0, 1}).SetInt(base + 88)
	ck(tp.Mid.Extra == base+88, "set promoted Extra via FieldByIndex")
	ck(v.FieldByIndex([]int{0, 2}).Int() == base+37, "read hidden unexported field")
	hf := v.FieldByIndex([]int{0, 2})
	ck(!hf.CanInterface() && !hf.CanSet(), "unexported field not interfaceable/settable")
	mustPanic("set unexported embedded field", func() { hf.SetInt(1) })

	// 3) 嵌入指针路径与 nil 嵌入指针的错误语义。
	ck(v.FieldByIndex([]int{1, 0}).Int() == base+51, "read through embedded pointer")
	v.FieldByIndex([]int{1, 1}).Elem().SetInt(base + 99)
	ck(tp.Leaf.Ptr != nil && *tp.Leaf.Ptr == base+99, "write pointee through embedded pointer")
	ck(v.FieldByIndex([]int{0, 0, 2}).Elem().String() == fmt.Sprintf("tag-%d", base), "read Tag through two embeddings")

	nilTop := &Top{}
	nv := reflect.ValueOf(nilTop).Elem()
	if _, err := nv.FieldByIndexErr([]int{1, 0}); err == nil {
		ck(false, "FieldByIndexErr should report nil embedded pointer")
	}
	mustPanic("FieldByIndex through nil embedded pointer", func() { nv.FieldByIndex([]int{1, 0}) })

	// 4) 嵌入接口字段与提升方法。
	lf := v.FieldByIndex([]int{0, 3})
	ck(lf.CanInterface(), "exported embedded interface interfaceable")
	lab, ok := lf.Interface().(Labeler)
	ck(ok && lab != nil && lab.Label() == fmt.Sprintf("label-%d", base), "call embedded interface method")
	mv := v.MethodByName("Label")
	ck(mv.IsValid(), "promoted Label method visible to reflect")
	if mv.IsValid() {
		ck(len(mv.Call(nil)) == 1 && mv.Call(nil)[0].String() == fmt.Sprintf("label-%d", base), "call promoted interface method")
	}
	bv := v.MethodByName("BaseVal")
	ck(bv.IsValid(), "promoted BaseVal method visible")
	if bv.IsValid() {
		ck(bv.Call(nil)[0].Int() == (base+77)*2, "promoted value-receiver method result")
	}

	// 5) 外层字段遮蔽嵌入字段；改写外层不影响内层。
	v.FieldByIndex(sharedf.Index).SetString("outer-changed")
	ck(tp.Shared == "outer-changed" && tp.Mid.Base.Shared == "mid-shared", "outer field shadows embedded")

	// 6) 丢弃类型化根，仅靠接口保活，GC 后按 Index 路径回读。
	anySink = tp
	tp = nil
	gcStress()
	tp2 := anySink.(*Top)
	ck(tp2.Mid.Base.ID == base+77 && tp2.Mid.Extra == base+88, "promoted scalar values after GC")
	ck(tp2.Leaf != nil && tp2.Leaf.Level == base+51 && tp2.Leaf.Ptr != nil && *tp2.Leaf.Ptr == base+99, "embedded pointer fields after GC")
	ck(tp2.Mid.Base.Tag != nil && *tp2.Mid.Base.Tag == fmt.Sprintf("tag-%d", base), "Tag pointer after GC")
	ck(tp2.Shared == "outer-changed" && tp2.Mid.Base.Shared == "mid-shared", "shadowed strings after GC")
	v2 := reflect.ValueOf(anySink).Elem()
	ck(v2.FieldByIndex([]int{0, 0, 0}).Int() == base+77, "reflect promoted field after GC")
	ck(v2.FieldByIndex([]int{0, 3}).Interface().(Labeler).Label() == fmt.Sprintf("label-%d", base), "embedded interface after GC")
	ck(v2.MethodByName("BaseVal").Call(nil)[0].Int() == (base+77)*2, "promoted method after GC")

	finish("l06_reflect_embedded_fields")
}
