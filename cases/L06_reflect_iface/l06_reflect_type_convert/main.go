// L06: reflect 类型转换：Convert/CanConvert 矩阵、命名类型、slice→array、接口转换、panic 路径，跨 GC。
package main

import (
	"bytes"
	"fmt"
	"os"
	"reflect"
	"runtime"
	"strconv"
	"sync/atomic"
)

type MyInt int64
type MyStr string
type Raw []byte

type A struct {
	X int64 `json:"x"`
	Y string
}

type B struct {
	X int64
	Y string
}

type ifaceImpl struct{ V int64 }

func (i ifaceImpl) Read() int64 { return i.V }

type reader interface{ Read() int64 }

var (
	failed   int32
	gcSink   [][]byte
	anySink  any
	byteSink []byte
	runeSink []rune
	uafSeed  int64 = 1
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

func main() {
	uafSeed = envSeed()
	base := uafSeed%1000 + 300
	int64T := reflect.TypeOf(int64(0))
	readerT := reflect.TypeOf((*reader)(nil)).Elem()
	ch := make(chan int64, 2)
	ch <- 4
	ch <- 5

	type spec struct {
		name  string
		src   any
		dst   reflect.Type
		conv  bool
		check func(reflect.Value) bool
	}
	specs := []spec{
		{"int64 to int8 wrap", int64(base), reflect.TypeOf(int8(0)), true,
			func(v reflect.Value) bool { return v.Kind() == reflect.Int8 && v.Int() == int64(int8(base)) }},
		{"int64 to uint8 wrap", int64(base + 201), reflect.TypeOf(uint8(0)), true,
			func(v reflect.Value) bool { return v.Uint() == uint64(uint8(base+201)) }},
		{"int32 to uint32", int32(-1), reflect.TypeOf(uint32(0)), true,
			func(v reflect.Value) bool { return v.Uint() == 4294967295 }},
		{"int64 to string rune", int64(65), reflect.TypeOf(""), true,
			func(v reflect.Value) bool { return v.String() == "A" }},
		{"float64 to int64 trunc", 3.99, int64T, true,
			func(v reflect.Value) bool { return v.Int() == 3 }},
		{"float32 to float64", float32(1.5), reflect.TypeOf(float64(0)), true,
			func(v reflect.Value) bool { return v.Float() == 1.5 }},
		{"string to bytes", "hi", reflect.TypeOf([]byte(nil)), true,
			func(v reflect.Value) bool { return bytes.Equal(v.Interface().([]byte), []byte("hi")) }},
		{"bytes to string", []byte("go"), reflect.TypeOf(""), true,
			func(v reflect.Value) bool { return v.String() == "go" }},
		{"string to runes", "hé", reflect.TypeOf([]rune(nil)), true,
			func(v reflect.Value) bool {
				r := v.Interface().([]rune)
				return len(r) == 2 && r[0] == 'h' && r[1] == 'é'
			}},
		{"int64 to named", base, reflect.TypeOf(MyInt(0)), true,
			func(v reflect.Value) bool { return v.Type().Name() == "MyInt" && v.Int() == base }},
		{"named to int64", MyInt(base + 1), int64T, true,
			func(v reflect.Value) bool { return v.Int() == base+1 }},
		{"string to named string", "s", reflect.TypeOf(MyStr("")), true,
			func(v reflect.Value) bool { return v.Type().Name() == "MyStr" && v.String() == "s" }},
		{"named string to string", MyStr("q"), reflect.TypeOf(""), true,
			func(v reflect.Value) bool { return v.String() == "q" }},
		{"bytes to named slice", []byte{1, 2}, reflect.TypeOf(Raw(nil)), true,
			func(v reflect.Value) bool { return bytes.Equal([]byte(v.Interface().(Raw)), []byte{1, 2}) }},
		{"named slice to bytes", Raw{3, 4}, reflect.TypeOf([]byte(nil)), true,
			func(v reflect.Value) bool { return bytes.Equal(v.Interface().([]byte), []byte{3, 4}) }},
		{"slice to array", []int64{1, 2, 3}, reflect.TypeOf([3]int64{}), true,
			func(v reflect.Value) bool { return v.Interface().([3]int64) == [3]int64{1, 2, 3} }},
		{"slice to array pointer", []int64{4, 5, 6}, reflect.TypeOf((*[3]int64)(nil)), true,
			func(v reflect.Value) bool {
				p := v.Interface().(*[3]int64)
				return p != nil && *p == [3]int64{4, 5, 6}
			}},
		{"named pointer to pointer", (*MyInt)(nil), reflect.TypeOf((*int64)(nil)), true,
			func(v reflect.Value) bool { return v.IsNil() }},
		{"struct ignores tags", A{X: 7, Y: "z"}, reflect.TypeOf(B{}), true,
			func(v reflect.Value) bool {
				b := v.Interface().(B)
				return b.X == 7 && b.Y == "z"
			}},
		{"chan to recv only", ch, reflect.TypeOf((<-chan int64)(nil)), true,
			func(v reflect.Value) bool {
				rv, ok := v.Recv()
				return ok && rv.Int() == 4
			}},
		{"concrete to interface", ifaceImpl{V: 12}, readerT, true,
			func(v reflect.Value) bool {
				r, ok := v.Interface().(reader)
				return ok && r.Read() == 12
			}},
		{"string to int64", "x", int64T, false, nil},
		{"int32 slice to int64 array", []int32{1, 2}, reflect.TypeOf([2]int64{}), false, nil},
		{"int64 to chan", base, reflect.TypeOf(make(chan int64)), false, nil},
		{"chan element type differs", make(chan int64), reflect.TypeOf(make(chan MyInt)), false, nil},
		{"array to slice", [3]int64{}, reflect.TypeOf([]int64(nil)), false, nil},
		{"pointer base differs", (*int64)(nil), reflect.TypeOf((*string)(nil)), false, nil},
		{"bool to string", true, reflect.TypeOf(""), false, nil},
	}

	var convCount, nonConvCount int
	for _, s := range specs {
		sv := reflect.ValueOf(s.src)
		got := sv.Type().ConvertibleTo(s.dst)
		ck(got == s.conv, "ConvertibleTo "+s.name)
		ck(sv.CanConvert(s.dst) == s.conv, "CanConvert "+s.name)
		if !s.conv {
			nonConvCount++
			mustPanic("Convert not allowed "+s.name, func() { sv.Convert(s.dst) })
			continue
		}
		convCount++
		out := sv.Convert(s.dst)
		ck(out.Type() == s.dst, "converted type "+s.name)
		if s.check != nil {
			ck(s.check(out), "converted value "+s.name)
		}
	}
	ck(convCount == 21, "convertible case count")
	ck(nonConvCount == 7, "non-convertible case count")
	ck(len(ch) == 1, "converted recv-only channel consumed value")
	if v, ok := <-ch; !ok || v != 5 {
		ck(false, "converted channel shares underlying queue")
	}

	// 类型级转换可用但运行时长度不足：必须 panic。
	mustPanic("short slice to array", func() {
		reflect.ValueOf([]int64{1, 2}).Convert(reflect.TypeOf([3]int64{}))
	})
	mustPanic("zero Value conversion", func() { reflect.Value{}.Convert(int64T) })

	// 非 nil 命名指针转换后共享目标；slice→array 指针共享底层数组。
	pv := MyInt(base + 9)
	pOut := reflect.ValueOf(&pv).Convert(reflect.TypeOf((*int64)(nil))).Interface().(*int64)
	ck(*pOut == base+9, "named pointer conversion value")
	*pOut = base + 10
	ck(pv == MyInt(base+10), "named pointer conversion shares target")

	sl := []int64{1, 2, 3, 4}
	arrPtr := reflect.ValueOf(sl).Convert(reflect.TypeOf((*[3]int64)(nil))).Interface().(*[3]int64)
	ck(arrPtr[2] == 3, "slice to array pointer value")
	arrPtr[0] = 99
	ck(sl[0] == 99, "slice to array pointer aliases backing array")

	// 转换结果只由全局槽保活，GC 后内容仍正确。
	byteSink = reflect.ValueOf("persist").Convert(reflect.TypeOf([]byte(nil))).Interface().([]byte)
	runeSink = reflect.ValueOf("héllo").Convert(reflect.TypeOf([]rune(nil))).Interface().([]rune)
	anySink = sl
	gcStress()
	ck(bytes.Equal(byteSink, []byte("persist")), "converted bytes after GC")
	ck(len(runeSink) == 5 && runeSink[1] == 'é', "converted runes after GC")
	ck(arrPtr[0] == 99 && sl[0] == 99, "array pointer after GC")
	ck(*pOut == base+10, "converted named pointer after GC")

	finish("l06_reflect_type_convert")
}
