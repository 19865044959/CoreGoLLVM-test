// L01: 泛型前缀树（node[V] 嵌套 map）——插入/覆盖/删除/前缀计数/有序遍历，
// 长压测下每 N 次操作 GC；验证指针载荷、空 key、nil 值与 TORTURE_ROUNDS。
package main

import (
	"fmt"
	"os"
	"runtime"
	"strconv"
	"strings"
	"sync/atomic"
)

type entry struct {
	ID    int64
	Name  string
	Score int64
}

type node[V any] struct {
	kids map[byte]*node[V]
	val  *V
	has  bool
	n    int // 子树中带值的节点数
}

type Trie[V any] struct {
	root *node[V]
	size int
}

func NewTrie[V any]() *Trie[V] {
	return &Trie[V]{root: &node[V]{kids: make(map[byte]*node[V])}}
}

func insert[V any](n *node[V], key string, i int, v *V) bool {
	if i == len(key) {
		fresh := !n.has
		n.has = true
		n.val = v
		if fresh {
			n.n++
		}
		return fresh
	}
	c := key[i]
	ch := n.kids[c]
	if ch == nil {
		ch = &node[V]{kids: make(map[byte]*node[V])}
		n.kids[c] = ch
	}
	fresh := insert(ch, key, i+1, v)
	if fresh {
		n.n++
	}
	return fresh
}

func (t *Trie[V]) Insert(key string, v *V) {
	if insert(t.root, key, 0, v) {
		t.size++
	}
}

func (t *Trie[V]) Get(key string) (*V, bool) {
	n := t.root
	for i := 0; i < len(key); i++ {
		n = n.kids[key[i]]
		if n == nil {
			return nil, false
		}
	}
	if !n.has {
		return nil, false
	}
	return n.val, true
}

func remove[V any](n *node[V], key string, i int) bool {
	if i == len(key) {
		if !n.has {
			return false
		}
		n.has = false
		n.val = nil
		n.n--
		return true
	}
	c := key[i]
	ch := n.kids[c]
	if ch == nil {
		return false
	}
	if !remove(ch, key, i+1) {
		return false
	}
	n.n--
	if ch.n == 0 && len(ch.kids) == 0 {
		delete(n.kids, c)
	}
	return true
}

func (t *Trie[V]) Delete(key string) bool {
	if remove(t.root, key, 0) {
		t.size--
		return true
	}
	return false
}

func (t *Trie[V]) Len() int { return t.size }

func (t *Trie[V]) PrefixCount(prefix string) int {
	n := t.root
	for i := 0; i < len(prefix); i++ {
		n = n.kids[prefix[i]]
		if n == nil {
			return 0
		}
	}
	return n.n
}

func walkNode[V any](n *node[V], prefix string, fn func(string, *V)) {
	if n.has {
		fn(prefix, n.val)
	}
	for b := 0; b < 256; b++ {
		if ch := n.kids[byte(b)]; ch != nil {
			walkNode(ch, prefix+string(byte(b)), fn)
		}
	}
}

