// L17: 大量指针参数强制 spill。14 个指针 + slice/map/func 参数超出寄存器，GC 必须扫描栈上溢出槽。
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

type spiller struct {
	tag  int64
	keep *item
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

func envRounds(def int) int {
	n := def
	if s := os.Getenv("TORTURE_ROUNDS"); s != "" {
		if v, err := strconv.Atoi(s); err == nil && v > 0 {
			n *= v
			if n > 2000 {
				n = 2000
			}
		}
	}
	return n
}

func finish(name string) {
	if n := atomic.LoadInt32(&failed); n != 0 {
		fmt.Printf("FAILED %s (%d failed checks) seed=%d\n", name, n, uafSeed)
		os.Exit(1)
	}
	fmt.Println("ALL PASS " + name)
}

func val(p *item, idx int, where string) int64 {
	if p == nil {
		ck(false, fmt.Sprintf("%s: arg%d nil seed=%d", where, idx, uafSeed))
		return 0
	}
	if p.name != "it" {
		ck(false, fmt.Sprintf("%s: arg%d name corrupted seed=%d", where, idx, uafSeed))
	}
	return p.v
}

func fold14(a0, a1, a2, a3, a4, a5, a6, a7, a8, a9, a10, a11, a12, a13 *item,
	label string, list []*item, meta map[string]*item, pick func() *item, where string) int64 {
	sum := val(a0, 0, where) + val(a1, 1, where) + val(a2, 2, where) + val(a3, 3, where) +
		val(a4, 4, where) + val(a5, 5, where) + val(a6, 6, where) + val(a7, 7, where) +
		val(a8, 8, where) + val(a9, 9, where) + val(a10, 10, where) + val(a11, 11, where) +
		val(a12, 12, where) + val(a13, 13, where) + int64(len(label))
	for i, it := range list {
		if it == nil {
			ck(false, fmt.Sprintf("%s: list[%d] nil seed=%d", where, i, uafSeed))
			continue
		}
		sum += it.v
	}
	for _, k := range []string{"x", "y"} {
		if meta[k] == nil {
			ck(false, fmt.Sprintf("%s: meta[%s] nil seed=%d", where, k, uafSeed))
			continue
		}
		sum += meta[k].v
	}
	if p := pick(); p != nil {
		sum += p.v
	} else {
		ck(false, where+": pick returned nil")
	}
	return sum
}

//go:noinline
func mix14(a0, a1, a2, a3, a4, a5, a6, a7, a8, a9, a10, a11, a12, a13 *item,
	label string, list []*item, meta map[string]*item, pick func() *item) int64 {
	gcStress()
	a := fold14(a0, a1, a2, a3, a4, a5, a6, a7, a8, a9, a10, a11, a12, a13, label, list, meta, pick, "mix14")
	gcStress()
	b := fold14(a0, a1, a2, a3, a4, a5, a6, a7, a8, a9, a10, a11, a12, a13, label, list, meta, pick, "mix14-after")
	if a != b {
		ck(false, fmt.Sprintf("mix14 checksum changed across GC: %d != %d seed=%d", a, b, uafSeed))
	}
	gcStress()
	return b
}

//go:noinline
func (s *spiller) blend(a0, a1, a2, a3, a4, a5, a6, a7, a8, a9 *item, label string,
	list []*item, pick func() *item) int64 {
	gcStress()
	sum := int64(len(label)) + s.tag
	for i, p := range []*item{a0, a1, a2, a3, a4, a5, a6, a7, a8, a9} {
		if p == nil {
			ck(false, fmt.Sprintf("blend: arg%d nil seed=%d", i, uafSeed))
			continue
		}
		sum += p.v
	}
	for _, it := range list {
		sum += it.v
	}
	sum += pick().v
	if s.keep == nil || s.keep.name != "keep" {
		ck(false, fmt.Sprintf("blend: receiver keep corrupted seed=%d", uafSeed))
	} else {
		sum += s.keep.v
	}
	gcStress()
	return sum
}

func main() {
	uafSeed = seedFromEnv()
	rounds := envRounds(240)

	for rnd := 0; rnd < rounds; rnd++ {
		base := int64(rnd*1000) + uafSeed%23
		items := make([]*item, 14)
		want := int64(0)
		for i := range items {
			items[i] = &item{v: base + int64(i+1), name: "it", next: &item{v: base + 100 + int64(i), name: "n"}}
			want += items[i].v
		}
		list := []*item{{v: base + 201, name: "it"}, {v: base + 202, name: "it"}, {v: base + 203, name: "it"}}
		for _, it := range list {
			want += it.v
		}
		meta := map[string]*item{"x": {v: base + 211, name: "it"}, "y": {v: base + 212, name: "it"}}
		want += meta["x"].v + meta["y"].v
		capItem := &item{v: base + 221, name: "it"}
		pick := func() *item { return capItem }
		want += capItem.v
		label := "spill"
		want += int64(len(label))

		got := mix14(items[0], items[1], items[2], items[3], items[4], items[5], items[6], items[7],
			items[8], items[9], items[10], items[11], items[12], items[13],
			label, list, meta, pick)
		ck(got == want, fmt.Sprintf("round %d mix14=%d want=%d seed=%d", rnd, got, want, uafSeed))

		gcStress()
		for i, it := range items {
			if it == nil || it.v != base+int64(i+1) || it.name != "it" ||
				it.next == nil || it.next.v != base+100+int64(i) {
				ck(false, fmt.Sprintf("round %d item[%d] corrupted seed=%d", rnd, i, uafSeed))
				break
			}
		}
		ck(list[2] != nil && list[2].v == base+203, fmt.Sprintf("round %d list corrupted seed=%d", rnd, uafSeed))
		ck(meta["y"] != nil && meta["y"].v == base+212, fmt.Sprintf("round %d meta corrupted seed=%d", rnd, uafSeed))
		ck(capItem.v == base+221, fmt.Sprintf("round %d closure capture corrupted seed=%d", rnd, uafSeed))

		s := &spiller{tag: int64(rnd), keep: &item{v: base + 301, name: "keep"}}
		wantM := int64(len(label)) + s.tag + s.keep.v
		for i := 0; i < 10; i++ {
			wantM += items[i].v
		}
		for _, it := range list {
			wantM += it.v
		}
		wantM += capItem.v
		gotM := s.blend(items[0], items[1], items[2], items[3], items[4], items[5], items[6], items[7], items[8], items[9],
			label, list, pick)
		ck(gotM == wantM, fmt.Sprintf("round %d blend=%d want=%d seed=%d", rnd, gotM, wantM, uafSeed))

		gcStress()
		if s.keep == nil || s.keep.v != base+301 {
			ck(false, fmt.Sprintf("round %d spiller keep corrupted seed=%d", rnd, uafSeed))
		}
	}

	finish("l17_spill_many_ptr_args")
}
