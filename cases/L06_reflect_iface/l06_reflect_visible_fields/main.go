// L06: reflect.VisibleFields：嵌入提升/遮蔽/同深度歧义、Index 路径与 FieldByName 一致性，跨 GC。
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

type BaseA struct {
	ID     int64
	Shared string
}

type BaseB struct {
	ID    int64
	Other string
}

type Mid struct {
	BaseA
	Extra  int64
	hidden int64
	Labeler
}

type Top struct {
	Mid
	*BaseB
	Name   string
	Shared string
}

type Amb struct {
	BaseA
	BaseB
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
	return &Top{
		Mid: Mid{
			BaseA:   BaseA{ID: base + 99, Shared: "inner-shared"},
			Extra:   base + 23,
			hidden:  base + 37,
			Labeler: &labelImpl{S: fmt.Sprintf("label-%d", base)},
		},
		BaseB:  &BaseB{ID: base + 11, Other: fmt.Sprintf("other-%d", base)},
		Name:   "top-name",
		Shared: "outer-shared",
	}
}

func main() {
	uafSeed = envSeed()
	base := uafSeed%13 + 7

	t := reflect.TypeOf(Top{})
	vf := reflect.VisibleFields(t)

	// 1) 可见字段序列与 Index 路径（含匿名字段本身）。
	wantNames := []string{"Mid", "BaseA", "Extra", "hidden", "Labeler", "BaseB", "ID", "Other", "Name", "Shared"}
	wantIdx := [][]int{{0}, {0, 0}, {0, 1}, {0, 2}, {0, 3}, {1}, {1, 0}, {1, 1}, {2}, {3}}
	ck(t.NumField() == 4, "declared field count")
	ck(len(vf) == len(wantNames) && len(vf) > t.NumField(), "visible field count")
	if len(vf) == len(wantNames) {
		for i := range wantNames {
			ck(vf[i].Name == wantNames[i], "visible field order "+wantNames[i])
			ck(sameIndex(vf[i].Index, wantIdx[i]), "visible index path "+wantNames[i])
		}
	} else {
		ck(false, "visible fields length mismatch")
	}

	// 2) 匿名标记与类型。
	anon := map[string]bool{"Mid": true, "BaseA": true, "Labeler": true, "BaseB": true}
	for _, f := range vf {
		if f.Anonymous != anon[f.Name] {
			ck(false, "anonymous flag "+f.Name)
		}
	}
	ck(vf[5].Type.Kind() == reflect.Ptr && vf[5].Type.Elem() == reflect.TypeOf(BaseB{}), "embedded pointer field type")
	ck(vf[4].Type.Kind() == reflect.Interface && vf[4].Type.NumMethod() == 1, "embedded interface field type")

	// 3) FieldByName 与 VisibleFields 的路径一致。
	for _, f := range vf {
		got, ok := t.FieldByName(f.Name)
		ck(ok && sameIndex(got.Index, f.Index), "FieldByName matches visible "+f.Name)
	}
	shared, ok := t.FieldByName("Shared")
	ck(ok && sameIndex(shared.Index, []int{3}), "outer Shared shadows embedded Shared")
	idf, ok := t.FieldByName("ID")
	ck(ok && sameIndex(idf.Index, []int{1, 0}), "shallower BaseB.ID shadows deeper BaseA.ID")

	// 4) 按 VisibleFields 的 Index 读取与写入。
	tp := buildTop(base)
	v := reflect.ValueOf(tp).Elem()
	for _, f := range vf {
		fv := v.FieldByIndex(f.Index)
		if !fv.IsValid() {
			ck(false, "FieldByIndex invalid "+f.Name)
			continue
		}
		if f.Name == "hidden" {
			ck(!f.IsExported() && fv.Int() == base+37 && !fv.CanInterface(), "unexported visible field read")
			continue
		}
		if f.IsExported() && !fv.CanInterface() {
			ck(false, "exported visible field not interfaceable "+f.Name)
		}
	}
	ck(v.FieldByIndex([]int{0, 0, 0}).Int() == base+99, "shadowed deeper BaseA.ID still reachable by path")
	ck(v.FieldByIndex([]int{1, 0}).Int() == base+11, "visible BaseB.ID value")
	ck(v.FieldByIndex([]int{0, 1}).Int() == base+23, "Extra value via visible path")
	ck(v.FieldByIndex([]int{2}).String() == "top-name", "Name value via visible path")
	ck(v.FieldByIndex([]int{3}).String() == "outer-shared", "Shared value via visible path")

	v.FieldByIndex([]int{1, 0}).SetInt(base + 44)
	ck(tp.BaseB.ID == base+44 && tp.Mid.BaseA.ID == base+99, "write visible promoted field only")
	lab := v.FieldByIndex([]int{0, 3}).Interface().(Labeler)
	ck(lab.Label() == fmt.Sprintf("label-%d", base), "embedded interface method via visible path")
	mv := v.MethodByName("Label")
	ck(mv.IsValid() && mv.Call(nil)[0].String() == fmt.Sprintf("label-%d", base), "promoted Label method")
	if _, ok := t.FieldByName("Label"); ok {
		ck(false, "method should not appear as a field")
	}

	// 5) 同深度同名歧义：Amb 的 ID 不可见，FieldByName 不解析。
	amb := reflect.VisibleFields(reflect.TypeOf(Amb{}))
	nameSet := make(map[string]bool, len(amb))
	for _, f := range amb {
		nameSet[f.Name] = true
	}
	ck(!nameSet["ID"], "ambiguous ID omitted from VisibleFields")
	ck(nameSet["BaseA"] && nameSet["BaseB"] && nameSet["Shared"] && nameSet["Other"], "non-conflicting promoted fields visible")
	if _, ok := reflect.TypeOf(Amb{}).FieldByName("ID"); ok {
		ck(false, "ambiguous FieldByName must fail")
	}

	// 6) 嵌入指针为 nil：VisibleFields 不受影响，按路径访问返回错误/panic。
	nilTop := &Top{}
	nv := reflect.ValueOf(nilTop).Elem()
	ck(len(reflect.VisibleFields(reflect.TypeOf(Top{}))) == 10, "VisibleFields ignores nil pointer")
	if _, err := nv.FieldByIndexErr([]int{1, 0}); err == nil {
		ck(false, "FieldByIndexErr on nil embedded pointer")
	}
	mustPanic("FieldByIndex through nil embedded pointer", func() { nv.FieldByIndex([]int{1, 0}) })

	// 7) 非 struct 类型调用 VisibleFields 必须 panic。
	mustPanic("VisibleFields non-struct", func() { reflect.VisibleFields(reflect.TypeOf(1)) })

	// 8) 仅由接口保活，GC 后沿 VisibleFields 路径逐个回读。
	anySink = tp
	tp = nil
	gcStress()
	v2 := reflect.ValueOf(anySink).Elem()
	vf2 := reflect.VisibleFields(v2.Type())
	ck(len(vf2) == 10, "visible fields after GC")
	ck(v2.FieldByIndex([]int{1, 0}).Int() == base+44, "visible pointer-embedded field after GC")
	ck(v2.FieldByIndex([]int{1, 1}).String() == fmt.Sprintf("other-%d", base), "Other after GC")
	ck(v2.FieldByIndex([]int{2}).String() == "top-name" && v2.FieldByIndex([]int{3}).String() == "outer-shared", "strings after GC")
	ck(v2.FieldByIndex([]int{0, 2}).Int() == base+37, "hidden after GC")
	ck(v2.FieldByIndex([]int{0, 3}).Interface().(Labeler).Label() == fmt.Sprintf("label-%d", base), "Labeler after GC")
	ck(v2.FieldByIndex([]int{0, 0, 0}).Int() == base+99, "shadowed deeper ID after GC")

	finish("l06_reflect_visible_fields")
}