func (t *Trie[V]) Walk(fn func(string, *V)) {
	walkNode(t.root, "", fn)
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

func tortureRounds(def int) int {
	s := os.Getenv("TORTURE_ROUNDS")
	if s == "" {
		return def
	}
	n, err := strconv.Atoi(s)
	if err != nil || n <= 0 {
		return def
	}
	if n > 16 {
		n = 16
	}
	return n
}

func main() {
	uafSeed = envSeed()
	rounds := tortureRounds(1)
	n := (1100 + int(uafSeed%201)) * rounds

	tr := NewTrie[entry]()
	refs := make([]*entry, n)
	keys := make([]string, n)
	deleted := make([]bool, n)
	live := 0
	for i := 0; i < n; i++ {
		key := fmt.Sprintf("k%02d/%04d", i%13, i)
		keys[i] = key
		p := &entry{ID: int64(i), Name: fmt.Sprintf("e%04d", i), Score: int64(i % 97)}
		if i%3 == 0 {
			p = &entry{ID: int64(i), Name: "over", Score: int64(i * 2)}
		}
		tr.Insert(key, p)
		refs[i] = p
		live++
		if i%5 == 4 {
			ck(tr.Delete(key), "delete existing trie key failed")
			ck(!tr.Delete(key), "second delete of trie key must return false")
			deleted[i] = true
			refs[i] = nil
			live--
		}
		if i%256 == 0 {
			gcStress()
		}
	}

	// 显式覆盖已存在的 key：指针替换、size 不变。
	overKey := keys[1]
	old, ok := tr.Get(overKey)
	ck(ok && old == refs[1], "pre-overwrite trie value mismatch")
	newP := &entry{ID: 777777, Name: "explicit-over", Score: 99}
	tr.Insert(overKey, newP)
	got, ok := tr.Get(overKey)
	ck(ok && got == newP && got != old, "trie overwrite did not replace pointer")
	refs[1] = newP

	// 删除后重新插入第一个被删 key。
	reinsert := -1
	for i := 0; i < n; i++ {
		if deleted[i] {
			reinsert = i
			break
		}
	}
	if reinsert >= 0 {
		rp := &entry{ID: int64(reinsert), Name: "reinsert", Score: 5}
		tr.Insert(keys[reinsert], rp)
		refs[reinsert] = rp
		deleted[reinsert] = false
		live++
		got, ok := tr.Get(keys[reinsert])
		ck(ok && got == rp, "reinserted trie value mismatch")
	}

	// 空 key 挂在根节点；nil 值通过 has 标志与"不存在"区分。
	rootVal := &entry{ID: -1, Name: "root"}
	tr.Insert("", rootVal)
	live++
	tr.Insert("nil-value", nil)
	live++
	got, ok = tr.Get("nil-value")
	ck(ok && got == nil, "nil trie value must be present with nil payload")
	ck(tr.Delete("nil-value"), "delete nil-value trie key failed")
	live--
	if _, ok = tr.Get("nil-value"); ok {
		ck(false, "deleted nil-value trie key is still visible")
	}

	ck(tr.Len() == live, fmt.Sprintf("trie len got=%d want=%d", tr.Len(), live))
	ck(tr.PrefixCount("") == live, "empty prefix count must equal size")
	ck(tr.PrefixCount("zz/") == 0, "absent prefix count must be zero")

	gcStress()
	for i := 0; i < n; i++ {
		p, ok := tr.Get(keys[i])
		if deleted[i] {
			ck(!ok, "deleted trie key became visible after GC")
			continue
		}
		ck(ok && p != nil, "surviving trie value missing after GC")
		if !ok || p == nil {
			continue
		}
		ck(p == refs[i], "trie lost pointer identity after GC")
		ck(p.ID == refs[i].ID && p.Name == refs[i].Name && p.Score == refs[i].Score,
			"trie payload corrupted after GC")
	}
	got, ok = tr.Get("")
	ck(ok && got == rootVal, "root value lost after GC")

	// 前缀计数逐个对照。
	for g := 0; g < 13; g++ {
		prefix := fmt.Sprintf("k%02d/", g)
		want := 0
		for i := 0; i < n; i++ {
			if !deleted[i] && strings.HasPrefix(keys[i], prefix) {
				want++
			}
		}
		ck(tr.PrefixCount(prefix) == want, fmt.Sprintf("prefix %s count mismatch", prefix))
	}

	// 有序遍历：字节序递增、计数与载荷和一致。
	var count int
	var sum int64
	sorted := true
	first := true
	prev := ""
	tr.Walk(func(k string, v *entry) {
		count++
		if v != nil {
			sum += v.ID
		}
		if !first && k <= prev {
			sorted = false
		}
		prev = k
		first = false
	})
	ck(count == live, fmt.Sprintf("walk count got=%d want=%d", count, live))
	ck(sorted, "walk order is not strictly increasing")
	wantSum := rootVal.ID
	for i := 0; i < n; i++ {
		if !deleted[i] && refs[i] != nil {
			wantSum += refs[i].ID
		}
	}
	ck(sum == wantSum, "walk payload sum mismatch")

	gcStress()
	ck(tr.Len() == live, "trie len changed after final GC")
	ck(tr.PrefixCount("") == live, "empty prefix count changed after final GC")

	// 空 trie 边界。
	et := NewTrie[int64]()
	if _, ok := et.Get("x"); ok {
		ck(false, "empty trie must not find x")
	}
	ck(!et.Delete("x"), "empty trie delete must return false")
	ck(et.PrefixCount("") == 0, "empty trie prefix count must be zero")
	visited := 0
	et.Walk(func(string, *int64) { visited++ })
	ck(visited == 0, "empty trie walk must visit nothing")
	ck(et.Len() == 0, "empty trie len must be zero")
	var five int64 = 5
	et.Insert("x", &five)
	gotI, ok := et.Get("x")
	ck(ok && gotI != nil && *gotI == 5, "single-entry trie mismatch")
	ck(et.PrefixCount("x") == 1, "single-entry prefix count mismatch")

	// 第二个实例化 Trie[int64]。
	it := NewTrie[int64]()
	var wantIntSum int64
	const in = 100
	for i := 0; i < in; i++ {
		v := int64(i * 5)
		wantIntSum += v
		it.Insert(fmt.Sprintf("i%03d", i), &v)
	}
	gcStress()
	var intSum int64
	it.Walk(func(_ string, v *int64) {
		if v != nil {
			intSum += *v
		}
	})
	ck(it.Len() == in, "int64 trie len mismatch")
	ck(it.PrefixCount("i") == in, "int64 trie prefix count mismatch")
	ck(intSum == wantIntSum, "int64 trie payload sum mismatch")

	finish("l01_generic_trie")
}
