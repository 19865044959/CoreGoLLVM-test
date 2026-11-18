// L01: 泛型 slice/map 容器存指针：slice 多次扩容、map 删除、GC 后指针身份与内容一致。
package main

import (
	"fmt"
	"os"
	"runtime"
	"strconv"
	"sync/atomic"
)

type item struct {
	id   int64
	name string
	blob []byte
}

type Vec[T any] struct {
	items []*T
}

func (v *Vec[T]) Add(p *T)    { v.items = append(v.items, p) }
func (v *Vec[T]) At(i int) *T { return v.items[i] }
func (v *Vec[T]) Len() int    { return len(v.items) }
func (v *Vec[T]) Cap() int    { return cap(v.items) }

type GMap[K comparable, V any] struct {
	m map[K]*V
}

func NewGMap[K comparable, V any]() *GMap[K, V] {
	return &GMap[K, V]{m: make(map[K]*V)}
}

func (g *GMap[K, V]) Set(k K, p *V)      { g.m[k] = p }
func (g *GMap[K, V]) Get(k K) (*V, bool) { p, ok := g.m[k]; return p, ok }
func (g *GMap[K, V]) Delete(k K)         { delete(g.m, k) }
func (g *GMap[K, V]) Len() int           { return len(g.m) }

var (
	failed  int32
	gcSink  [][]byte
	vecSink []*item
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
	n := 600 + int(uafSeed%97)

	vec := &Vec[item]{}
	orig := make([]*item, n)
	for i := 0; i < n; i++ {
		it := &item{
			id:   int64(i),
			name: fmt.Sprintf("i%d", i),
			blob: make([]byte, 16+i%7),
		}
		it.blob[0] = byte(i)
		it.blob[len(it.blob)-1] = byte(i >> 1)
		orig[i] = it
		vec.Add(it)
		if i == n/2 {
			gcStress() // 扩容进行中触发 GC
		}
	}
	vecSink = orig
	gcStress()

	ck(vec.Len() == n, fmt.Sprintf("vec len got=%d want=%d", vec.Len(), n))
	for i := 0; i < n; i++ {
		p := vec.At(i)
		ck(p == orig[i], "vec element pointer identity changed")
		ck(p.id == int64(i), "vec element id corrupted")
		ck(p.name == fmt.Sprintf("i%d", i), "vec element name corrupted")
		ck(len(p.blob) == 16+i%7, "vec element blob length corrupted")
		ck(p.blob[0] == byte(i) && p.blob[len(p.blob)-1] == byte(i>>1), "vec element blob payload corrupted")
	}

	// 泛型 map：int64 键与 string 键两种实例化，删除每 7 个中的 1 个。
	gmap := NewGMap[int64, item]()
	smap := NewGMap[string, item]()
	for i := 0; i < n; i++ {
		gmap.Set(int64(i), orig[i])
		smap.Set(orig[i].name, orig[i])
	}
	deleted := 0
	for i := 0; i < n; i++ {
		if i%7 == 3 {
			gmap.Delete(int64(i))
			smap.Delete(orig[i].name)
			deleted++
		}
	}
	gcStress()

	ck(gmap.Len() == n-deleted, fmt.Sprintf("gmap len got=%d want=%d", gmap.Len(), n-deleted))
	ck(smap.Len() == n-deleted, fmt.Sprintf("smap len got=%d want=%d", smap.Len(), n-deleted))
	for i := 0; i < n; i++ {
		p, ok := gmap.Get(int64(i))
		if i%7 == 3 {
			ck(!ok, "deleted gmap entry resurrected")
			continue
		}
		ck(ok && p == orig[i], "gmap pointer identity changed")
		ck(p.id == int64(i) && p.name == fmt.Sprintf("i%d", i), "gmap payload corrupted")

		sp, ok := smap.Get(orig[i].name)
		ck(ok && sp == p, "smap and gmap disagree on pointer identity")
	}

	// GC 后再追加，触发又一次扩容；旧元素与新元素都必须在下一轮 GC 后完好。
	extra := make([]*item, 50)
	for i := range extra {
		extra[i] = &item{id: int64(n + i), name: fmt.Sprintf("x%d", i), blob: []byte{byte(i), byte(i + 1)}}
		vec.Add(extra[i])
	}
	vecSink = append(vecSink, extra...)
	gcStress()

	ck(vec.Len() == n+len(extra), "vec len after growth corrupted")
	for i, p := range extra {
		got := vec.At(n + i)
		ck(got == p, "appended element pointer identity changed")
		ck(got.id == int64(n+i) && got.name == fmt.Sprintf("x%d", i), "appended element payload corrupted")
		ck(len(got.blob) == 2 && got.blob[1] == byte(i+1), "appended element blob corrupted")
	}
	ck(vec.At(0) == orig[0] && vec.At(n-1) == orig[n-1], "old elements lost after growth+GC")
	ck(vec.Cap() >= vec.Len(), "vec capacity invariant broken")

	finish("l01_slice_map_generic")
}
