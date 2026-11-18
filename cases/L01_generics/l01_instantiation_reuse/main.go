// L01: 同一泛型实例的多处实例化/跨编译单元语义：类型身份共享，不同实例互不干扰。
package main

import (
	"fmt"
	"os"
	"reflect"
	"runtime"
	"strconv"
	"sync/atomic"
)

type payload struct {
	id   int64
	name string
}

type Holder[T any] struct {
	v   *T
	seq int
}

func NewHolder[T any](v *T, seq int) *Holder[T] { return &Holder[T]{v: v, seq: seq} }

func (h *Holder[T]) Get() *T { return h.v }

// 两个独立的构造点，模拟不同包/不同编译单元各自实例化同一泛型类型。
func newHolderFromModuleA(v *payload, seq int) *Holder[payload] { return NewHolder(v, seq) }

func newHolderFromModuleB(v *payload, seq int) *Holder[payload] {
	return NewHolder[payload](v, seq)
}

var (
	failed   int32
	gcSink   [][]byte
	holdSink []any
	uafSeed  int64 = 1
)

func ck(ok bool, msg string) {
	if !ok {
		atomic.AddInt32(&failed, 1)
		fmt.Println("FAIL:", msg)
	}
}

func gcStress() {
	for i := 0; i < 8; i++ {
		b := make([]byte, 2048)
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
	rounds := 60 + int(uafSeed%21)

	// 同一实例 Holder[payload] 在两个构造点产生相同运行时类型，可互换赋值。
	p := &payload{id: 1, name: "shared"}
	a := newHolderFromModuleA(p, 1)
	b := newHolderFromModuleB(p, 2)
	ck(reflect.TypeOf(*a) == reflect.TypeOf(*b), "same instantiation produced distinct types")
	ck(fmt.Sprintf("%T", a) == fmt.Sprintf("%T", b), "same instantiation type names differ")
	var shared *Holder[payload] = a
	ck(shared == a && b.v == shared.v, "instantiation not reusable across construction sites")
	boxed := any(b)
	bb, ok := boxed.(*Holder[payload])
	ck(ok && bb == b, "runtime type assertion to same instantiation failed")

	holdSink = append(holdSink, a, b, shared)
	gcStress()
	ck(a.Get() == p && b.Get() == p && p.id == 1 && p.name == "shared", "shared payload corrupted after GC")
	ck(b.seq == 2 && a.seq == 1, "Holder seq field corrupted after GC")

	// 多种不同实例化并存，互不干扰。
	var h64s []*Holder[int64]
	var h32s []*Holder[int32]
	var hstrs []*Holder[string]
	for i := 0; i < rounds; i++ {
		v64 := int64(i*2 + 1)
		v32 := int32(i)
		s := fmt.Sprintf("s%d", i)
		h64s = append(h64s, NewHolder(&v64, i))
		h32s = append(h32s, NewHolder(&v32, i))
		hstrs = append(hstrs, NewHolder(&s, i))
		if i%16 == 0 {
			gcStress()
		}
	}
	holdSink = append(holdSink, h64s, h32s, hstrs)
	gcStress()

	for i := 0; i < rounds; i++ {
		ck(h64s[i].Get() != nil && *h64s[i].Get() == int64(i*2+1), "Holder[int64] value corrupted")
		ck(h32s[i].Get() != nil && *h32s[i].Get() == int32(i), "Holder[int32] value corrupted")
		ck(hstrs[i].Get() != nil && *hstrs[i].Get() == fmt.Sprintf("s%d", i), "Holder[string] value corrupted")
		ck(h64s[i].seq == i && h32s[i].seq == i && hstrs[i].seq == i, "Holder seq corrupted")
	}

	ck(reflect.TypeOf(*h64s[0]) != reflect.TypeOf(*h32s[0]), "distinct instantiations collapsed")
	ck(reflect.TypeOf(*h32s[0]) != reflect.TypeOf(*hstrs[0]), "distinct instantiations collapsed")
	ck(fmt.Sprintf("%T", h64s[0]) == fmt.Sprintf("%T", NewHolder[int64](nil, 0)),
		"same int64 instantiation type names differ")
	ck(fmt.Sprintf("%T", hstrs[0]) == fmt.Sprintf("%T", NewHolder[string](nil, 0)),
		"same string instantiation type names differ")

	// 复用同一实例的切片/接口容器，GC 后仍可类型断言回原实例。
	sink := []any{h64s[0], h32s[0], hstrs[0]}
	gcStress()
	if v, ok := sink[0].(*Holder[int64]); ok {
		ck(*v.Get() == 1 && v.seq == 0, "asserted Holder[int64] payload corrupted")
	} else {
		ck(false, "Holder[int64] lost identity through interface")
	}
	if v, ok := sink[1].(*Holder[int32]); ok {
		ck(*v.Get() == 0, "asserted Holder[int32] payload corrupted")
	} else {
		ck(false, "Holder[int32] lost identity through interface")
	}
	if v, ok := sink[2].(*Holder[string]); ok {
		ck(*v.Get() == "s0", "asserted Holder[string] payload corrupted")
	} else {
		ck(false, "Holder[string] lost identity through interface")
	}

	finish("l01_instantiation_reuse")
}
