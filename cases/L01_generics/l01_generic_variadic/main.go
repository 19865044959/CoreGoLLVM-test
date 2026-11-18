// L01: 泛型可变参数 + slice 展开：Collect[T](ps ...*T)、Bag[T].AddMany、AppendPairs。
package main

import (
	"fmt"
	"os"
	"runtime"
	"strconv"
	"sync/atomic"
)

type rec struct {
	id   int64
	name string
}

func Collect[T any](ps ...*T) []*T {
	out := make([]*T, len(ps))
	copy(out, ps)
	return out
}

func FirstOr[T any](def *T, ps ...*T) *T {
	if len(ps) == 0 || ps[0] == nil {
		return def
	}
	return ps[0]
}

func AppendPairs[T any](dst []*T, groups ...[]*T) []*T {
	for _, g := range groups {
		dst = append(dst, g...)
	}
	return dst
}

type Bag[T any] struct {
	items []*T
}

func (b *Bag[T]) AddMany(ps ...*T) { b.items = append(b.items, ps...) }
func (b *Bag[T]) Len() int         { return len(b.items) }

var (
	failed  int32
	gcSink  [][]byte
	bagSink []*rec
	uafSeed int64 = 1
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
	n := 32 + int(uafSeed%9)
	orig := make([]*rec, n)
	for i := 0; i < n; i++ {
		orig[i] = &rec{id: int64(i), name: fmt.Sprintf("r%d", i)}
	}

	// slice 展开传入泛型可变参数。
	got := Collect(orig...)
	gcStress()
	ck(len(got) == n, fmt.Sprintf("Collect len got=%d want=%d", len(got), n))
	for i := 0; i < n; i++ {
		ck(got[i] == orig[i], "Collect element pointer identity changed")
		ck(got[i].id == int64(i) && got[i].name == fmt.Sprintf("r%d", i), "Collect element payload corrupted")
	}

	// 逐个实参传入，返回切片与实参顺序一致。
	g2 := Collect(orig[0], orig[1], orig[2])
	gcStress()
	ck(len(g2) == 3 && g2[0] == orig[0] && g2[1] == orig[1] && g2[2] == orig[2], "explicit variadic order corrupted")

	// 空 variadic 与 nil 元素保留。
	empty := Collect[rec]()
	ck(len(empty) == 0, "empty variadic should produce empty slice")
	withNil := Collect(orig[n-1], nil)
	gcStress()
	ck(len(withNil) == 2 && withNil[0] == orig[n-1] && withNil[1] == nil, "nil variadic element not preserved")

	// 默认值语义：无参数/首参 nil 时返回 def。
	def := &rec{id: -7, name: "def"}
	ck(FirstOr(def) == def, "FirstOr default not returned for empty variadic")
	ck(FirstOr(def, orig[3]) == orig[3], "FirstOr lost first explicit argument")
	ck(FirstOr(def, nil) == def, "FirstOr nil first argument should use default")
	ck(FirstOr[int64](nil) == nil, "FirstOr[int64] nil default not nil")

	// 方法上的泛型可变参数 + slice 展开，分段追加。
	bag := &Bag[rec]{}
	bag.AddMany(orig[:n/2]...)
	bag.AddMany(orig[n/2:]...)
	bagSink = orig
	gcStress()
	ck(bag.Len() == n, "Bag.AddMany expanded slices count mismatch")
	for i := 0; i < n; i++ {
		ck(bag.items[i] == orig[i], "Bag element pointer identity changed")
		ck(bag.items[i].name == fmt.Sprintf("r%d", i), "Bag element payload corrupted")
	}

	// GC 后再用变参追加少量元素。
	extra := Collect(orig[0], orig[n-1])
	bag.AddMany(extra...)
	gcStress()
	ck(bag.Len() == n+2, "Bag length after post-GC append mismatch")
	ck(bag.items[n] == orig[0] && bag.items[n+1] == orig[n-1], "post-GC variadic append corrupted")

	// 变参元素本身是 slice（多组展开）。
	groups := AppendPairs[rec](nil, orig[:2], orig[2:4], orig[4:6])
	gcStress()
	ck(len(groups) == 6, "AppendPairs count mismatch")
	for i := 0; i < 6; i++ {
		ck(groups[i] == orig[i], "AppendPairs pointer identity/order corrupted")
	}

	finish("l01_generic_variadic")
}
