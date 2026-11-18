// L06: 接口持有指针：接口数据区保活、接口值拷贝共享、*接口、typed nil 接收者，跨 GC。
package main

import (
	"fmt"
	"os"
	"reflect"
	"runtime"
	"strconv"
	"sync/atomic"
)

type Node struct {
	V    int64
	Name string
}

type Payload struct {
	N   int64
	Tag *Node
	Arr *[4]*Node
}

type Counter struct {
	n int64
	P *Payload
}

func (c *Counter) Inc(d int64) int64 {
	if c == nil {
		return -1000 - d
	}
	c.n += d
	return c.n
}

func (c *Counter) Get() int64 {
	if c == nil {
		return -2000
	}
	return c.n
}

type incer interface {
	Inc(int64) int64
	Get() int64
}
type getter interface{ Get() int64 }

type holder struct {
	I   incer
	Own *Payload
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

func makeCounter(base int64) incer {
	tag := &Node{V: base + 100, Name: fmt.Sprintf("tag%d", base)}
	arr := &[4]*Node{
		{V: base + 1, Name: "a"},
		nil,
		{V: base + 2, Name: "b"},
		{V: base + 3, Name: "c"},
	}
	p := &Payload{N: base, Tag: tag, Arr: arr}
	return &Counter{n: base, P: p}
}

func main() {
	uafSeed = envSeed()
	base := uafSeed%9 + 1

	// 1) 接口是唯一根：对象只在接口数据区中，GC 后仍可解引用其指针字段。
	i := makeCounter(base)
	gcStress()
	c := i.(*Counter)
	ck(c != nil, "interface holds pointer after GC")
	ck(c.P != nil && c.P.N == base, "payload survives via interface")
	ck(c.P.Tag != nil && c.P.Tag.V == base+100 && c.P.Tag.Name == fmt.Sprintf("tag%d", base), "nested tag pointer after GC")
	ck(c.P.Arr != nil && c.P.Arr[0].V == base+1 && c.P.Arr[1] == nil && c.P.Arr[2].V == base+2 && c.P.Arr[3].V == base+3, "pointer to array after GC")

	// 2) 接口值拷贝共享同一指针；经不同接口视图修改互相可见。
	i2 := i
	ck(i2.Inc(3) == base+3, "interface copy increments")
	ck(i.Get() == base+3, "same object via original interface")
	var g getter = i2
	ck(g.Get() == base+3, "getter view sees mutation")
	cc, ok := i.(*Counter)
	ck(ok && cc == c, "assert back to same pointer")

	// 3) 装箱进 any、再经接口断言回来。
	var anyI any = i
	gcStress()
	i3, ok := anyI.(incer)
	ck(ok && i3.Get() == base+3, "any round trip to incer after GC")
	ck(reflect.TypeOf(anyI) == reflect.TypeOf(&Counter{}), "dynamic type after GC")

	// 4) 结构体接口字段持指针：holder 整体只由 any 保活。
	h := &holder{I: i, Own: c.P}
	anySink = h
	h = nil
	gcStress()
	h2 := anySink.(*holder)
	ck(h2.I.Get() == base+3, "interface field after GC")
	ck(h2.Own != nil && h2.Own.Tag.V == base+100 && h2.Own.Arr[2].Name == "b", "payload via holder after GC")
	h2.I.Inc(2)
	ck(i.Get() == base+5 && h2.Own == c.P, "holder interface and payload share object")

	// 5) 指针指向接口变量：*incer。
	rg := &i
	var rgAny any = rg
	(*rg).Inc(1)
	gcStress()
	rg2 := rgAny.(*incer)
	ck((*rg2).Get() == base+6, "*interface survives GC")
	ck(reflect.TypeOf(rgAny).Kind() == reflect.Ptr, "*interface kind")
	ck(reflect.TypeOf(rgAny).Elem() == reflect.TypeOf((*incer)(nil)).Elem(), "*interface element type")
	ck(reflect.ValueOf(rgAny).Elem().Kind() == reflect.Interface, "*interface reflect kind")

	// 6) 接口持有指向数组与切片的指针。
	arr := &[2]*Node{{V: base + 7}, {V: base + 8}}
	var arrAny any = arr
	arr = nil
	gcStress()
	ar := arrAny.(*[2]*Node)
	ck(ar[0].V == base+7 && ar[1].V == base+8, "pointer to array via interface after GC")

	ps := &[]*Node{{V: base + 9}, nil, {V: base + 10}}
	var psAny any = ps
	ps = nil
	gcStress()
	ps2 := psAny.(*[]*Node)
	ck(len(*ps2) == 3 && (*ps2)[0].V == base+9 && (*ps2)[1] == nil && (*ps2)[2].V == base+10, "pointer to slice via interface after GC")

	// 7) typed nil 指针装箱：接口非 nil，nil 接收者方法可安全调用。
	var nilC *Counter
	var nilI incer = nilC
	ck(nilI != nil, "typed nil interface is non-nil")
	anySink = nilI
	gcStress()
	ck(nilI != nil, "typed nil interface after GC")
	ck(nilI.Inc(7) == -1007, "nil receiver method via interface")
	bn, ok := nilI.(*Counter)
	ck(ok && bn == nil, "typed nil asserts back to nil pointer")
	ng, ok := nilI.(getter)
	ck(ok && ng != nil, "typed nil implements getter view")
	ck(ng.Get() == -2000, "nil receiver getter")
	var nilReader incer
	var boxedNil any = nilReader
	ck(boxedNil == nil, "nil interface boxes to nil")

	// 8) 重复 GC 后所有接口槽仍可用。
	for k := 0; k < 3; k++ {
		gcStress()
		ck(i.Get() == base+6 && h2.I.Get() == base+6, "interface slots after repeated GC")
		ck(c.P.Arr[3].V == base+3 && (*ps2)[2].V == base+10, "nested pointers after repeated GC")
	}

	finish("l06_iface_ptr_holder")
}
