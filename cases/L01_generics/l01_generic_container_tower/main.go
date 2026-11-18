// L01: 泛型容器嵌套（Index[string, Stack[item]] 与 Index[int, Index[string, item]]）。
// 覆盖嵌套实例化的字段布局、指针存活与多轮 GC；输出确定。
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
}

type Stack[T any] struct {
	elems []*T
}

func (s *Stack[T]) Push(v *T) { s.elems = append(s.elems, v) }

func (s *Stack[T]) Pop() *T {
	n := len(s.elems)
	if n == 0 {
		return nil
	}
	v := s.elems[n-1]
	s.elems[n-1] = nil
	s.elems = s.elems[:n-1]
	return v
}

func (s *Stack[T]) Len() int { return len(s.elems) }

func (s *Stack[T]) At(i int) *T { return s.elems[i] }

type Index[K comparable, V any] struct {
	m map[K]*V
}

func NewIndex[K comparable, V any]() *Index[K, V] {
	return &Index[K, V]{m: make(map[K]*V)}
}

func (x *Index[K, V]) Put(k K, v *V) { x.m[k] = v }

func (x *Index[K, V]) Get(k K) (*V, bool) {
	v, ok := x.m[k]
	return v, ok
}

func (x *Index[K, V]) Delete(k K) { delete(x.m, k) }

func (x *Index[K, V]) Len() int { return len(x.m) }

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

func tenantKey(t int) string { return fmt.Sprintf("tenant-%02d", t) }

func main() {
	uafSeed = envSeed()
	tenants := 5 + int(uafSeed%4)
	per := 18 + int(uafSeed%7)

	// 二层嵌套：Index[string, Stack[item]]。
	tower := NewIndex[string, Stack[item]]()
	refs := make([][]*item, tenants)
	stacks := make([]*Stack[item], tenants)
	for t := 0; t < tenants; t++ {
		st := &Stack[item]{}
		refs[t] = make([]*item, per)
		for i := 0; i < per; i++ {
			p := &item{id: int64(t*1000 + i), name: fmt.Sprintf("t%d-i%d", t, i)}
			refs[t][i] = p
			st.Push(p)
		}
		stacks[t] = st
		tower.Put(tenantKey(t), st)
		if t%2 == 0 {
			gcStress()
		}
	}
	gcStress()

	ck(tower.Len() == tenants, "tower lost tenants after GC")

	// 每个 tenant 弹 3 个（LIFO），被弹出的指针仍由局部变量保活。
	popped := make([][]*item, tenants)
	for t := 0; t < tenants; t++ {
		popped[t] = make([]*item, 0, 3)
		for j := 0; j < 3; j++ {
			popped[t] = append(popped[t], stacks[t].Pop())
		}
	}
	gcStress()

	for t := 0; t < tenants; t++ {
		st, ok := tower.Get(tenantKey(t))
		ck(ok && st == stacks[t], "nested stack pointer identity lost")
		if !ok || st == nil {
			continue
		}
		ck(st.Len() == per-3, fmt.Sprintf("stack len got=%d want=%d", st.Len(), per-3))
		for i := 0; i < per-3; i++ {
			p := st.At(i)
			ck(p == refs[t][i], "stack element pointer identity lost after GC")
			if p == nil {
				continue
			}
			ck(p.id == int64(t*1000+i), "nested payload id corrupted after GC")
			ck(p.name == fmt.Sprintf("t%d-i%d", t, i), "nested payload name corrupted after GC")
		}
		for j := 0; j < 3; j++ {
			p := popped[t][j]
			want := refs[t][per-1-j]
			ck(p == want, "popped pointer identity lost after GC")
			if p != nil {
				ck(p.id == want.id && p.name == want.name, "popped payload corrupted after GC")
			}
		}
	}

	// 删掉中间 tenant，但保留栈指针；GC 后该栈及其载荷仍必须完整。
	mid := tenants / 2
	keep := stacks[mid]
	keepKey := tenantKey(mid)
	tower.Delete(keepKey)
	gcStress()
	_, ok := tower.Get(keepKey)
	ck(!ok, "deleted nested container is still visible")
	ck(keep.Len() == per-3, "kept stack lost elements after map delete + GC")
	for i := 0; i < per-3; i++ {
		ck(keep.At(i) == refs[mid][i], "kept stack payload identity lost after GC")
	}
	tower.Put(keepKey, keep)
	ck(tower.Len() == tenants, "reinserted nested container not visible")

	// 二层嵌套的另一种组合：Index[int, Index[string, item]]。
	const groups = 6
	const perGroup = 5
	nested := NewIndex[int, Index[string, item]]()
	groupRefs := make([][]*item, groups)
	for g := 0; g < groups; g++ {
		inner := NewIndex[string, item]()
		groupRefs[g] = make([]*item, perGroup)
		for k := 0; k < perGroup; k++ {
			p := &item{id: int64(100000 + g*100 + k), name: fmt.Sprintf("g%d-k%d", g, k)}
			groupRefs[g][k] = p
			inner.Put(fmt.Sprintf("k%d", k), p)
		}
		nested.Put(g, inner)
	}
	gcStress()

	var gotSum, wantSum int64
	for g := 0; g < groups; g++ {
		in, ok := nested.Get(g)
		ck(ok && in != nil, "second-level index lost after GC")
		if !ok || in == nil {
			continue
		}
		ck(in.Len() == perGroup, "second-level index size mismatch")
		for k := 0; k < perGroup; k++ {
			p, ok := in.Get(fmt.Sprintf("k%d", k))
			ck(ok && p == groupRefs[g][k], "second-level pointer identity lost")
			if p != nil {
				ck(p.id == int64(100000+g*100+k), "second-level payload id corrupted")
				gotSum += p.id
				wantSum += int64(100000 + g*100 + k)
			}
		}
	}
	ck(gotSum == wantSum, "second-level payload sum mismatch after GC")

	// 另一组嵌套实例化：Index[string, Stack[int64]]，与 item 层互不干扰。
	flat := NewIndex[string, Stack[int64]]()
	fs := &Stack[int64]{}
	flatRefs := make([]*int64, 6)
	for i := 0; i < 6; i++ {
		v := int64(i*7 + 1)
		flatRefs[i] = &v
		fs.Push(flatRefs[i])
	}
	flat.Put("flat", fs)
	gcStress()
	got, ok := flat.Get("flat")
	ck(ok && got == fs, "int64 nested stack lost after GC")
	if ok && got != nil {
		ck(got.Len() == 6, "int64 nested stack len mismatch")
		for i := 0; i < 6; i++ {
			p := got.At(i)
			ck(p == flatRefs[i], "int64 nested stack lost payload identity")
			if p != nil {
				ck(*p == int64(i*7+1), "int64 nested stack payload corrupted")
			}
		}
	}

	finish("l01_generic_container_tower")
}
