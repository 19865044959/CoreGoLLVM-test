// X02: 局部指针数组 liveness（P0）。
// 对准 go#82026 形态：`arr := [N]T{...}`（T 含 *payload），下标 m 由 seed/轮次派生
// （非常量，编译器无法折叠），call 前取 q := &arr[m].p（同时保留值拷贝 v := arr[m].p），
// noinline callee 内 runtime.GC() 后按同 size class 批量分配迫使复用，返回后通过
// q / v / arr[m] 三路读取并校验 canary；再做第二轮 GC（取址跨多轮保留），随后
// 在 GC 后改写 arr[m].p 并用第三轮 GC 验证新指针与同元素其它字段（element2 的 q）。
// 8 种 shape 矩阵：[1/2/4/8] × element{1 ptr, 2 ptrs}；另有一个跨轮保留取址的
// held [8]element2 数组。探针：canary + weak.Make 强引用断言（强引用在时 Value 必须非 nil）。
package main

import (
	"fmt"
	"os"
	"runtime"
	"runtime/debug"
	"strconv"
	"sync"
	"sync/atomic"
	"time"
	"weak"
)

const (
	scenario           = "x02_localarray_liveness"
	poisonIDBase       = int64(1) << 50
	fixedRoundsDefault = 8
)

type payload struct {
	magic uint64
	id    int64
	aux   int64
	pad   [5]uint64
}

func magicFor(seed uint64, id int64) uint64 {
	x := seed*0x9E3779B97F4A7C15 + uint64(id)*0x2545F4914F6CDD1D + 0x5EED5EED
	x ^= x >> 31
	x *= 0xBF58476D1CE4E5B9
	x ^= x >> 27
	return x
}

func newPayload(seed uint64, id int64) *payload {
	return &payload{magic: magicFor(seed, id), id: id, aux: id ^ 0x5A5A5A5A}
}

//go:noinline
func checkPayload(p *payload, seed uint64, want int64, where string) {
	if p == nil {
		failf("%s: nil payload (want id=%d): UAF/漏标", where, want)
	}
	if p.id != want || p.magic != magicFor(seed, p.id) || p.aux != p.id^0x5A5A5A5A {
		failf("%s: canary mismatch id=%d want=%d magic=%#x want_magic=%#x aux=%#x: UAF/复用",
			where, p.id, want, p.magic, magicFor(seed, p.id), p.aux)
	}
	runtime.KeepAlive(p)
}

var poisonSeq int64

//go:noinline
func poisonAlloc(seed uint64, n int) []*payload {
	seq := atomic.AddInt64(&poisonSeq, 1)
	out := make([]*payload, n)
	for i := range out {
		out[i] = newPayload(seed, poisonIDBase+seq*1_000_000+int64(i))
	}
	return out
}

// gcOnly 是“无参数、callee 内 GC”边界：局部数组/取址/值拷贝必须跨过它存活。
//
//go:noinline
func gcOnly(seed uint64) {
	runtime.GC()
	poison := poisonAlloc(seed, 64)
	runtime.KeepAlive(poison)
}

func newElem1(seed uint64, base int64, i int) element1 {
	return element1{p: newPayload(seed, base+int64(i))}
}

func newElem2(seed uint64, base int64, i int) element2 {
	return element2{
		p: newPayload(seed, base+int64(i)),
		q: newPayload(seed, base+50+int64(i)),
	}
}

type element1 struct{ p *payload }
type element2 struct {
	p *payload
	q *payload
}

// ---------- shape 矩阵：[N]element1 × N=1/2/4/8 ----------

//go:noinline
func gcCheck1x1(a *[1]element1, seed uint64, base int64, where string) {
	runtime.GC()
	poison := poisonAlloc(seed, 64)
	for i := range a {
		checkPayload(a[i].p, seed, base+int64(i), where)
	}
	runtime.KeepAlive(a)
	runtime.KeepAlive(poison)
}

//go:noinline
func gcCheck2x1(a *[2]element1, seed uint64, base int64, where string) {
	runtime.GC()
	poison := poisonAlloc(seed, 64)
	for i := range a {
		checkPayload(a[i].p, seed, base+int64(i), where)
	}
	runtime.KeepAlive(a)
	runtime.KeepAlive(poison)
}

