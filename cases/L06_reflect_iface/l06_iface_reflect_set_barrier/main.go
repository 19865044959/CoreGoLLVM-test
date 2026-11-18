// L06: 反射写接口槽：把不同动态类型的具体值写进 old 对象里的 any/Stringer/自定义接口字段，
// 丢弃局部根后 GC，验证接口 type/data 字替换、写屏障与动态派发。
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

type payload struct {
	V    int64
	Name string
	Tag  *node
}

func (p *payload) String() string {
	if p == nil {
		return "<nil-payload>"
	}
	return p.Name
}

type shaped interface {
	Shape() int64
}

type circle struct {
	R   int64
	Tag *node
}

func (c *circle) Shape() int64 {
	if c == nil {
		return -1
	}
	return c.R + c.Tag.V
}

type holder struct {
	Any   any
	Str   fmt.Stringer
	Sh    shaped
	Ptr   *payload
	Count int64
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

// installPayload 只通过接口字段建立对象引用；返回后局部指针不再存活。
func installPayload(hv reflect.Value, fv, tagV int64, label string) {
	hv.FieldByName("Any").Set(reflect.ValueOf(&payload{
		V:    fv,
		Name: label,
		Tag:  &node{V: tagV, Label: label},
	}))
}

func installStringer(hv reflect.Value, name string, tagV int64) {
	hv.FieldByName("Str").Set(reflect.ValueOf(&payload{Name: name, Tag: &node{V: tagV}}))
}

func installShaped(hv reflect.Value, r, tagV int64) {
	hv.FieldByName("Sh").Set(reflect.ValueOf(&circle{R: r, Tag: &node{V: tagV}}))
}

func main() {
	uafSeed = envSeed()
	h := &holder{}
	hv := reflect.ValueOf(h).Elem()
	anySink = h

	for r := int64(0); r < 6; r++ {
		// 先把 h 送进老年代，再写接口字段，覆盖 old->new 写屏障。
		gcStress()
		base := uafSeed*1000 + r*17

		installPayload(hv, base, base+1, fmt.Sprintf("p%d", r))
		gcStress()
		got, ok := hv.FieldByName("Any").Interface().(*payload)
		ck(ok && got != nil, "any field holds *payload after GC")
		if ok && got != nil {
			ck(got.V == base && got.Name == fmt.Sprintf("p%d", r), "any payload scalar after GC")
			ck(got.Tag != nil && got.Tag.V == base+1, "any payload tag pointer after GC")
		}

		installStringer(hv, fmt.Sprintf("s%d", r), base+2)
		gcStress()
		sf := hv.FieldByName("Str").Interface().(fmt.Stringer)
		ck(sf.String() == fmt.Sprintf("s%d", r), "fmt.Stringer slot dispatch after GC")

		installShaped(hv, base+3, base+4)
		gcStress()
		shv := hv.FieldByName("Sh")
		ck(shv.Interface().(shaped).Shape() == 2*base+7, "named interface field dispatch after GC")
		mv := shv.MethodByName("Shape")
		ck(mv.IsValid(), "reflect method on interface field")
		if mv.IsValid() {
			ck(mv.Call(nil)[0].Int() == 2*base+7, "reflect interface field method after GC")
		}

		// 接口槽原地换动态类型：指针 -> 字符串 -> 指针 -> nil -> 指针。
		anyField := hv.FieldByName("Any")
		anyField.Set(reflect.ValueOf("phase-string"))
		gcStress()
		ck(anyField.Interface().(string) == "phase-string", "any slot string dynamic type after GC")
		anyField.Set(reflect.ValueOf(&payload{V: base + 5, Name: "second"}))
		gcStress()
		second := anyField.Interface().(*payload)
		ck(second.V == base+5 && second.Tag == nil, "any slot replaced pointer after GC")
		anyField.Set(reflect.Zero(anyField.Type()))
		ck(anyField.IsNil() && anyField.Interface() == nil, "any slot zeroed to nil")
		gcStress()
		ck(anyField.IsNil(), "any slot stays nil after GC")

		// 独立可设置的接口槽，只由反射槽保活。
		slot := reflect.New(reflect.TypeOf((*any)(nil)).Elem()).Elem()
		slot.Set(reflect.ValueOf(&payload{V: base + 6, Name: "slot", Tag: &node{V: base + 7}}))
		anySink = slot
		gcStress()
		sp, ok := slot.Interface().(*payload)
		ck(ok && sp.V == base+6 && sp.Tag.V == base+7, "standalone any slot after GC")

		// 具体类型可直接写入接口字段；不满足接口的赋值必须 panic。
		hv.FieldByName("Str").Set(reflect.ValueOf(&payload{Name: "direct"}))
		gcStress()
		ck(hv.FieldByName("Str").Interface().(fmt.Stringer).String() == "direct", "concrete value set into interface field")

		func() {
			defer func() { ck(recover() != nil, "non-implementer set into interface field panics") }()
			hv.FieldByName("Str").Set(reflect.ValueOf(42))
		}()

		// 指针字段 + 标量字段同步写入，GC 后仍与接口字段互相独立。
		hv.FieldByName("Ptr").Set(reflect.ValueOf(&payload{V: base + 8, Tag: &node{V: base + 9}}))
		hv.FieldByName("Count").SetInt(base + 10)
		gcStress()
		ck(hv.FieldByName("Ptr").Elem().FieldByName("V").Int() == base+8, "pointer field after GC")
		ck(hv.FieldByName("Ptr").Elem().FieldByName("Tag").Elem().FieldByName("V").Int() == base+9, "nested tag after GC")
		ck(hv.FieldByName("Count").Int() == base+10, "scalar field after GC")

		// 只留下接口根，再走一遍全部字段。
		anySink = any(h)
		h = hv.Addr().Interface().(*holder)
		gcStress()
		ck(h.Sh.Shape() == 2*base+7, "holder shape via typed root after GC")
		ck(h.Str.String() == "direct", "holder stringer via typed root after GC")
		ck(h.Ptr.Tag.V == base+9, "holder pointer via typed root after GC")
	}

	finish("l06_iface_reflect_set_barrier")
}
