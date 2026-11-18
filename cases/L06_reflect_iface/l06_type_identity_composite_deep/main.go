// L06: 复合类型身份：接口方法顺序无关、命名指针、数组长度/嵌套、map key 命名类型、
// 方向通道、变参函数签名、StructOf 字段顺序/匿名标记、递归类型与 reflect.Type 作 map 键。
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
	V    int64
	Next *node
}

type myInt int64
type aliasInt = int64
type nodePtr *node
type nodePtrAlias = *node
type freeze struct {
	Next *freeze
	V    int64
}
type otherFreeze struct {
	Next *freeze
	V    int64
}

type namedI1 interface{ A() int64 }
type namedI2 interface{ B() int64 }

var (
	failed   int32
	gcSink   [][]byte
	typeSink []reflect.Type
	anySink  any
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

func envInt(name string, def, min, max int) int {
	s := os.Getenv(name)
	if s == "" {
		return def
	}
	n, err := strconv.Atoi(s)
	if err != nil || n < min {
		return def
	}
	if n > max {
		return max
	}
	return n
}

func main() {
	uafSeed = envSeed()
	rounds := envInt("TORTURE_ROUNDS", 8, 1, 2000)

	int64T := reflect.TypeOf(int64(0))
	strT := reflect.TypeOf("")
	errT := reflect.TypeOf((*error)(nil)).Elem()
	nodeT := reflect.TypeOf(node{})
	nodePtrT := reflect.TypeOf((*node)(nil))
	myIntT := reflect.TypeOf(myInt(0))

	// 具名接口与结构相同的匿名接口不相等；匿名接口按方法集合判定，声明顺序无关。
	iAB := reflect.TypeOf((*interface {
		A() int64
		B() int64
	})(nil)).Elem()
	iBA := reflect.TypeOf((*interface {
		B() int64
		A() int64
	})(nil)).Elem()
	emb := reflect.TypeOf((*interface {
		namedI1
		namedI2
	})(nil)).Elem()
	ck(iAB == iBA, "unnamed interface identity ignores method order")
	ck(emb == iAB, "unnamed interface embedding equals direct method set")
	ck(emb.NumMethod() == 2 && emb.Method(0).Name == "A" && emb.Method(1).Name == "B", "unnamed interface method order sorted")
	ck(reflect.TypeOf((*namedI1)(nil)).Elem() != iAB, "named interface differs from unnamed structural type")

	// 命名指针类型、别名与 *T 的身份。
	npT := reflect.TypeOf(nodePtr(nil))
	ck(npT != nodePtrT, "named pointer type differs from *T")
	ck(npT.Elem() == nodePtrT.Elem(), "named pointer elem is underlying elem")
	ck(npT.Kind() == reflect.Ptr && npT.Name() == "nodePtr" && npT.PkgPath() == "main", "named pointer metadata")
	ck(reflect.TypeOf(nodePtrAlias(nil)) == nodePtrT, "pointer alias identity equals *T")
	ck(npT.ConvertibleTo(nodePtrT) && npT.AssignableTo(nodePtrT) && nodePtrT.AssignableTo(npT), "named pointer assignment rules")
	ck(npT == reflect.TypeOf(nodePtr(&node{})) && npT == reflect.TypeOf(nodePtr(nil)), "named pointer stable identity")

	// 数组长度、元素类型与嵌套构造。
	ck(reflect.TypeOf([3]int64{}) != reflect.TypeOf([4]int64{}), "array length participates in identity")
	ck(reflect.TypeOf([0]int64{}) == reflect.ArrayOf(0, int64T), "zero length ArrayOf identity")
	ck(reflect.TypeOf([0]int64{}) != reflect.TypeOf([0]int32{}), "zero length arrays of different elem differ")
	ck(reflect.TypeOf([2][3]int64{}) == reflect.ArrayOf(2, reflect.ArrayOf(3, int64T)), "nested ArrayOf identity")

	// map key 的命名类型与匿名类型不同；MapOf 与字面 map 身份一致。
	mNamed := reflect.TypeOf(map[myInt]string{})
	mPlain := reflect.TypeOf(map[int64]string{})
	ck(mNamed != mPlain, "map key named type participates in identity")
	ck(reflect.MapOf(myIntT, strT) == mNamed, "MapOf named key identity")
	ck(reflect.MapOf(int64T, strT) == mPlain, "MapOf unnamed key identity")
	ck(mNamed.Key() == myIntT && mNamed.Elem() == strT, "map key/elem metadata")

	// 通道方向与嵌套方向类型。
	chBoth := reflect.TypeOf((chan int64)(nil))
	chSend := reflect.TypeOf((chan<- int64)(nil))
	chRecv := reflect.TypeOf((<-chan int64)(nil))
	ck(chBoth != chSend && chSend != chRecv && chBoth != chRecv, "channel directions are distinct types")
	ck(reflect.ChanOf(reflect.SendDir, int64T) == chSend, "ChanOf send identity")
	ck(reflect.TypeOf((chan<- chan int64)(nil)) != reflect.TypeOf((chan<- <-chan int64)(nil)), "nested directional chan identity")

	// 变参函数签名身份：In 末位是切片且 Variadic 为 true。
	fT := reflect.TypeOf(func(int64, ...string) (int64, error) { return 0, nil })
	built := reflect.FuncOf([]reflect.Type{int64T, reflect.SliceOf(strT)}, []reflect.Type{int64T, errT}, true)
	ck(fT == built, "FuncOf variadic identity")
	ck(fT.IsVariadic() && !reflect.TypeOf(func(int64, []string) (int64, error) { return 0, nil }).IsVariadic(), "variadic flag identity")
	ck(fT.In(1) == reflect.SliceOf(strT) && fT.Out(1) == errT, "variadic func in/out types")

	// StructOf：字段顺序、字段名与匿名标记参与身份。
	sAB := reflect.StructOf([]reflect.StructField{
		{Name: "A", Type: int64T},
		{Name: "B", Type: strT},
	})
	sBA := reflect.StructOf([]reflect.StructField{
		{Name: "B", Type: strT},
		{Name: "A", Type: int64T},
	})
	sNames := reflect.StructOf([]reflect.StructField{
		{Name: "C", Type: int64T},
		{Name: "D", Type: strT},
	})
	ck(sAB == reflect.TypeOf(struct {
		A int64
		B string
	}{}), "StructOf field order identity")
	ck(sAB != sBA, "field order participates in identity")
	ck(sAB != sNames, "field names participate in identity")
	anonIfaceT := reflect.TypeOf((*interface{ A() int64 })(nil)).Elem()
	anon := reflect.StructOf([]reflect.StructField{{Name: "I", Type: anonIfaceT, Anonymous: true}})
	plain := reflect.StructOf([]reflect.StructField{{Name: "I", Type: anonIfaceT}})
	ck(anon != plain && anon.Field(0).Anonymous && !plain.Field(0).Anonymous, "anonymous flag participates in StructOf identity")

	// 递归命名类型：Next 的 Elem 指回自身；不同命名类型即使底层相同也不相等。
	fz := reflect.TypeOf(freeze{})
	ck(fz.Field(0).Type.Elem() == fz, "recursive pointer type identity")
	ck(fz != reflect.TypeOf(otherFreeze{}), "distinct recursive named types differ")

	// reflect.Type 作 map 键，按类型身份哈希。
	tm := map[reflect.Type]int{
		nodeT:    1,
		nodePtrT: 2,
		sAB:      3,
		sBA:      4,
		mNamed:   5,
		fT:       6,
	}
	ck(len(tm) == 6, "type map keyed by identity")
	tm[nodePtrT] = 20
	ck(tm[nodePtrT] == 20 && tm[reflect.TypeOf(node{})] == 1, "type map lookup by equal type")
	ck(tm[reflect.StructOf([]reflect.StructField{{Name: "A", Type: int64T}, {Name: "B", Type: strT}})] == 3,
		"StructOf canonical type hits map key")
	ck(tm[reflect.MapOf(myIntT, strT)] == 5, "MapOf canonical type hits map key")

	typeSink = []reflect.Type{iAB, emb, npT, mNamed, chSend, fT, sAB, anon, fz}
	anySink = tm
	gcStress()
	for i, want := range []reflect.Type{iAB, emb, npT, mNamed, chSend, fT, sAB, anon, fz} {
		ck(typeSink[i] == want, "type identity after GC")
	}
	ck(anySink.(map[reflect.Type]int)[nodeT] == 1, "type map survives GC")
	ck(reflect.ArrayOf(0, int64T) == reflect.TypeOf([0]int64{}), "constructed type identity after GC")

	for r := 0; r < rounds; r++ {
		base := uafSeed + int64(r)
		nv := reflect.New(nodeT).Elem()
		nv.FieldByName("V").SetInt(base)
		nv.FieldByName("Next").Set(reflect.Zero(nodePtrT))
		gcStress()
		ck(nv.FieldByName("V").Int() == base, "value built from type after GC")

		// 每轮重新构造复合类型，命中类型缓存后身份不变。
		again := reflect.FuncOf([]reflect.Type{int64T, reflect.SliceOf(strT)}, []reflect.Type{int64T, errT}, true)
		ck(again == fT, "rebuilt FuncOf identity")
		againStruct := reflect.StructOf([]reflect.StructField{{Name: "A", Type: int64T}, {Name: "B", Type: strT}})
		ck(againStruct == sAB, "rebuilt StructOf identity")
		againMap := reflect.MapOf(myIntT, strT)
		ck(againMap == mNamed, "rebuilt MapOf identity")
		ck(reflect.TypeOf(map[int64]string{}) == mPlain, "plain map identity stable")
	}

	finish("l06_type_identity_composite_deep")
}