//go:noinline
func gcCheck4x1(a *[4]element1, seed uint64, base int64, where string) {
	runtime.GC()
	poison := poisonAlloc(seed, 64)
	for i := range a {
		checkPayload(a[i].p, seed, base+int64(i), where)
	}
	runtime.KeepAlive(a)
	runtime.KeepAlive(poison)
}

//go:noinline
func gcCheck8x1(a *[8]element1, seed uint64, base int64, where string) {
	runtime.GC()
	poison := poisonAlloc(seed, 64)
	for i := range a {
		checkPayload(a[i].p, seed, base+int64(i), where)
	}
	runtime.KeepAlive(a)
	runtime.KeepAlive(poison)
}

// ---------- shape 矩阵：[N]element2 × N=1/2/4/8 ----------

//go:noinline
func gcCheck1x2(a *[1]element2, seed uint64, base int64, where string) {
	runtime.GC()
	poison := poisonAlloc(seed, 64)
	for i := range a {
		checkPayload(a[i].p, seed, base+int64(i), where+".p")
		checkPayload(a[i].q, seed, base+50+int64(i), where+".q")
	}
	runtime.KeepAlive(a)
	runtime.KeepAlive(poison)
}

//go:noinline
func gcCheck2x2(a *[2]element2, seed uint64, base int64, where string) {
	runtime.GC()
	poison := poisonAlloc(seed, 64)
	for i := range a {
		checkPayload(a[i].p, seed, base+int64(i), where+".p")
		checkPayload(a[i].q, seed, base+50+int64(i), where+".q")
	}
	runtime.KeepAlive(a)
	runtime.KeepAlive(poison)
}

//go:noinline
func gcCheck4x2(a *[4]element2, seed uint64, base int64, where string) {
	runtime.GC()
	poison := poisonAlloc(seed, 64)
	for i := range a {
		checkPayload(a[i].p, seed, base+int64(i), where+".p")
		checkPayload(a[i].q, seed, base+50+int64(i), where+".q")
	}
	runtime.KeepAlive(a)
	runtime.KeepAlive(poison)
}

//go:noinline
func gcCheck8x2(a *[8]element2, seed uint64, base int64, where string) {
	runtime.GC()
	poison := poisonAlloc(seed, 64)
	for i := range a {
		checkPayload(a[i].p, seed, base+int64(i), where+".p")
		checkPayload(a[i].q, seed, base+50+int64(i), where+".q")
	}
	runtime.KeepAlive(a)
	runtime.KeepAlive(poison)
}

// ---------- 8 个局部数组 shape ----------

//go:noinline
func local1x1(seed uint64, base int64, pick uint64) int64 {
	var arr [1]element1
	for i := range arr {
		arr[i] = newElem1(seed, base, i)
	}
	for i := range arr {
		checkPayload(arr[i].p, seed, base+int64(i), "1x1/pre-gc")
	}
	m := int(pick % uint64(len(arr)))
	q := &arr[m].p
	v := arr[m].p
	wp := weak.Make(arr[m].p)

	gcCheck1x1(&arr, seed, base, "1x1/callee-post-gc")
	checkPayload(*q, seed, base+int64(m), "1x1/q-after-gc")
	checkPayload(v, seed, base+int64(m), "1x1/v-after-gc")
	checkPayload(arr[m].p, seed, base+int64(m), "1x1/arr-after-gc")
	if wp.Value() == nil {
		failf("1x1: strong ref through local array lost after GC (seed=%d base=%d m=%d)", seed, base, m)
	}

	// 取址跨多轮 GC 保留。
	gcOnly(seed)
	checkPayload(arr[m].p, seed, base+int64(m), "1x1/arr-after-gc2")
	checkPayload(*q, seed, base+int64(m), "1x1/q-after-gc2")

	// GC 后改写 arr[m].p，再 GC 一次；q 指向同一字段必须看到新值。
	nid := base + 100 + int64(m)
	arr[m].p = newPayload(seed, nid)
	wp2 := weak.Make(arr[m].p)
	gcOnly(seed)
	checkPayload(arr[m].p, seed, nid, "1x1/rewrite-after-gc")
	checkPayload(*q, seed, nid, "1x1/q-after-rewrite")
	if wp2.Value() == nil {
		failf("1x1: rewritten pointer lost after GC (seed=%d base=%d m=%d)", seed, base, m)
	}
	runtime.KeepAlive(&arr)
	return int64(m)
}

