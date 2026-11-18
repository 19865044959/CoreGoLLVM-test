// L06: reflect 数组：ArrayOf/New/Index/Set、数组值拷贝语义、Copy、多维数组、指针元素跨 GC。
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

type item struct {
	V   int64
	Tag *string
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

func itemValue(base int64, i int) *item {
	s := fmt.Sprintf("a%d-%d", base, i*7)
	return &item{V: base + int64(i*13) + 1, Tag: &s}
}

func buildArray(base int64) reflect.Value {
	ptrT := reflect.TypeOf((*item)(nil))
	av := reflect.New(reflect.ArrayOf(8, ptrT)).Elem()
	for i := 0; i < 8; i++ {
		av.Index(i).Set(reflect.ValueOf(itemValue(base, i)))
	}
	return av
}

func arraySum(v reflect.Value) int64 {
	var s int64
	for i := 0; i < v.Len(); i++ {
		s += v.Index(i).Elem().FieldByName("V").Int()
	}
	return s
}

func main() {
	uafSeed = envSeed()
	base := uafSeed%7 + 3
	rnd := rand.New(rand.NewSource(uafSeed))

	ptrT := reflect.TypeOf((*item)(nil))
	int64T := reflect.TypeOf(int64(0))

	// 1) ArrayOf + New + Index/Set。
	av := buildArray(base)
	ck(av.Kind() == reflect.Array && av.Len() == 8 && av.Cap() == 8, "reflect array shape")
	ck(av.CanSet() && av.Index(0).CanSet(), "new array elements settable")
	ck(av.Index(0).Type() == ptrT && av.Index(0).Kind() == reflect.Ptr, "array element type")
	wantSum := int64(0)
	for i := 0; i < 8; i++ {
		wantSum += itemValue(base, i).V
	}
	ck(arraySum(av) == wantSum, "array element sum")

	// 2) 非寻址数组值不可 Set；反射写入必须 panic。
	nv := reflect.ValueOf([3]int64{base, base + 1, base + 2})
	ck(!nv.Index(0).CanSet(), "value array element not settable")
	mustPanic("set non-addressable array element", func() { nv.Index(0).SetInt(9) })
	ck(nv.Index(0).Int() == base && nv.Index(2).Int() == base+2, "non-addressable array unchanged")

	// 3) 装箱数组是值拷贝：改槽不影响副本，改指针目标共享。
	origView := av.Interface().([8]*item)
	anySink = av.Interface()
	copied := anySink.([8]*item)
	copied[0] = &item{V: -1}
	back := anySink.([8]*item)
	ck(back[0].V == base+1, "array slot copy independent")
	origView[1].V = 777 + base
	ck(back[1].V == 777+base, "array element pointers shared")

	// 4) 丢弃反射值，仅由接口中的数组副本持有点素，GC 后逐项校验。
	av = reflect.Value{}
	gcStress()
	back = anySink.([8]*item)
	for i := 0; i < 8; i++ {
		want := itemValue(base, i)
		wantV := want.V
		if i == 1 {
			wantV = 777 + base
		}
		if back[i] == nil || back[i].V != wantV || back[i].Tag == nil || *back[i].Tag != *want.Tag {
			ck(false, "array pointer element corrupted after GC")
			break
		}
	}
	ck(back[1].V == 777+base, "mutated element survives GC")

	// 5) 多维数组：ArrayOf(2, ArrayOf(3, int64))。
	innerT := reflect.ArrayOf(3, int64T)
	outerT := reflect.ArrayOf(2, innerT)
	md := reflect.New(outerT).Elem()
	var mdSum int64
	for i := 0; i < 2; i++ {
		ck(md.Index(i).Kind() == reflect.Array && md.Index(i).Len() == 3, "nested array shape")
		for j := 0; j < 3; j++ {
			x := base + int64(i*10+j)
			md.Index(i).Index(j).SetInt(x)
			mdSum += x
		}
	}
	ck(md.Index(1).Index(2).Int() == base+12, "nested array set/read")
	anySink = md.Interface()
	md = reflect.Value{}
	gcStress()
	md2 := anySink.([2][3]int64)
	var gotSum int64
	for i := 0; i < 2; i++ {
		for j := 0; j < 3; j++ {
			gotSum += md2[i][j]
		}
	}
	ck(gotSum == mdSum && md2[1][2] == base+12, "nested array survives GC")

	// 6) reflect.Copy：数组→切片、切片→数组、元素类型不匹配 panic。
	dst := reflect.MakeSlice(reflect.SliceOf(int64T), 5, 5)
	n := reflect.Copy(dst, reflect.ValueOf([3]int64{base + 1, base + 2, base + 3}))
	ck(n == 3 && dst.Index(0).Int() == base+1 && dst.Index(4).Int() == 0, "copy array to slice")
	dstArr := reflect.New(innerT).Elem()
	n = reflect.Copy(dstArr, reflect.ValueOf([]int64{base + 7, base + 8, base + 9, base + 10}))
	ck(n == 3 && dstArr.Index(2).Int() == base+9, "copy slice to array")
	mustPanic("copy mismatched element types", func() {
		reflect.Copy(reflect.MakeSlice(reflect.SliceOf(int64T), 2, 2), reflect.ValueOf([]int32{1, 2}))
	})

	// 7) 零长数组与 ArrayOf 边界。
	emptyT := reflect.ArrayOf(0, ptrT)
	zero := reflect.Zero(emptyT)
	ck(emptyT.Len() == 0 && zero.Len() == 0, "zero-length array")
	z2 := reflect.Zero(reflect.ArrayOf(2, ptrT))
	ck(z2.Len() == 2 && z2.Index(0).IsNil() && z2.Index(1).IsNil(), "zero array nil elements")
	mustPanic("ArrayOf negative length", func() { reflect.ArrayOf(-1, ptrT) })

	// 8) UAF_SEED 驱动的随机槽位回写，验证反射写入与类型化视图一致。
	for k := 0; k < 12; k++ {
		idx := rnd.Intn(8)
		val := base + int64(500+k)
		back[idx].V = val
		rv := reflect.ValueOf(&back).Elem()
		ck(rv.Index(idx).Elem().FieldByName("V").Int() == val, "seeded array slot write/read")
	}
	gcStress()
	rv := reflect.ValueOf(&back).Elem()
	ck(rv.Len() == 8 && rv.Index(rnd.Intn(8)).IsValid(), "typed array view after GC")

	finish("l06_reflect_array_ops")
}
