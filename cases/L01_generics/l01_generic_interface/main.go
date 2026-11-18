// L01: 泛型接口：接口嵌入、泛型实现、非泛型实现、委托包装与接口方法值，跨 GC 校验指针派发。
package main

import (
	"fmt"
	"os"
	"runtime"
	"sort"
	"strconv"
	"sync/atomic"
)

type payload struct {
	id   int64
	name string
	blob []byte
}

// ReadOnly 是泛型接口，被 Store 嵌入。
type ReadOnly[T any] interface {
	Get(key string) (*T, bool)
	Len() int
	Keys() []string
}

type Store[T any] interface {
	ReadOnly[T]
	Put(key string, v *T) *T
	Delete(key string) (*T, bool)
}

// memStore 是泛型实现。
type memStore[T any] struct {
	m map[string]*T
}

func NewMemStore[T any]() Store[T] {
	return &memStore[T]{m: make(map[string]*T)}
}

func (s *memStore[T]) Get(key string) (*T, bool) {
	v, ok := s.m[key]
	return v, ok
}

func (s *memStore[T]) Len() int { return len(s.m) }

func (s *memStore[T]) Keys() []string {
	ks := make([]string, 0, len(s.m))
	for k := range s.m {
		ks = append(ks, k)
	}
	sort.Strings(ks)
	return ks
}

func (s *memStore[T]) Put(key string, v *T) *T {
	old := s.m[key]
	s.m[key] = v
	return old
}

func (s *memStore[T]) Delete(key string) (*T, bool) {
	v, ok := s.m[key]
	if ok {
		delete(s.m, key)
	}
	return v, ok
}

// audited 是委托包装：同样满足泛型接口，并统计调用次数。
type audited[T any] struct {
	inner      Store[T]
	gets, puts int
	deletes    int
}

func NewAudited[T any](inner Store[T]) Store[T] { return &audited[T]{inner: inner} }

func (a *audited[T]) Get(key string) (*T, bool) {
	a.gets++
	return a.inner.Get(key)
}

func (a *audited[T]) Len() int       { return a.inner.Len() }
func (a *audited[T]) Keys() []string { return a.inner.Keys() }
func (a *audited[T]) Delete(k string) (*T, bool) {
	a.deletes++
	return a.inner.Delete(k)
}

func (a *audited[T]) Put(key string, v *T) *T {
	a.puts++
	return a.inner.Put(key, v)
}

// payloadStore 是非泛型实现，直接实现 Store[payload] 这一具体实例。
type payloadStore struct {
	m map[string]*payload
}

func (s *payloadStore) Get(key string) (*payload, bool) {
	v, ok := s.m[key]
	return v, ok
}

func (s *payloadStore) Len() int { return len(s.m) }

func (s *payloadStore) Keys() []string {
	ks := make([]string, 0, len(s.m))
	for k := range s.m {
		ks = append(ks, k)
	}
	sort.Strings(ks)
	return ks
}

func (s *payloadStore) Put(key string, v *payload) *payload {
	old := s.m[key]
	s.m[key] = v
	return old
}

func (s *payloadStore) Delete(key string) (*payload, bool) {
	v, ok := s.m[key]
	if ok {
		delete(s.m, key)
	}
	return v, ok
}