//go:noinline
func local2x1(seed uint64, base int64, pick uint64) int64 {
	var arr [2]element1
	for i := range arr {
		arr[i] = newElem1(seed, base, i)
	}
	for i := range arr {
		checkPayload(arr[i].p, seed, base+int64(i), "2x1/pre-gc")
	}
	m := int(pick % uint64(len(arr)))
	q := &arr[m].p
	v := arr[m].p
	wp := weak.Make(arr[m].p)

	gcCheck2x1(&arr, seed, base, "2x1/callee-post-gc")
	checkPayload(*q, seed, base+int64(m), "2x1/q-after-gc")
	checkPayload(v, seed, base+int64(m), "2x1/v-after-gc")
	checkPayload(arr[m].p, seed, base+int64(m), "2x1/arr-after-gc")
	if wp.Value() == nil {
		failf("2x1: strong ref through local array lost after GC (seed=%d base=%d m=%d)", seed, base, m)
	}

	gcOnly(seed)
	checkPayload(arr[m].p, seed, base+int64(m), "2x1/arr-after-gc2")
	checkPayload(*q, seed, base+int64(m), "2x1/q-after-gc2")

	nid := base + 100 + int64(m)
	arr[m].p = newPayload(seed, nid)
	wp2 := weak.Make(arr[m].p)
	gcOnly(seed)
	checkPayload(arr[m].p, seed, nid, "2x1/rewrite-after-gc")
	checkPayload(*q, seed, nid, "2x1/q-after-rewrite")
	if wp2.Value() == nil {
		failf("2x1: rewritten pointer lost after GC (seed=%d base=%d m=%d)", seed, base, m)
	}
	runtime.KeepAlive(&arr)
	return int64(m)
}

//go:noinline
func local4x1(seed uint64, base int64, pick uint64) int64 {
	var arr [4]element1
	for i := range arr {
		arr[i] = newElem1(seed, base, i)
	}
	for i := range arr {
		checkPayload(arr[i].p, seed, base+int64(i), "4x1/pre-gc")
	}
	m := int(pick % uint64(len(arr)))
	q := &arr[m].p
	v := arr[m].p
	wp := weak.Make(arr[m].p)

	gcCheck4x1(&arr, seed, base, "4x1/callee-post-gc")
	checkPayload(*q, seed, base+int64(m), "4x1/q-after-gc")
	checkPayload(v, seed, base+int64(m), "4x1/v-after-gc")
	checkPayload(arr[m].p, seed, base+int64(m), "4x1/arr-after-gc")
	if wp.Value() == nil {
		failf("4x1: strong ref through local array lost after GC (seed=%d base=%d m=%d)", seed, base, m)
	}

	gcOnly(seed)
	checkPayload(arr[m].p, seed, base+int64(m), "4x1/arr-after-gc2")
	checkPayload(*q, seed, base+int64(m), "4x1/q-after-gc2")

	nid := base + 100 + int64(m)
	arr[m].p = newPayload(seed, nid)
	wp2 := weak.Make(arr[m].p)
	gcOnly(seed)
	checkPayload(arr[m].p, seed, nid, "4x1/rewrite-after-gc")
	checkPayload(*q, seed, nid, "4x1/q-after-rewrite")
	if wp2.Value() == nil {
		failf("4x1: rewritten pointer lost after GC (seed=%d base=%d m=%d)", seed, base, m)
	}
	runtime.KeepAlive(&arr)
	return int64(m)
}

