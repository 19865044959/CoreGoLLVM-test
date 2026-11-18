// L17: 返回含指针字段的 struct 值。大聚合返回槽跨 GC 后，指针/切片/字符串/map/interface 字段必须完好。
package main

import (
	"fmt"
	"os"
	"runtime"
	"strconv"
	"sync/atomic"
)

type item struct {
	v    int64
	name string
	next *item
}

type record struct {
	id    int64
	head  *item
	list  []*item
	arr   [3]*item
	label string
	meta  map[string]*item
	extra any
	blob  []byte
	nest  struct{ a, b *item }
}

type builder struct {
	prefix int64
	keep   *item
}

type maker interface {
	make(i int64) record
}

var (
	failed  int32
	uafSeed int64 = 1
)

func ck(ok bool, msg string) {
	if !ok {
		atomic.AddInt32(&failed, 1)
		fmt.Println("FAIL:", msg)
	}
}

func gcStress() {
	var sink [][]byte
	for i := 0; i < 3; i++ {
		b := make([]byte, 1024)
		b[0] = byte(i)
		sink = append(sink, b)
	}
	runtime.GC()
	runtime.GC()
	runtime.KeepAlive(sink)
}

func seedFromEnv() int64 {
	if s := os.Getenv("UAF_SEED"); s != "" {
		if v, err := strconv.ParseInt(s, 10, 64); err == nil && v != 0 {
			return v
		}
	}
	return 1
}

func finish(name string) {
	if n := atomic.LoadInt32(&failed); n != 0 {
		fmt.Printf("FAILED %s (%d failed checks) seed=%d\n", name, n, uafSeed)
		os.Exit(1)
	}
	fmt.Println("ALL PASS " + name)
}

//go:noinline
func build(i int64) record {
	r := record{
		id:    i,
		head:  &item{v: i + 1, name: "head"},
		list:  []*item{{v: i + 2, name: "l0"}, {v: i + 3, name: "l1"}},
		arr:   [3]*item{{v: i + 4, name: "a0"}, {v: i + 5, name: "a1"}, {v: i + 6, name: "a2"}},
		label: "record",
		meta:  map[string]*item{"x": {v: i + 7, name: "mx"}, "y": {v: i + 8, name: "my"}},
		extra: &item{v: i + 9, name: "extra"},
		blob:  []byte{byte(i), byte(i + 1), byte(i + 2)},
	}
	r.nest.a = &item{v: i + 10, name: "na"}
	r.nest.b = &item{v: i + 11, name: "nb"}
	r.head.next = &item{v: i + 12, name: "tail"}
	gcStress()
	r.list = append(r.list, &item{v: i + 13, name: "l2"})
	gcStress()
	return r
}

func verifyBuilt(r record, i int64, where string) {
	bad := r.id != i ||
		r.head == nil || r.head.v != i+1 || r.head.name != "head" ||
		r.head.next == nil || r.head.next.v != i+12 || r.head.next.name != "tail" ||
		len(r.list) != 3 ||
		r.list[0].v != i+2 || r.list[1].v != i+3 || r.list[2].v != i+13 ||
		r.arr[0] == nil || r.arr[0].v != i+4 ||
		r.arr[1] == nil || r.arr[1].v != i+5 ||
		r.arr[2] == nil || r.arr[2].v != i+6 ||
		r.label != "record" ||
		r.meta["x"] == nil || r.meta["x"].v != i+7 ||
		r.meta["y"] == nil || r.meta["y"].v != i+8 ||
		r.nest.a == nil || r.nest.a.v != i+10 ||
		r.nest.b == nil || r.nest.b.v != i+11 ||
		len(r.blob) != 3 || r.blob[0] != byte(i) || r.blob[2] != byte(i+2)
	if !bad {
		x, ok := r.extra.(*item)
		bad = !ok || x == nil || x.v != i+9 || x.name != "extra"
	}
	ck(!bad, where+": record corrupted after GC")
}

//go:noinline
func mutate(r record, delta int64) record {
	gcStress()
	wantID := r.id + delta
	r.id = wantID
	r.head = &item{v: wantID + 20, name: "mhead"}
	r.arr[2] = &item{v: wantID + 22, name: "ma2"}
	r.extra = &item{v: wantID + 23, name: "mextra"}
	r.meta = map[string]*item{"x": {v: wantID + 21, name: "mx2"}, "y": r.meta["y"]}
	gcStress()
	return r
}

func verifyMutated(r record, base int64, delta int64, where string) {
	wantID := base + delta
	bad := r.id != wantID ||
		r.head == nil || r.head.v != wantID+20 || r.head.name != "mhead" || r.head.next != nil ||
		len(r.list) != 3 || r.list[0].v != base+2 || r.list[2].v != base+13 ||
		r.arr[2] == nil || r.arr[2].v != wantID+22 ||
		r.meta["x"] == nil || r.meta["x"].v != wantID+21 ||
		r.meta["y"] == nil || r.meta["y"].v != base+8 ||
		r.nest.a == nil || r.nest.a.v != base+10 ||
		len(r.blob) != 3 || r.blob[1] != byte(base+1)
	if !bad {
		x, ok := r.extra.(*item)
		bad = !ok || x == nil || x.v != wantID+23 || x.name != "mextra"
	}
	ck(!bad, where+": mutated record corrupted after GC")
}

//go:noinline
func (b *builder) make(i int64) record {
	r := build(i)
	gcStress()
	r.id += b.prefix
	r.head = &item{v: r.id, name: "bhead", next: &item{v: b.keep.v, name: "bkeep"}}
	gcStress()
	return r
}

func main() {
	uafSeed = seedFromEnv()
	const rounds = 8

	for rnd := 0; rnd < rounds; rnd++ {
		id := int64(rnd*1000) + uafSeed%13
		delta := int64(17 + rnd)

		r := build(id)
		gcStress()
		verifyBuilt(r, id, fmt.Sprintf("round %d build", rnd))

		m := mutate(r, delta)
		gcStress()
		verifyMutated(m, id, delta, fmt.Sprintf("round %d mutate", rnd))
		verifyBuilt(r, id, fmt.Sprintf("round %d original", rnd))

		b := &builder{prefix: int64(100 + rnd), keep: &item{v: id + 777, name: "keep"}}
		var mk maker = b
		got := mk.make(id)
		gcStress()
		ck(got.id == id+b.prefix, fmt.Sprintf("round %d method id got=%d want=%d seed=%d", rnd, got.id, id+b.prefix, uafSeed))
		ck(got.head != nil && got.head.v == got.id && got.head.name == "bhead", fmt.Sprintf("round %d method head seed=%d", rnd, uafSeed))
		ck(got.head.next != nil && got.head.next.v == id+777 && got.head.next.name == "bkeep", fmt.Sprintf("round %d method keep pointer seed=%d", rnd, uafSeed))
		ck(len(got.list) == 3 && got.list[2].v == id+13, fmt.Sprintf("round %d method list seed=%d", rnd, uafSeed))
		ck(b.keep != nil && b.keep.v == id+777, fmt.Sprintf("round %d builder keep after GC seed=%d", rnd, uafSeed))
	}

	finish("l17_struct_return_fields")
}
