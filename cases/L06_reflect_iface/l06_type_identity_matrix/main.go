// L06: 类型身份与元数据：命名类型/别名、TypeFor、复合类型构造同一性、结构体 tag 参与身份、
// 接口结构同一性与 Comparable，跨 GC 稳定。
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

type myInt int64
type myAlias = int64
type mySlice []int64
type myMap map[string]int64
type myChan chan int64
type myFunc func(int64) int64

type namedI1 interface {
	M() int64
}

type namedI2 interface {
	M() int64
}

type namedIJ interface {
	M() int64
	N() int64
}

type impl struct{}

func (impl) M() int64 { return 1 }
func (impl) N() int64 { return 2 }

var (
	failed   int32
	gcSink   [][]byte
	typeSink []reflect.Type
	anySink  []any
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

	intT := reflect.TypeOf(0)
	int64T := reflect.TypeOf(int64(0))
	strT := reflect.TypeOf("")
	nodeT := reflect.TypeOf(node{})

	ck(reflect.TypeFor[node]() == nodeT, "TypeFor matches TypeOf")
	ck(reflect.TypeFor[node]() == reflect.TypeOf((*node)(nil)).Elem(), "TypeFor matches pointer elem")
	ck(reflect.TypeFor[*node]() == reflect.TypeOf((*node)(nil)), "TypeFor pointer matches pointer value")
	ck(reflect.TypeFor[myInt]().Name() == "myInt", "TypeFor named type")

	myIntT := reflect.TypeOf(myInt(0))
	ck(myIntT != intT, "defined type differs from underlying")
	ck(myIntT.Name() == "myInt" && myIntT.PkgPath() == "main", "defined type name and package")
	ck(myIntT.Kind() == reflect.Int64, "defined type kind follows underlying")
	ck(myIntT.ConvertibleTo(int64T) && !myIntT.AssignableTo(int64T), "defined type convertible not assignable")
	aliasT := reflect.TypeOf(myAlias(0))
	ck(aliasT == int64T, "alias identity equals underlying")
	ck(aliasT.Name() == "int64" && aliasT.PkgPath() == "", "alias reports underlying metadata")

	mySliceT := reflect.TypeOf(mySlice{})
	myMapT := reflect.TypeOf(myMap{})
	myChanT := reflect.TypeOf(myChan(nil))
	myFuncT := reflect.TypeOf(myFunc(nil))
	ck(mySliceT != reflect.TypeOf([]int64{}), "named slice differs from unnamed slice")
	ck(mySliceT.Name() == "mySlice" && mySliceT.Kind() == reflect.Slice, "named slice metadata")
	ck(mySliceT.Elem() == int64T, "named slice element")
	ck(myMapT.Name() == "myMap" && myMapT.Kind() == reflect.Map, "named map metadata")
	ck(myMapT.Key() == strT && myMapT.Elem() == int64T, "named map key elem")
	ck(myChanT.Name() == "myChan" && myChanT.Kind() == reflect.Chan && myChanT.ChanDir() == reflect.BothDir, "named chan metadata")
	ck(myChanT.Elem() == int64T, "named chan element")
	ck(myFuncT.Name() == "myFunc" && myFuncT.Kind() == reflect.Func, "named func metadata")
	ck(myFuncT.NumIn() == 1 && myFuncT.In(0) == int64T && myFuncT.NumOut() == 1 && myFuncT.Out(0) == int64T, "named func signature")
	ck(!myFuncT.IsVariadic(), "named func not variadic")

	ck(reflect.SliceOf(intT) == reflect.TypeOf([]int(nil)), "SliceOf identity")
	ck(reflect.SliceOf(intT) == reflect.SliceOf(intT), "SliceOf stable identity")
	ck(reflect.ArrayOf(3, intT) == reflect.TypeOf([3]int{}), "ArrayOf identity")
	ck(reflect.ArrayOf(0, intT) == reflect.TypeOf([0]int{}), "zero length ArrayOf identity")
	ck(reflect.MapOf(strT, intT) == reflect.TypeOf(map[string]int(nil)), "MapOf identity")
	ck(reflect.ChanOf(reflect.BothDir, intT) == reflect.TypeOf((chan int)(nil)), "ChanOf both identity")
	ck(reflect.ChanOf(reflect.SendDir, intT) == reflect.TypeOf((chan<- int)(nil)), "ChanOf send identity")
	ck(reflect.ChanOf(reflect.RecvDir, intT) == reflect.TypeOf((<-chan int)(nil)), "ChanOf recv identity")
	ck(reflect.FuncOf([]reflect.Type{intT}, []reflect.Type{intT}, false) == reflect.TypeOf(func(int) int { return 0 }), "FuncOf identity")
	ck(reflect.PointerTo(nodeT) == reflect.TypeOf((*node)(nil)), "PointerTo identity")

	uA := reflect.TypeOf(struct {
		A int `json:"a"`
	}{})
	uB := reflect.TypeOf(struct {
		A int `json:"a"`
	}{})
	uC := reflect.TypeOf(struct {
		A int `json:"b"`
	}{})
	ck(uA == uB, "identical unnamed structs with same tags are identical")
	ck(uA != uC, "struct tags participate in type identity")
	ck(uA.Name() == "" && uA.PkgPath() == "", "unnamed struct metadata")

	sf := []reflect.StructField{{Name: "A", Type: intT, Tag: `json:"a"`}}
	s1 := reflect.StructOf(sf)
	s2 := reflect.StructOf(sf)
	sfDiff := []reflect.StructField{{Name: "A", Type: intT, Tag: `json:"b"`}}
	s3 := reflect.StructOf(sfDiff)
	ck(s1 == s2, "StructOf same fields same identity")
	ck(s1 != s3, "StructOf tag change produces distinct type")
	ck(s1 == uA, "StructOf matches literal unnamed struct")
	ck(s1.Field(0).Tag.Get("json") == "a", "StructOf field tag")

	uI1 := reflect.TypeOf((*interface{ M() int64 })(nil)).Elem()
	uI2 := reflect.TypeOf((*interface{ M() int64 })(nil)).Elem()
	uIJ := reflect.TypeOf((*interface {
		M() int64
		N() int64
	})(nil)).Elem()
	nI1 := reflect.TypeOf((*namedI1)(nil)).Elem()
	nI2 := reflect.TypeOf((*namedI2)(nil)).Elem()
	nIJ := reflect.TypeOf((*namedIJ)(nil)).Elem()
	ck(uI1 == uI2, "identical unnamed interfaces equal")
	ck(nI1 != nI2, "different named interfaces distinct")
	ck(nI1.AssignableTo(nI2) && nI2.AssignableTo(nI1), "named interfaces with same method set assignable")
	ck(nI1 != uI1, "named interface differs from unnamed structural type")
	ck(nI1.AssignableTo(uI1) && uI1.AssignableTo(nI1), "named and unnamed structural interfaces assignable")
	ck(nIJ.Implements(nI1), "wider named interface implements narrower")
	ck(!nI1.Implements(nIJ), "narrower named interface does not implement wider")
	ck(nIJ.AssignableTo(nI1), "wider interface assignable to narrower")
	ck(!nI1.AssignableTo(nIJ), "narrower interface not assignable to wider")
	ck(nI1.NumMethod() == 1 && nI1.Method(0).Name == "M", "named interface method shape")
	ck(uIJ.NumMethod() == 2 && uIJ.Method(0).Name == "M" && uIJ.Method(1).Name == "N", "unnamed interface method order")
	ck(reflect.TypeOf(impl{}).Implements(nI1) && reflect.TypeOf(impl{}).Implements(nIJ), "impl implements interfaces")

	ck(intT.Comparable(), "int comparable")
	ck(!reflect.TypeOf([]int{}).Comparable(), "slice not comparable")
	ck(!reflect.TypeOf(map[string]int{}).Comparable(), "map not comparable")
	ck(!reflect.TypeOf(func() {}).Comparable(), "func not comparable")
	ck(reflect.TypeOf([2]int{}).Comparable(), "array comparable")
	ck(reflect.TypeOf(struct{ A int }{}).Comparable(), "struct of comparable fields comparable")
	ck(!reflect.TypeOf(struct{ F func() }{}).Comparable(), "struct with func not comparable")
	ck(reflect.TypeOf((*any)(nil)).Elem().Comparable(), "interface type comparable")
	ck(reflect.TypeOf((*error)(nil)).Elem().Comparable(), "error interface comparable")
	ck(reflect.TypeOf((chan int)(nil)).Comparable(), "channel type comparable")
	ck(reflect.TypeOf((*node)(nil)).Comparable(), "pointer type comparable")

	ck(intT.Name() == "int" && intT.PkgPath() == "", "builtin type metadata")
	ck(nodeT.Name() == "node" && nodeT.PkgPath() == "main", "local named struct metadata")
	ck(reflect.TypeOf([]int{}).Name() == "" && reflect.TypeOf([]int{}).PkgPath() == "", "unnamed slice metadata")
	ck(reflect.TypeOf([]int{}).String() == "[]int", "slice string")
	ck(reflect.TypeOf(map[string]int{}).String() == "map[string]int", "map string")
	ck(reflect.TypeOf((chan<- int)(nil)).String() == "chan<- int", "send chan string")
	ck(reflect.TypeOf((<-chan int)(nil)).String() == "<-chan int", "recv chan string")
	ck(reflect.TypeOf(func(int) int { return 0 }).String() == "func(int) int", "func string")
	ck(nodeT.String() == "main.node", "named struct string")
	ck(mySliceT.String() == "main.mySlice", "named slice string")

	ck(reflect.TypeOf([]node{}).Elem() == nodeT, "slice elem")
	ck(reflect.TypeOf(map[string]node{}).Key() == strT && reflect.TypeOf(map[string]node{}).Elem() == nodeT, "map key elem")
	ck(reflect.TypeOf((<-chan node)(nil)).Elem() == nodeT, "chan elem")
	ck(reflect.TypeOf((*node)(nil)).Elem() == nodeT, "pointer elem")
	ck(reflect.TypeOf([4]node{}).Elem() == nodeT && reflect.TypeOf([4]node{}).Len() == 4, "array elem and len")
	ck(nodeT.NumField() == 2 && nodeT.Field(0).Name == "V" && nodeT.Field(1).Name == "Label", "struct fields")
	ck(nodeT.Field(0).Type == int64T && nodeT.Field(1).Type == strT, "struct field types")

	// 类型与由类型构造的值跨 GC 稳定。
	typeSink = []reflect.Type{nodeT, myIntT, mySliceT, myMapT, myChanT, myFuncT, uA, s1, nI1, uI1}
	anySink = []any{nodeT, myIntT, mySliceT, myMapT, myChanT, myFuncT, uA, s1, nI1, uI1}
	gcStress()
	for i := range typeSink {
		ck(typeSink[i] == anySink[i].(reflect.Type), "type identity after GC")
	}
	ck(reflect.TypeFor[node]() == typeSink[0], "TypeFor after GC")
	v := reflect.New(typeSink[0]).Elem()
	v.FieldByName("V").SetInt(uafSeed)
	v.FieldByName("Label").SetString("gc")
	gcStress()
	ck(v.FieldByName("V").Int() == uafSeed && v.FieldByName("Label").String() == "gc", "value built from type survives GC")
	sl := reflect.MakeSlice(typeSink[2], 2, 2)
	sl.Index(0).SetInt(uafSeed)
	gcStress()
	ck(sl.Index(0).Int() == uafSeed && sl.Type() == mySliceT, "slice built from saved type after GC")
	mp := reflect.MakeMap(typeSink[3])
	mp.SetMapIndex(reflect.ValueOf("k"), reflect.ValueOf(uafSeed))
	gcStress()
	ck(mp.MapIndex(reflect.ValueOf("k")).Int() == uafSeed && mp.Type() == myMapT, "map built from saved type after GC")
	finish("l06_type_identity_matrix")
}