//go:noinline
func local8x1(seed uint64, base int64, pick uint64) int64 {
	var arr [8]element1
	for i := range arr {
		arr[i] = newElem1(seed, base, i)
	}
	for i := range arr {
		checkPayload(arr[i].p, seed, base+int64(i), "8x1/pre-gc")
	}
	m := int(pick % uint64(len(arr)))
	q := &arr[m].p
	v := arr[m].p
	wp := weak.Make(arr[m].p)

	gcCheck8x1(&arr, seed, base, "8x1/callee-post-gc")
	checkPayload(*q, seed, base+int64(m), "8x1/q-after-gc")
	checkPayload(v, seed, base+int64(m), "8x1/v-after-gc")
	checkPayload(arr[m].p, seed, base+int64(m), "8x1/arr-after-gc")
	if wp.Value() == nil {
		failf("8x1: strong ref through local array lost after GC (seed=%d base=%d m=%d)", seed, base, m)
	}

	gcOnly(seed)
	checkPayload(arr[m].p, seed, base+int64(m), "8x1/arr-after-gc2")
	checkPayload(*q, seed, base+int64(m), "8x1/q-after-gc2")

	nid := base + 100 + int64(m)
	arr[m].p = newPayload(seed, nid)
	wp2 := weak.Make(arr[m].p)
	gcOnly(seed)
	checkPayload(arr[m].p, seed, nid, "8x1/rewrite-after-gc")
	checkPayload(*q, seed, nid, "8x1/q-after-rewrite")
	if wp2.Value() == nil {
		failf("8x1: rewritten pointer lost after GC (seed=%d base=%d m=%d)", seed, base, m)
	}
	runtime.KeepAlive(&arr)
	return int64(m)
}

//go:noinline
func local1x2(seed uint64, base int64, pick uint64) int64 {
	var arr [1]element2
	for i := range arr {
		arr[i] = newElem2(seed, base, i)
	}
	for i := range arr {
		checkPayload(arr[i].p, seed, base+int64(i), "1x2/pre-gc")
		checkPayload(arr[i].q, seed, base+50+int64(i), "1x2/pre-gc.q")
	}
	m := int(pick % uint64(len(arr)))
	q := &arr[m].p
	v := arr[m].p
	wp := weak.Make(arr[m].p)

	gcCheck1x2(&arr, seed, base, "1x2/callee-post-gc")
	checkPayload(*q, seed, base+int64(m), "1x2/q-after-gc")
	checkPayload(v, seed, base+int64(m), "1x2/v-after-gc")
	checkPayload(arr[m].p, seed, base+int64(m), "1x2/arr-after-gc")
	checkPayload(arr[m].q, seed, base+50+int64(m), "1x2/arr.q-after-gc")
	if wp.Value() == nil {
		failf("1x2: strong ref through local array lost after GC (seed=%d base=%d m=%d)", seed, base, m)
	}

	gcOnly(seed)
	checkPayload(arr[m].p, seed, base+int64(m), "1x2/arr-after-gc2")
	checkPayload(arr[m].q, seed, base+50+int64(m), "1x2/arr.q-after-gc2")
	checkPayload(*q, seed, base+int64(m), "1x2/q-after-gc2")

	nid := base + 100 + int64(m)
	arr[m].p = newPayload(seed, nid)
	wp2 := weak.Make(arr[m].p)
	gcOnly(seed)
	checkPayload(arr[m].p, seed, nid, "1x2/rewrite-after-gc")
	checkPayload(*q, seed, nid, "1x2/q-after-rewrite")
	checkPayload(arr[m].q, seed, base+50+int64(m), "1x2/arr.q-after-rewrite")
	if wp2.Value() == nil {
		failf("1x2: rewritten pointer lost after GC (seed=%d base=%d m=%d)", seed, base, m)
	}
	runtime.KeepAlive(&arr)
	return int64(m)
}

