// L13: 前缀 trie 多前缀匹配，返回去重排序的 id 集合；含空前缀与重复插入。
package main

import (
	"fmt"
	"math/rand"
	"os"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync/atomic"
)

var (
	failed  int32
	gcSink  [][]byte
	curSeed int64
)

func ck(ok bool, msg string) {
	if !ok {
		atomic.AddInt32(&failed, 1)
		fmt.Println("FAIL:", msg)
	}
}

func ckSeed(ok bool, msg string) {
	if !ok {
		atomic.AddInt32(&failed, 1)
		fmt.Printf("FAIL: %s (seed=%d)\n", msg, curSeed)
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
		fmt.Printf("FAILED %s (%d failed checks)\n", name, n)
		os.Exit(1)
	}
	fmt.Println("ALL PASS " + name)
}

type trieNode struct {
	children map[byte]*trieNode
	ids      []int
}

func (n *trieNode) insert(key string, id int) {
	cur := n
	for i := 0; i < len(key); i++ {
		if cur.children == nil {
			cur.children = make(map[byte]*trieNode)
		}
		c := cur.children[key[i]]
		if c == nil {
			c = &trieNode{}
			cur.children[key[i]] = c
		}
		cur = c
	}
	for _, x := range cur.ids {
		if x == id {
			return
		}
	}
	cur.ids = append(cur.ids, id)
}

func (n *trieNode) collect(out map[int]struct{}) {
	for _, id := range n.ids {
		out[id] = struct{}{}
	}
	for _, c := range n.children {
		c.collect(out)
	}
}

func (t *trieNode) match(prefix string) []int {
	cur := t
	for i := 0; i < len(prefix); i++ {
		c := cur.children[prefix[i]]
		if c == nil {
			return nil
		}
		cur = c
	}
	set := make(map[int]struct{})
	cur.collect(set)
	out := make([]int, 0, len(set))
	for id := range set {
		out = append(out, id)
	}
	sort.Ints(out)
	return out
}

type refEntry struct {
	key string
	ids []int
}

func refInsert(entries []refEntry, key string, id int) []refEntry {
	for i := range entries {
		if entries[i].key == key {
			for _, x := range entries[i].ids {
				if x == id {
					return entries
				}
			}
			entries[i].ids = append(entries[i].ids, id)
			return entries
		}
	}
	return append(entries, refEntry{key: key, ids: []int{id}})
}

func refMatch(entries []refEntry, prefix string) []int {
	set := make(map[int]struct{})
	for _, e := range entries {
		if strings.HasPrefix(e.key, prefix) {
			for _, id := range e.ids {
				set[id] = struct{}{}
			}
		}
	}
	out := make([]int, 0, len(set))
	for id := range set {
		out = append(out, id)
	}
	sort.Ints(out)
	return out
}

func equalInts(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func envSeed() int64 {
	s := os.Getenv("UAF_SEED")
	if s == "" {
		return 1
	}
	v, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return 1
	}
	return v
}

func randomKey(r *rand.Rand) string {
	const letters = "abcd"
	n := 1 + r.Intn(8)
	b := make([]byte, n)
	for i := range b {
		b[i] = letters[r.Intn(len(letters))]
	}
	return string(b)
}

func main() {
	curSeed = envSeed()

	// 固定数据：覆盖重复 key/id、前缀命中与未命中。
	var t trieNode
	fixed := []struct {
		key string
		id  int
	}{
		{"ab", 3}, {"abc", 1}, {"abc", 2}, {"abcd", 2},
		{"abd", 1}, {"b", 4}, {"ba", 5}, {"ba", 5},
	}
	for _, f := range fixed {
		t.insert(f.key, f.id)
	}
	gcStress()

	fixedChecks := []struct {
		prefix string
		want   []int
	}{
		{"", []int{1, 2, 3, 4, 5}},
		{"a", []int{1, 2, 3}},
		{"ab", []int{1, 2, 3}},
		{"abc", []int{1, 2}},
		{"abcd", []int{2}},
		{"abd", []int{1}},
		{"b", []int{4, 5}},
		{"ba", []int{5}},
		{"c", nil},
		{"zzz", nil},
	}
	for _, fc := range fixedChecks {
		got := t.match(fc.prefix)
		ck(equalInts(got, fc.want), fmt.Sprintf("fixed match(%q): got %v want %v", fc.prefix, got, fc.want))
	}

	// 返回值必须是拷贝：改结果不影响 trie 内部状态。
	g1 := t.match("ab")
	if len(g1) > 0 {
		g1[0] = 999
	}
	g2 := t.match("ab")
	ck(len(g2) > 0 && g2[0] != 999, "match result is an independent copy")

	gcStress()
	got := t.match("abc")
	ck(equalInts(got, []int{1, 2}), "fixed trie survives GC")

	// 随机路径：随机键/随机 id/重复插入，trie 与暴力参考实现逐前缀对照。
	var tr trieNode
	r := rand.New(rand.NewSource(curSeed))
	var entries []refEntry
	for i := 0; i < 400; i++ {
		key := randomKey(r)
		id := 1 + r.Intn(60)
		tr.insert(key, id)
		entries = refInsert(entries, key, id)
	}
	gcStress()

	prefixes := []string{"", "a", "b", "c", "d", "ab", "abc", "abcd", "dd", "zz", "aaaa"}
	for i := 0; i < 150; i++ {
		if i%2 == 0 && len(entries) > 0 {
			k := entries[r.Intn(len(entries))].key
			n := r.Intn(len(k) + 1)
			prefixes = append(prefixes, k[:n])
		} else {
			l := r.Intn(6)
			b := make([]byte, l)
			for j := range b {
				b[j] = "abcd"[r.Intn(4)]
			}
			prefixes = append(prefixes, string(b))
		}
	}
	for _, p := range prefixes {
		got := tr.match(p)
		want := refMatch(entries, p)
		ckSeed(equalInts(got, want), fmt.Sprintf("random match(%q): got %v want %v", p, got, want))
		for i := 1; i < len(got); i++ {
			if got[i-1] >= got[i] {
				ckSeed(false, "match result not sorted/unique")
				break
			}
		}
	}

	// 追加一批后 GC，再查一次必须看到新 id。
	before := len(entries)
	for i := 0; i < 50; i++ {
		key := randomKey(r)
		id := 1000 + i
		tr.insert(key, id)
		entries = refInsert(entries, key, id)
	}
	ckSeed(len(entries) >= before, "reference entries grew")
	gcStress()
	p := entries[len(entries)-1].key
	ckSeed(equalInts(tr.match(p), refMatch(entries, p)), "match after additional inserts and GC")
	ckSeed(equalInts(tr.match(""), refMatch(entries, "")), "empty prefix returns all ids after GC")

	runtime.KeepAlive(t)
	runtime.KeepAlive(tr)
	runtime.KeepAlive(entries)
	finish("l13_prefix_trie_match")
}
