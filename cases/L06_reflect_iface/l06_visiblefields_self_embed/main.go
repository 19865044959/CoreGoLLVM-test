// L06: VisibleFields 自引用嵌入：*loop 嵌入自身时只列一层匿名字段与提升字段，验证遮蔽、
// 同深度歧义、嵌入接口、FieldByName 路径一致、nil 嵌入指针错误与指针类型 panic。
package main

import (
	"fmt"
	"os"
	"reflect"
	"runtime"
	"strconv"
	"sync/atomic"
)

type loop struct {
	*loop
	V int64 `bind:"v"`
}

type outer struct {
	*loop
	Name string `bind:"name"`
}

type dup1 struct {
	X int `bind:"x1"`
}

type dup2 struct {
	X int `bind:"x2"`
}

type ambiguous struct {
	dup1
	dup2
}

type labeler interface {
	Label() string
}

type leafLabel struct {
	s string
}

func (l *leafLabel) Label() string { return l.s }

type withIface struct {
	labeler
	Y int64 `bind:"y"`
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

// bindPlan 从 VisibleFields 生成确定性绑定计划（导出、非匿名、bind tag 非空）。
func bindPlan(t reflect.Type) []string {
	plan := make([]string, 0, 8)
	for _, f := range reflect.VisibleFields(t) {
		if f.Anonymous || f.PkgPath != "" {
			continue
		}
		tag, ok := f.Tag.Lookup("bind")
		if !ok || tag == "" || tag == "-" {
			continue
		}
		plan = append(plan, fmt.Sprintf("%s@%v=%s", f.Name, f.Index, tag))
	}
	return plan
}

func planEqual(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}

func main() {
	uafSeed = envSeed()
	base := uafSeed * 10
	loopT := reflect.TypeOf(loop{})
	int64T := reflect.TypeOf(int64(0))

	// 自引用嵌入：只出现一层匿名 *loop 与提升 V，不会无限展开。
	vf := reflect.VisibleFields(loopT)
	ck(len(vf) == 2, "self-embedded VisibleFields length")
	if len(vf) == 2 {
		ck(vf[0].Name == "loop" && sameIndex(vf[0].Index, []int{0}) && vf[0].Anonymous,
			"self-embedded anonymous field")
		ck(vf[0].Type == reflect.PointerTo(loopT), "self-embedded field type")
		ck(vf[1].Name == "V" && sameIndex(vf[1].Index, []int{1}) && !vf[1].Anonymous,
			"self-embedded promoted field")
		ck(vf[1].Type == int64T && vf[1].Tag.Get("bind") == "v", "self-embedded promoted metadata")
	}

	// 外层再次嵌入 loop：V 的路径变为 [0,1]，Name 为 [1]。
	outerT := reflect.TypeOf(outer{})
	vf = reflect.VisibleFields(outerT)
	ck(len(vf) == 3, "outer VisibleFields length")
	if len(vf) == 3 {
		ck(vf[0].Name == "loop" && sameIndex(vf[0].Index, []int{0}), "outer anonymous loop")
		ck(vf[1].Name == "V" && sameIndex(vf[1].Index, []int{0, 1}), "outer promoted V path")
		ck(vf[2].Name == "Name" && sameIndex(vf[2].Index, []int{1}), "outer direct Name")
	}
	for _, f := range vf {
		sf, ok := outerT.FieldByName(f.Name)
		ck(ok && sameIndex(sf.Index, f.Index), "outer FieldByName matches VisibleFields: "+f.Name)
	}

	// 同深度歧义字段不进入 VisibleFields，FieldByName 也失败。
	ambT := reflect.TypeOf(ambiguous{})
	vf = reflect.VisibleFields(ambT)
	ck(len(vf) == 2, "ambiguous VisibleFields only embedded fields")
	if len(vf) == 2 {
		ck(vf[0].Name == "dup1" && vf[1].Name == "dup2", "ambiguous embedded field names")
	}
	for _, f := range vf {
		ck(f.Name != "X", "ambiguous X is not visible")
	}
	_, ok := ambT.FieldByName("X")
	ck(!ok, "ambiguous FieldByName fails")

	// 嵌入接口字段：Anonymous、接口类型与 PkgPath 正确。
	ifaceT := reflect.TypeOf((*labeler)(nil)).Elem()
	vf = reflect.VisibleFields(reflect.TypeOf(withIface{}))
	ck(len(vf) == 2, "embedded interface VisibleFields length")
	if len(vf) == 2 {
		ck(vf[0].Name == "labeler" && vf[0].Anonymous && vf[0].Type == ifaceT,
			"embedded interface field")
		ck(vf[0].PkgPath == "main", "embedded unexported interface PkgPath")
		ck(vf[1].Name == "Y" && sameIndex(vf[1].Index, []int{1}), "field after embedded interface")
	}

	// VisibleFields 只接受 struct 类型；指针与标量都必须 panic。
	func() {
		defer func() { ck(recover() != nil, "VisibleFields on pointer panics") }()
		reflect.VisibleFields(reflect.PointerTo(loopT))
	}()
	func() {
		defer func() { ck(recover() != nil, "VisibleFields on non-struct panics") }()
		reflect.VisibleFields(int64T)
	}()

	// 绑定计划：外层只包含 V 与 Name，loop 只含 V，withIface 只含 Y。
	ck(planEqual(bindPlan(loopT), []string{"V@[1]=v"}), "loop bind plan")
	ck(planEqual(bindPlan(outerT), []string{"V@[0 1]=v", "Name@[1]=name"}), "outer bind plan")
	ck(planEqual(bindPlan(reflect.TypeOf(withIface{})), []string{"Y@[1]=y"}), "iface bind plan")
	ck(planEqual(bindPlan(ambT), nil), "ambiguous bind plan excludes X")

	// 自引用嵌套值：顶层 V 遮蔽深层 V，FieldByIndexErr 可取到深层。
	top := &loop{V: base}
	top.loop = &loop{V: base + 1}
	tv, err := reflect.ValueOf(top).Elem().FieldByIndexErr([]int{0, 1})
	ck(err == nil && tv.Int() == base+1, "nested self-embedded V")
	shallow, err := reflect.ValueOf(top).Elem().FieldByIndexErr([]int{1})
	ck(err == nil && shallow.Int() == base, "shallow V shadows nested")
	fbv := reflect.ValueOf(top).Elem().FieldByName("V")
	ck(fbv.IsValid() && fbv.Int() == base, "FieldByName returns shallow V")
	_, err = reflect.ValueOf(top).Elem().FieldByIndexErr([]int{0, 0, 1})
	ck(err != nil, "deeper nil embedded pointer reports error")

	// nil 嵌入指针：Err 版本报错，非 Err 版本 panic。
	nl := &loop{V: base + 2}
	_, err = reflect.ValueOf(nl).Elem().FieldByIndexErr([]int{0, 1})
	ck(err != nil, "nil embedded self pointer reports error")
	func() {
		defer func() { ck(recover() != nil, "FieldByIndex through nil self pointer panics") }()
		reflect.ValueOf(nl).Elem().FieldByIndex([]int{0, 1})
	}()

	// 只保留接口根与类型，GC 后重读。
	anySink = top
	top = nil
	gcStress()
	got := anySink.(*loop)
	ck(got.V == base, "self-embedded root after GC")
	ck(got.loop != nil && got.loop.V == base+1, "nested self pointer after GC")
	vf2 := reflect.VisibleFields(reflect.TypeOf(got).Elem())
	ck(len(vf2) == 2 && vf2[0].Name == "loop" && vf2[1].Name == "V", "VisibleFields stable after GC")
	ck(vf2[1].Tag.Get("bind") == "v" && vf2[1].Type == int64T, "visible field metadata after GC")
	ck(planEqual(bindPlan(outerT), []string{"V@[0 1]=v", "Name@[1]=name"}), "bind plan after GC")

	finish("l06_visiblefields_self_embed")
}