//go:noinline
func local2x2(seed uint64, base int64, pick uint64) int64 {
	var arr [2]element2
	for i := range arr {
		arr[i] = newElem2(seed, base, i)
	}
	for i := range arr {
		checkPayload(arr[i].p, seed, base+int64(i), "2x2/pre-gc")
		checkPayload(arr[i].q, seed, base+50+int64(i), "2x2/pre-gc.q")
	}
	m := int(pick % uint64(len(arr)))
	q := &arr[m].p
	v := arr[m].p
	wp := weak.Make(arr[m].p)

	gcCheck2x2(&arr, seed, base, "2x2/callee-post-gc")
	checkPayload(*q, seed, base+int64(m), "2x2/q-after-gc")
	checkPayload(v, seed, base+int64(m), "2x2/v-after-gc")
	checkPayload(arr[m].p, seed, base+int64(m), "2x2/arr-after-gc")
	checkPayload(arr[m].q, seed, base+50+int64(m), "2x2/arr.q-after-gc")
	if wp.Value() == nil {
		failf("2x2: strong ref through local array lost after GC (seed=%d base=%d m=%d)", seed, base, m)
	}

	gcOnly(seed)
	checkPayload(arr[m].p, seed, base+int64(m), "2x2/arr-after-gc2")
	checkPayload(arr[m].q, seed, base+50+int64(m), "2x2/arr.q-after-gc2")
	checkPayload(*q, seed, base+int64(m), "2x2/q-after-gc2")

	nid := base + 100 + int64(m)
	arr[m].p = newPayload(seed, nid)
	wp2 := weak.Make(arr[m].p)
	gcOnly(seed)
	checkPayload(arr[m].p, seed, nid, "2x2/rewrite-after-gc")
	checkPayload(*q, seed, nid, "2x2/q-after-rewrite")
	checkPayload(arr[m].q, seed, base+50+int64(m), "2x2/arr.q-after-rewrite")
	if wp2.Value() == nil {
		failf("2x2: rewritten pointer lost after GC (seed=%d base=%d m=%d)", seed, base, m)
	}
	runtime.KeepAlive(&arr)
	return int64(m)
}

//go:noinline
func local4x2(seed uint64, base int64, pick uint64) int64 {
	var arr [4]element2
	for i := range arr {
		arr[i] = newElem2(seed, base, i)
	}
	for i := range arr {
		checkPayload(arr[i].p, seed, base+int64(i), "4x2/pre-gc")
		checkPayload(arr[i].q, seed, base+50+int64(i), "4x2/pre-gc.q")
	}
	m := int(pick % uint64(len(arr)))
	q := &arr[m].p
	v := arr[m].p
	wp := weak.Make(arr[m].p)

	gcCheck4x2(&arr, seed, base, "4x2/callee-post-gc")
	checkPayload(*q, seed, base+int64(m), "4x2/q-after-gc")
	checkPayload(v, seed, base+int64(m), "4x2/v-after-gc")
	checkPayload(arr[m].p, seed, base+int64(m), "4x2/arr-after-gc")
	checkPayload(arr[m].q, seed, base+50+int64(m), "4x2/arr.q-after-gc")
	if wp.Value() == nil {
		failf("4x2: strong ref through local array lost after GC (seed=%d base=%d m=%d)", seed, base, m)
	}

	gcOnly(seed)
	checkPayload(arr[m].p, seed, base+int64(m), "4x2/arr-after-gc2")
	checkPayload(arr[m].q, seed, base+50+int64(m), "4x2/arr.q-after-gc2")
	checkPayload(*q, seed, base+int64(m), "4x2/q-after-gc2")

	nid := base + 100 + int64(m)
	arr[m].p = newPayload(seed, nid)
	wp2 := weak.Make(arr[m].p)
	gcOnly(seed)
	checkPayload(arr[m].p, seed, nid, "4x2/rewrite-after-gc")
	checkPayload(*q, seed, nid, "4x2/q-after-rewrite")
	checkPayload(arr[m].q, seed, base+50+int64(m), "4x2/arr.q-after-rewrite")
	if wp2.Value() == nil {
		failf("4x2: rewritten pointer lost after GC (seed=%d base=%d m=%d)", seed, base, m)
	}
	runtime.KeepAlive(&arr)
	return int64(m)
}