var (
	failed      int32
	gcSink      [][]byte
	payloadSink []*payload
	uafSeed     int64 = 1
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
	n := 160 + int(uafSeed%32)

	keys := make([]string, n)
	vals := make([]*payload, n)
	for i := 0; i < n; i++ {
		keys[i] = fmt.Sprintf("key-%03d", i)
		vals[i] = &payload{id: int64(i), name: fmt.Sprintf("v%d", i), blob: make([]byte, 6+i%7)}
		vals[i].blob[0] = byte(i)
		vals[i].blob[len(vals[i].blob)-1] = byte(i * 5)
	}
	payloadSink = vals

	base := NewMemStore[payload]()
	proxy := NewAudited[payload](NewMemStore[payload]())
	concrete := &payloadStore{m: make(map[string]*payload)}
	for i := 0; i < n; i++ {
		ck(base.Put(keys[i], vals[i]) == nil, "first Put should return nil old value")
		proxy.Put(keys[i], vals[i])
		concrete.Put(keys[i], vals[i])
	}
	// 覆盖写：三个独立实现都返回旧指针，Get 看到新指针。
	repl := &payload{id: 9000, name: "repl", blob: []byte{0x11, 0x22}}
	payloadSink = append(payloadSink, repl)
	ck(base.Put(keys[0], repl) == vals[0], "base overwrite Put did not return old pointer")
	ck(proxy.Put(keys[0], repl) == vals[0], "proxy overwrite Put did not return old pointer")
	ck(concrete.Put(keys[0], repl) == vals[0], "concrete overwrite Put did not return old pointer")
	vals[0] = repl

	stores := []Store[payload]{base, proxy, concrete}
	p, ok := base.Get(keys[0])
	ck(ok && p == repl, "overwritten value not visible before GC")

	// 接口视图 + 方法值，跨 GC 调用。
	var ro ReadOnly[payload] = proxy
	fnGet := base.Get
	fnPut := concrete.Put
	gcStress()

	ck(ro.Len() == n, fmt.Sprintf("readonly len got=%d want=%d", ro.Len(), n))
	cks := ro.Keys()
	ck(len(cks) == n && sort.StringsAreSorted(cks), "readonly keys not sorted/complete")

	for si, st := range stores {
		for i := 0; i < n; i++ {
			p, ok := st.Get(keys[i])
			ck(ok && p == vals[i], fmt.Sprintf("store[%d] pointer identity changed at key %s", si, keys[i]))
			if p != nil {
				ck(p.id == vals[i].id && p.name == vals[i].name, fmt.Sprintf("store[%d] payload corrupted", si))
				ck(len(p.blob) == len(vals[i].blob) && len(p.blob) > 0 && p.blob[0] == vals[i].blob[0], "store blob corrupted")
			}
		}
	}

	p, ok = fnGet(keys[n/2])
	ck(ok && p == vals[n/2], "method value Get failed after GC")
	np := &payload{id: 7777, name: "via-method"}
	payloadSink = append(payloadSink, np)
	prev := fnPut("method-key", np)
	ck(prev == nil, "method value Put returned non-nil for new key")
	gcStress()
	p, ok = concrete.Get("method-key")
	ck(ok && p == np, "method value Put result lost after GC")

	// 删除部分 key：三种实现（含委托）保持一致。
	for i := 0; i < n; i += 5 {
		d1, ok1 := base.Delete(keys[i])
		d2, ok2 := proxy.Delete(keys[i])
		d3, ok3 := concrete.Delete(keys[i])
		ck(ok1 && ok2 && ok3, "Delete presence mismatch across stores")
		ck(d1 == vals[i] && d2 == vals[i] && d3 == vals[i], "Delete returned wrong pointer")
	}
	gcStress()
	deleted := (n + 4) / 5
	wantLen := n - deleted
	ck(base.Len() == wantLen, fmt.Sprintf("base len got=%d want=%d", base.Len(), wantLen))
	ck(proxy.Len() == wantLen, fmt.Sprintf("proxy len got=%d want=%d", proxy.Len(), wantLen))
	ck(concrete.Len() == wantLen+1, fmt.Sprintf("concrete len got=%d want=%d", concrete.Len(), wantLen+1))
	for i := 0; i < n; i++ {
		p, ok := base.Get(keys[i])
		if i%5 == 0 {
			ck(!ok, "deleted key resurrected after GC")
			continue
		}
		ck(ok && p == vals[i], "surviving key pointer changed after GC")
	}
	au := proxy.(*audited[payload])
	ck(au.gets > 0 && au.puts > 0 && au.deletes > 0, "audited wrapper counters not updated")
	ck(au.puts == n+1, fmt.Sprintf("audited puts got=%d want=%d", au.puts, n+1))

	// 第二次实例化 Store[string]：相同接口形态，不同 T。
	ss := NewMemStore[string]()
	strs := []string{"alpha", "beta", "gamma"}
	for i := range strs {
		v := strs[i] + "-payload"
		ss.Put(strs[i], &v)
	}
	gcStress()
	ck(ss.Len() == 3, "Store[string] len corrupted")
	for i := range strs {
		p, ok := ss.Get(strs[i])
		ck(ok && p != nil && *p == strs[i]+"-payload", "Store[string] payload corrupted after GC")
	}

	finish("l01_generic_interface")
}
