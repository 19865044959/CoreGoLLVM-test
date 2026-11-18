// L01: 实例化去重——同一实参在多个构造点/复合类型/函数值/并发首次使用时
// 共享同一运行时类型身份；不同实参保持独立布局。GC 后经 interface 回读。
package main

import (
	"fmt"
	"os"
	"reflect"
	"runtime"
	"strconv"
	"sync"
	"sync/atomic"
)

type item struct {
	id   int64
	name string
}

type Holder[T any] struct {
	V   *T
	Seq int64
}

func (h Holder[T]) Any() *T { return h.V }

type anyHolder[T any] interface {
	Any() *T
}

func makeHolderA[T any](v *T, seq int64) Holder[T] {
	return Holder[T]{V: v, Seq: seq}
}

func makeHolderB[T any](v *T, seq int64) Holder[T] {
	return Holder[T]{V: v, Seq: seq + 1}
}

func ident[T any](v T) T { return v }

func ifaceOK[T any](h Holder[T]) bool {
	_, ok := any(h).(anyHolder[T])
	return ok
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

func main() {
	uafSeed = envSeed()
	p := &item{id: 42, name: "shared"}

	// 两个独立构造点实例化 Holder[item]，运行时类型必须相同。
	a := makeHolderA[item](p, 1)
	b := makeHolderB[item](p, 1)
	ck(reflect.TypeOf(a) == reflect.TypeOf(b), "same instantiation produced distinct types")
	ck(reflect.TypeOf(a) == reflect.TypeFor[Holder[item]](), "TypeFor disagrees with value type")
	ck(a.V == p && b.V == p, "constructor lost payload pointer")
	ck(b.Seq == 2, "constructor sequence wrong")

	c := a
	ck(c.V == p && c.Seq == 1, "assignment between same instantiation corrupted")
	ck(ifaceOK(a), "Holder[item] must satisfy anyHolder[item]")
	ck(ifaceOK(b), "second construction must satisfy anyHolder[item]")

	// 泛型函数值：同一实例在多处取值应指向同一份实例化代码。
	f1 := ident[Holder[item]]
	f2 := ident[Holder[item]]
	ck(reflect.ValueOf(f1).Pointer() == reflect.ValueOf(f2).Pointer(),
		"generic function instantiation was not deduplicated")

	// 复合类型中的实例化身份。
	mp := map[string]*Holder[item]{"a": &a}
	ck(reflect.TypeOf(mp) == reflect.TypeFor[map[string]*Holder[item]](), "map composite type mismatch")
	sl := []Holder[item]{a, b}
	ck(reflect.TypeOf(sl) == reflect.TypeFor[[]Holder[item]](), "slice composite type mismatch")
	ch := make(chan Holder[item], 2)
	ch <- a
	ch <- b
	ck(reflect.TypeOf(<-ch) == reflect.TypeOf(a), "channel element type mismatch")
	ck(reflect.TypeOf(ch) == reflect.TypeFor[chan Holder[item]](), "channel composite type mismatch")
	fn := func(h Holder[item]) Holder[item] { return h }
	ck(reflect.TypeOf(fn) == reflect.TypeFor[func(Holder[item]) Holder[item]](), "func type mismatch")
	arr := [2]Holder[item]{a, b}
	ck(reflect.TypeOf(arr) == reflect.TypeFor[[2]Holder[item]](), "array composite type mismatch")
	st := struct{ H Holder[item] }{a}
	ck(reflect.TypeOf(st) == reflect.TypeFor[struct{ H Holder[item] }](), "struct field type mismatch")

	// 不同实参的实例必须互不相等，布局各自独立。
	var i8 int8 = -5
	var u8 uint8 = 250
	s := "text"
	bs := []byte{1, 2, 3}
	hI8 := Holder[int8]{V: &i8, Seq: 11}
	hU8 := Holder[uint8]{V: &u8, Seq: 12}
	hS := Holder[string]{V: &s, Seq: 13}
	hB := Holder[[]byte]{V: &bs, Seq: 14}
	ck(reflect.TypeOf(hI8) != reflect.TypeOf(hU8), "int8/uint8 instantiations collapsed")
	ck(reflect.TypeOf(hI8) != reflect.TypeOf(a), "int8/item instantiations collapsed")
	ck(reflect.TypeOf(hS) != reflect.TypeOf(hB), "string/[]byte instantiations collapsed")
	ck(ifaceOK(hI8) && ifaceOK(hU8) && ifaceOK(hS) && ifaceOK(hB),
		"distinct instantiations lost interface method set")

	boxed := []any{a, hI8, hU8, hS, hB}
	gcStress()

	// 经 interface 断言回各实例，字段偏移与指针必须正确。
	ga, ok := boxed[0].(Holder[item])
	ck(ok && ga.V == p && ga.Seq == 1, "item instance corrupted through interface")
	gi8, ok := boxed[1].(Holder[int8])
	ck(ok && gi8.V != nil && *gi8.V == -5 && gi8.Seq == 11, "int8 instance corrupted through interface")
	gu8, ok := boxed[2].(Holder[uint8])
	ck(ok && gu8.V != nil && *gu8.V == 250 && gu8.Seq == 12, "uint8 instance corrupted through interface")
	gs, ok := boxed[3].(Holder[string])
	ck(ok && gs.V != nil && *gs.V == "text" && gs.Seq == 13, "string instance corrupted through interface")
	gb, ok := boxed[4].(Holder[[]byte])
	ck(ok && gb.V != nil && len(*gb.V) == 3 && (*gb.V)[0] == 1 && gb.Seq == 14,
		"[]byte instance corrupted through interface")

	// 并发首次使用同一实例：所有 goroutine 观察到的类型与函数实例一致。
	workers := 4 + int(uafSeed%5)
	rounds := 80 + int(uafSeed%40)
	var wg sync.WaitGroup
	types := make([]reflect.Type, workers)
	fptrs := make([]uintptr, workers)
	ptrs := make([]*item, workers)
	vals := make([]any, workers)
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			local := &item{id: int64(w), name: fmt.Sprintf("w%d", w)}
			h := makeHolderA[item](local, int64(w))
			var acc Holder[item]
			for i := 0; i < rounds; i++ {
				acc = ident[Holder[item]](h)
			}
			types[w] = reflect.TypeOf(acc)
			fptrs[w] = reflect.ValueOf(ident[Holder[item]]).Pointer()
			ptrs[w] = local
			vals[w] = acc
		}(w)
	}
	wg.Wait()
	gcStress()

	for w := 0; w < workers; w++ {
		ck(types[w] == reflect.TypeFor[Holder[item]](), "concurrent type identity mismatch")
		ck(fptrs[w] == reflect.ValueOf(ident[Holder[item]]).Pointer(),
			"concurrent generic function instance mismatch")
		h, ok := vals[w].(Holder[item])
		ck(ok && h.V == ptrs[w], "concurrent instance lost payload pointer after GC")
		if ok && h.V != nil {
			ck(h.V.id == int64(w), "concurrent payload id corrupted after GC")
			ck(h.V.name == fmt.Sprintf("w%d", w), "concurrent payload name corrupted after GC")
			ck(h.Seq == int64(w), "concurrent sequence corrupted after GC")
		}
	}

	finish("l01_generic_inst_dedup")
}