//go:noinline
func local8x2(seed uint64, base int64, pick uint64) int64 {
	var arr [8]element2
	for i := range arr {
		arr[i] = newElem2(seed, base, i)
	}
	for i := range arr {
		checkPayload(arr[i].p, seed, base+int64(i), "8x2/pre-gc")
		checkPayload(arr[i].q, seed, base+50+int64(i), "8x2/pre-gc.q")
	}
	m := int(pick % uint64(len(arr)))
	q := &arr[m].p
	v := arr[m].p
	wp := weak.Make(arr[m].p)

	gcCheck8x2(&arr, seed, base, "8x2/callee-post-gc")
	checkPayload(*q, seed, base+int64(m), "8x2/q-after-gc")
	checkPayload(v, seed, base+int64(m), "8x2/v-after-gc")
	checkPayload(arr[m].p, seed, base+int64(m), "8x2/arr-after-gc")
	checkPayload(arr[m].q, seed, base+50+int64(m), "8x2/arr.q-after-gc")
	if wp.Value() == nil {
		failf("8x2: strong ref through local array lost after GC (seed=%d base=%d m=%d)", seed, base, m)
	}

	gcOnly(seed)
	checkPayload(arr[m].p, seed, base+int64(m), "8x2/arr-after-gc2")
	checkPayload(arr[m].q, seed, base+50+int64(m), "8x2/arr.q-after-gc2")
	checkPayload(*q, seed, base+int64(m), "8x2/q-after-gc2")

	nid := base + 100 + int64(m)
	arr[m].p = newPayload(seed, nid)
	wp2 := weak.Make(arr[m].p)
	gcOnly(seed)
	checkPayload(arr[m].p, seed, nid, "8x2/rewrite-after-gc")
	checkPayload(*q, seed, nid, "8x2/q-after-rewrite")
	checkPayload(arr[m].q, seed, base+50+int64(m), "8x2/arr.q-after-rewrite")
	if wp2.Value() == nil {
		failf("8x2: rewritten pointer lost after GC (seed=%d base=%d m=%d)", seed, base, m)
	}
	runtime.KeepAlive(&arr)
	return int64(m)
}

// ---------- worker ----------

type worker struct {
	id    int64
	seed  uint64
	base0 int64

	held *[8]element2 // 跨轮保留取址的数组

	next   int64
	rounds int64
	checks int64
	gcN    int64

	lastWeak   []weak.Pointer[payload]
	lastWeakID []int64
}

func (w *worker) addWeak(wps *[]weak.Pointer[payload], ids *[]int64, p *payload, id int64) {
	*wps = append(*wps, weak.Make(p))
	*ids = append(*ids, id)
}

