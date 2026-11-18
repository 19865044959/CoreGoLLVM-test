// L01: 带方法的类型约束：接口方法约束、方法+底层类型 union、comparable+方法组合，跨 GC 校验。
package main

import (
	"fmt"
	"os"
	"runtime"
	"strconv"
	"strings"
	"sync/atomic"
)

type keyed interface {
	Key() int64
	Label() string
}

type entity struct {
	id    int64
	label string
	next  *entity
}

func (e entity) Key() int64    { return e.id }
func (e entity) Label() string { return e.label }

var (
	failed  int32
	gcSink  [][]byte
	entSink []*entity
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

func nextRand(state *uint64) uint64 {
	*state = *state*6364136223846793005 + 1442695040888963407
	return *state >> 33
}

// maxKey 的约束是"有 Key/Label 方法"的接口（ent Limiter[Q interface{ Limit(int) }] 同形态）。
func maxKey[E keyed](xs []E) (E, bool) {
	var zero E
	if len(xs) == 0 {
		return zero, false
	}
	best := xs[0]
	for _, x := range xs[1:] {
		if x.Key() > best.Key() {
			best = x
		}
	}
	return best, true
}

func sumKeys[E keyed](xs []E) int64 {
	var s int64
	for _, x := range xs {
		s += x.Key()
	}
	return s
}

func findByLabel[E keyed](xs []E, label string) (E, bool) {
	var zero E
	for _, x := range xs {
		if x.Label() == label {
			return x, true
		}
	}
	return zero, false
}

// indexByKey 要求 E 同时 comparable 且带 Key 方法，作为 map 键。
func indexByKey[E interface {
	comparable
	keyed
}](xs []E) map[E]int {
	m := make(map[E]int, len(xs))
	for i, x := range xs {
		m[x] = i
	}
	return m
}

// weighted 是"方法 + 底层类型 union"的约束，仅可作 constraint 使用。
type weighted interface {
	~int64 | ~int32
	Weight() int64
}

type w64 int64

func (w w64) Weight() int64 { return int64(w) * 2 }

type w32 int32

func (w w32) Weight() int64 { return int64(w) * 3 }

func totalWeight[T weighted](xs []T) int64 {
	var s int64
	for _, x := range xs {
		s += x.Weight()
	}
	return s
}

func main() {
	uafSeed = envSeed()
	n := 180 + int(uafSeed%20)

	ents := make([]*entity, n)
	for i := 0; i < n; i++ {
		ents[i] = &entity{id: int64(i * 2), label: fmt.Sprintf("n%d", i)}
		if i > 0 {
			ents[i].next = ents[i-1] // 指针链，GC 必须保活
		}
	}
	entSink = ents

	// UAF_SEED 只打乱呈现顺序，期望值由同一批指针独立计算。
	order := make([]int, n)
	for i := range order {
		order[i] = i
	}
	state := uint64(uafSeed)*0x9e3779b97f4a7c15 + 7
	for i := n - 1; i > 0; i-- {
		j := int(nextRand(&state) % uint64(i+1))
		order[i], order[j] = order[j], order[i]
	}
	xs := make([]*entity, n)
	var wantSum int64
	for i, o := range order {
		xs[i] = ents[o]
		wantSum += ents[o].id
	}

	gcStress()
	ck(sumKeys(xs) == wantSum, fmt.Sprintf("sumKeys got=%d want=%d", sumKeys(xs), wantSum))

	best, ok := maxKey(xs)
	ck(ok && best == ents[n-1], "maxKey returned wrong pointer after GC")
	ck(best != nil && best.Key() == int64((n-1)*2) && best.Label() == fmt.Sprintf("n%d", n-1), "maxKey payload corrupted")

	mid := ents[n/2]
	found, ok := findByLabel(xs, mid.Label())
	ck(ok && found == mid, "findByLabel pointer identity changed after GC")
	ck(found != nil && found.id == mid.id, "findByLabel payload corrupted")
	_, ok = findByLabel(xs, "absent-label")
	ck(!ok, "findByLabel matched an absent label")

	// comparable + 方法约束：值作为 map key，GC 后按值查找。
	evals := make([]entity, n)
	for i, o := range order {
		evals[i] = *ents[o]
	}
	idx := indexByKey(evals)
	gcStress()
	for i, o := range order {
		pos, ok := idx[*ents[o]]
		ck(ok && pos == i, fmt.Sprintf("indexByKey lookup failed for id=%d", ents[o].id))
	}
	ck(idx[*ents[n/3]] == func() int {
		for i, o := range order {
			if o == n/3 {
				return i
			}
		}
		return -1
	}(), "indexByKey value mismatch")

	// 方法 + union 约束：两种底层类型各自实例化。
	m64 := make([]w64, 40)
	m32 := make([]w32, 40)
	for i := range m64 {
		m64[i] = w64(i + 1)
		m32[i] = w32(i + 1)
	}
	gcStress()
	var want64, want32 int64
	for i := 1; i <= 40; i++ {
		want64 += int64(i) * 2
		want32 += int64(i) * 3
	}
	ck(totalWeight(m64) == want64, fmt.Sprintf("totalWeight[w64] got=%d want=%d", totalWeight(m64), want64))
	ck(totalWeight(m32) == want32, fmt.Sprintf("totalWeight[w32] got=%d want=%d", totalWeight(m32), want32))

	// 方法集在取值/指针两种接收者下都要正确派发。
	var k keyed = ents[n-1]
	ck(k.Key() == int64((n-1)*2), "keyed interface over pointer receiver corrupted")
	ck(strings.HasPrefix(k.Label(), "n"), "keyed Label returned wrong text")

	// 空切片边界：返回零值且 ok=false，不 panic。
	e, ok := maxKey([]*entity{})
	ck(!ok && e == nil, "maxKey on empty slice should return zero,false")

	finish("l01_constraint_methods")
}
