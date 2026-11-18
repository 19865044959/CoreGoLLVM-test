// L06: reflect 读/写 struct 字段（含指针、嵌套、slice、命名类型），GC 后校验。
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

type inner struct {
	Score int64
	Tag   *node
}

type ID int64

type record struct {
	ID     int64
	Name   string
	Ratio  float64
	OK     bool
	Count  uint32
	Tag    *node
	Ptr    *int64
	Inner  inner
	Items  []*node
	Alias  ID
	hidden int64
}

var (
	failed  int32
	gcSink  [][]byte
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

func setRecord(rv reflect.Value, base int64) {
	rv.FieldByName("ID").SetInt(base)
	rv.FieldByName("Name").SetString("alpha")
	rv.FieldByName("Ratio").SetFloat(float64(base) + 0.5)
	rv.FieldByName("OK").SetBool(true)
	rv.FieldByName("Count").SetUint(7)

	rv.FieldByName("Tag").Set(reflect.ValueOf(&node{V: base + 1, Label: "tag"}))

	pv := reflect.New(reflect.TypeOf(int64(0)))
	pv.Elem().SetInt(base + 2)
	rv.FieldByName("Ptr").Set(pv)

	in := rv.FieldByName("Inner")
	in.FieldByName("Score").SetInt(base + 3)
	in.FieldByName("Tag").Set(reflect.ValueOf(&node{V: base + 4, Label: "inner"}))

	items := []*node{
		{V: base + 5, Label: "a"},
		{V: base + 6, Label: "b"},
	}
	rv.FieldByName("Items").Set(reflect.ValueOf(items))

	al := rv.FieldByName("Alias")
	al.Set(reflect.ValueOf(int64(base + 7)).Convert(al.Type()))
}

func checkRecord(rv reflect.Value, base int64) {
	ck(rv.FieldByName("ID").Int() == base, "reflect read ID")
	ck(rv.FieldByName("Name").String() == "alpha", "reflect read Name")
	ck(rv.FieldByName("Ratio").Float() == float64(base)+0.5, "reflect read Ratio")
	ck(rv.FieldByName("OK").Bool(), "reflect read OK")
	ck(rv.FieldByName("Count").Uint() == 7, "reflect read Count")
	ck(rv.FieldByName("Tag").Elem().FieldByName("V").Int() == base+1, "reflect read Tag pointer")
	ck(rv.FieldByName("Tag").Elem().FieldByName("Label").String() == "tag", "reflect read Tag label")
	ck(rv.FieldByName("Ptr").Elem().Int() == base+2, "reflect read Ptr")
	in := rv.FieldByName("Inner")
	ck(in.FieldByName("Score").Int() == base+3, "reflect read nested Score")
	ck(in.FieldByName("Tag").Elem().FieldByName("V").Int() == base+4, "reflect read nested Tag")
	its := rv.FieldByName("Items")
	ck(its.Len() == 2, "reflect read Items len")
	ck(its.Index(0).Elem().FieldByName("V").Int() == base+5, "reflect read Items[0]")
	ck(its.Index(1).Elem().FieldByName("Label").String() == "b", "reflect read Items[1]")
	ck(rv.FieldByName("Alias").Int() == base+7, "reflect read Alias")
	ck(rv.FieldByName("Alias").Type() == reflect.TypeOf(ID(0)), "reflect alias type")
}

func main() {
	uafSeed = envSeed()
	const rounds = 60
	for r := 0; r < rounds; r++ {
		base := uafSeed + int64(r)

		var rec record
		rv := reflect.ValueOf(&rec).Elem()
		ck(rv.FieldByName("Name").CanSet(), "exported field settable")
		hf := rv.FieldByName("hidden")
		ck(!hf.CanSet() && !hf.CanInterface(), "unexported field not settable")

		setRecord(rv, base)
		ck(rec.ID == base && rec.Name == "alpha", "typed view after reflect write")
		checkRecord(rv, base)

		gcStress()
		ck(rec.Tag != nil && rec.Tag.V == base+1 && rec.Tag.Label == "tag", "Tag pointer survives GC")
		ck(rec.Ptr != nil && *rec.Ptr == base+2, "Ptr survives GC")
		ck(rec.Inner.Tag != nil && rec.Inner.Tag.V == base+4, "nested Tag survives GC")
		ck(len(rec.Items) == 2 && rec.Items[1].V == base+6, "Items survive GC")
		checkRecord(rv, base)

		// nil 语义：Zero 清空指针字段，再恢复。
		rv.FieldByName("Tag").Set(reflect.Zero(rv.FieldByName("Tag").Type()))
		ck(rec.Tag == nil, "reflect zero pointer field")
		gcStress()
		ck(rec.Tag == nil, "nil pointer field after GC")
		rv.FieldByName("Tag").Set(reflect.ValueOf(&node{V: base + 9, Label: "again"}))
		gcStress()
		ck(rec.Tag != nil && rec.Tag.V == base+9, "restored pointer field after GC")

		// reflect.New 分配整个 struct，写入后装箱成接口，再跨 GC 校验。
		fresh := reflect.New(reflect.TypeOf(record{})).Elem()
		setRecord(fresh, base+100)
		boxed := fresh.Interface()
		gcStress()
		got, ok := boxed.(record)
		ck(ok, "reflected struct boxes into interface")
		ck(got.ID == base+100 && got.Tag.V == base+101 && got.Ptr != nil && *got.Ptr == base+102, "boxed reflected struct survives GC")
		ck(got.Inner.Tag.V == base+104 && got.Items[0].V == base+105, "boxed reflected pointers survive GC")
	}
	finish("l06_reflect_struct_fields")
}