//go:noinline
func (w *worker) round(r int64) {
	base := w.base0 + r*10000
	pick := w.seed ^ (uint64(r) * 0x9E3779B97F4A7C15) ^ 0x5DEECE66D

	var wps []weak.Pointer[payload]
	var wids []int64

	check := func(shape string, got int64, want int64) {
		if got != want {
			failf("phase=index shape=%s worker=%d round=%d: m=%d want=%d (非常量下标被错误折叠)",
				shape, w.id, r, got, want)
		}
		w.checks++
	}

	m := local1x1(w.seed, base+0, pick+1)
	check("1x1", m, int64((pick+1)%1))
	m = local2x1(w.seed, base+1000, pick+2)
	check("2x1", m, int64((pick+2)%2))
	m = local4x1(w.seed, base+2000, pick+3)
	check("4x1", m, int64((pick+3)%4))
	m = local8x1(w.seed, base+3000, pick+4)
	check("8x1", m, int64((pick+4)%8))
	m = local1x2(w.seed, base+4000, pick+5)
	check("1x2", m, int64((pick+5)%1))
	m = local2x2(w.seed, base+5000, pick+6)
	check("2x2", m, int64((pick+6)%2))
	m = local4x2(w.seed, base+6000, pick+7)
	check("4x2", m, int64((pick+7)%4))
	m = local8x2(w.seed, base+7000, pick+8)
	check("8x2", m, int64((pick+8)%8))
	w.gcN += 24 // 8 shapes × 3 次显式 GC

	// 跨轮保留取址：held 数组在 worker 生命周期内持有，每轮重写 + 两轮 GC。
	hb := base + 8000
	hp := w.held
	for i := 0; i < len(hp); i++ {
		hp[i] = newElem2(w.seed, hb, i)
	}
	hm := int(pick % uint64(len(hp)))
	hq := &hp[hm].p
	wp := weak.Make(hp[hm].p)
	gcOnly(w.seed)
	checkPayload(hp[hm].p, w.seed, hb+int64(hm), "held/arr-after-gc")
	checkPayload(*hq, w.seed, hb+int64(hm), "held/q-after-gc")
	checkPayload(hp[hm].q, w.seed, hb+50+int64(hm), "held/arr.q-after-gc")
	if wp.Value() == nil {
		failf("held: strong ref through retained array lost after GC (worker=%d round=%d)", w.id, r)
	}
	hnid := hb + 100 + int64(hm)
	hp[hm].p = newPayload(w.seed, hnid)
	gcOnly(w.seed)
	checkPayload(hp[hm].p, w.seed, hnid, "held/rewrite-after-gc")
	checkPayload(*hq, w.seed, hnid, "held/q-after-rewrite")
	checkPayload(hp[hm].q, w.seed, hb+50+int64(hm), "held/arr.q-after-rewrite")
	check("held", int64(hm), int64(pick%uint64(len(hp))))
	w.gcN += 2
	w.addWeak(&wps, &wids, hp[hm].p, hnid)

	// 强引用断言：held 数组仍被 worker 强引用，再 GC 一次后 weak 必须非 nil。
	runtime.GC()
	w.gcN++
	for i, wp := range wps {
		v := wp.Value()
		if v == nil {
			failf("phase=weak worker=%d round=%d slot=%d id=%d seed=%d: strong ref held but object collected (局部数组漏标/UAF)",
				w.id, r, i, wids[i], w.seed)
		}
		checkPayload(v, w.seed, wids[i], "weak-strong-ref")
	}
	atomic.AddInt64(&gWeakChecks, int64(len(wps)))
	runtime.KeepAlive(hp)

	w.lastWeak = wps
	w.lastWeakID = wids
	w.rounds++
	w.next = r + 1
	atomic.AddInt64(&gBeats, 1)
}

// ---------- 通用 harness ----------

var (
	gBeats      int64
	gWeakChecks int64
	gStarted    int64
	gFinished   int64
	gSeed       uint64
)

func failf(format string, args ...interface{}) {
	fmt.Printf("FAIL: "+format+"\n", args...)
	buf := make([]byte, 1<<20)
	n := runtime.Stack(buf, true)
	fmt.Fprintf(os.Stderr, "%s", buf[:n])
	os.Exit(1)
}

func envInt(key string, def, min, max int) int {
	s := os.Getenv(key)
	if s == "" {
		return def
	}
	v, err := strconv.Atoi(s)
	if err != nil || v < min {
		return def
	}
	if v > max {
		return max
	}
	return v
}

func envSeed(def uint64) uint64 {
	s := os.Getenv("UAF_SEED")
	if s == "" {
		return def
	}
	v, err := strconv.ParseUint(s, 10, 64)
	if err != nil || v == 0 {
		return def
	}
	return v
}

func setupCrashOutput() {
	p := os.Getenv("STRESS_CRASH_FILE")
	if p == "" {
		return
	}
	f, err := os.OpenFile(p, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return
	}
	_ = debug.SetCrashOutput(f, debug.CrashOptions{})
}

func watchdog(interval time.Duration, stop <-chan struct{}, wg *sync.WaitGroup) {
	defer wg.Done()
	last := atomic.LoadInt64(&gBeats)
	lastChange := time.Now()
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-stop:
			return
		case <-t.C:
			cur := atomic.LoadInt64(&gBeats)
			if cur != last {
				last = cur
				lastChange = time.Now()
				continue
			}
			if time.Since(lastChange) > 6*interval {
				failf("phase=watchdog seed=%d: no worker progress for %s (beats=%d)", gSeed, time.Since(lastChange), cur)
			}
		}
	}
}

func waitGoroutines(base int, grace time.Duration) int {
	giveUp := time.Now().Add(grace)
	for {
		n := runtime.NumGoroutine()
		if n <= base || time.Now().After(giveUp) {
			return n
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func (w *worker) run(seconds int, deadline time.Time, fixed int64) {
	atomic.AddInt64(&gStarted, 1)
	defer atomic.AddInt64(&gFinished, 1)
	w.held = &[8]element2{}
	r := int64(0)
	for {
		if seconds > 0 {
			if time.Now().After(deadline) {
				break
			}
		} else if r >= fixed {
			break
		}
		w.round(r)
		r++
	}
}

func main() {
	setupCrashOutput()
	seconds := envInt("STRESS_SECONDS", 60, 0, 86400)
	workers := envInt("STRESS_WORKERS", 4, 1, 16)
	hb := envInt("STRESS_HEARTBEAT", 5, 1, 60)
	seed := envSeed(1)
	fixed := int64(envInt("STRESS_FIXED_ROUNDS", fixedRoundsDefault, 1, 1000000))
	gSeed = seed

	fmt.Printf("scenario=%s phase=preflight go=%s goos=%s goarch=%s gomaxprocs=%d seed=%d seconds=%d workers=%d fixed_rounds=%d heartbeat=%ds shapes=8 array_sizes=1,2,4,8 elem_ptrs=1,2 held=8x element2\n",
		scenario, runtime.Version(), runtime.GOOS, runtime.GOARCH, runtime.GOMAXPROCS(0), seed, seconds, workers, fixed, hb)

	runtime.GC()
	baseGoroutines := runtime.NumGoroutine()

	deadline := time.Now().Add(time.Duration(seconds) * time.Second)
	ws := make([]*worker, workers)
	for i := 0; i < workers; i++ {
		ws[i] = &worker{
			id:    int64(i),
			seed:  seed*1_000_003 + uint64(i+1)*7919,
			base0: int64(i) * 1_000_000_000_000,
		}
	}

	stop := make(chan struct{})
	var aux sync.WaitGroup
	aux.Add(1)
	go watchdog(time.Duration(hb)*time.Second, stop, &aux)

	var wg sync.WaitGroup
	for _, w := range ws {
		wg.Add(1)
		go func(w *worker) {
			defer wg.Done()
			w.run(seconds, deadline, fixed)
		}(w)
	}
	wg.Wait()
	close(stop)
	aux.Wait()

	// quiesce：先丢掉跨轮保留数组的强引用，再全量 GC（lastWeak 失活仅记录）。
	for _, w := range ws {
		w.held = nil
	}
	runtime.GC()
	runtime.GC()
	aliveAfterDrop := 0
	for _, w := range ws {
		for _, wp := range w.lastWeak {
			if wp.Value() != nil {
				aliveAfterDrop++
			}
		}
	}

	if s, f := atomic.LoadInt64(&gStarted), atomic.LoadInt64(&gFinished); s != int64(workers) || f != s {
		failf("phase=final seed=%d: started=%d finished=%d workers=%d", seed, s, f, workers)
	}
	var roundsTotal, checksTotal, gcTotal int64
	for _, w := range ws {
		roundsTotal += w.rounds
		checksTotal += w.checks
		gcTotal += w.gcN
		if seconds == 0 && w.next != fixed {
			failf("phase=final seed=%d worker=%d: rounds=%d want=%d", seed, w.id, w.next, fixed)
		}
	}
	if roundsTotal == 0 {
		failf("phase=final seed=%d: no rounds completed", seed)
	}

	ng := waitGoroutines(baseGoroutines, 5*time.Second)
	if ng > baseGoroutines+4 {
		failf("phase=final seed=%d: goroutines baseline=%d after=%d: goroutine leak", seed, baseGoroutines, ng)
	}

	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	fmt.Printf("metrics: rounds=%d shape_ops=%d canary_checks=%d worker_gc=%d weak_checks=%d weak_alive_after_drop=%d num_gc=%d heap_alloc=%d heap_objects=%d goroutines_start=%d goroutines_final=%d\n",
		roundsTotal, roundsTotal*9, checksTotal, gcTotal, atomic.LoadInt64(&gWeakChecks),
		aliveAfterDrop, m.NumGC, m.HeapAlloc, m.HeapObjects, baseGoroutines, ng)
	fmt.Printf("STRESS PASS %s\n", scenario)
}
