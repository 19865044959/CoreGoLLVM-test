// L17: 嵌套 FCA 聚合同时作为多个 defer 实参。两个 bundle + leaf 数组按值复制进
// defer 记录，注册后重绑局部嵌套指针/切片/any；defer 内 GC 后仍须看到原副本。
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
	tag  string
	next *item
}

type pair struct {
	p *item
	s string
}

type leaf struct {
	p *item
	s string
	n int64
}

type mat2 struct {
	rows [2][2]leaf
	anyv any
}

type bundle struct {
	m    mat2
	ptrs [3]*item
	list []*item
	tag  string
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

//go:noinline
func gcStress() {
	var sink [][]byte
	for i := 0; i < 8; i++ {
		b := make([]byte, 2048)
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

func mkItem(base int64) *item {
	return &item{v: base, tag: "fca", next: &item{v: base + 6, tag: "fca-next"}}
}

func itemVal(x *item, where string) int64 {
	if x == nil {
		ck(false, fmt.Sprintf("%s: nil item seed=%d", where, uafSeed))
		return 0
	}
	if x.tag != "fca" || x.next == nil || x.next.tag != "fca-next" || x.next.v != x.v+6 {
		ck(false, fmt.Sprintf("%s: item chain corrupted seed=%d", where, uafSeed))
	}
	return x.v + x.next.v
}

func leafSum(l leaf, where string) int64 {
	return itemVal(l.p, where+".p") + int64(len(l.s)) + l.n
}

func anySum(a any, where string) int64 {
	switch x := a.(type) {
	case nil:
		return 0
	case *item:
		return itemVal(x, where)
	case pair:
		return itemVal(x.p, where+".p") + int64(len(x.s))
	case string:
		return int64(len(x))
	default:
		ck(false, fmt.Sprintf("%s: unexpected any type %T seed=%d", where, x, uafSeed))
		return 0
	}
}

func bundleSum(b bundle, where string) int64 {
	var sum int64
	for i := 0; i < 2; i++ {
		for j := 0; j < 2; j++ {
			sum += leafSum(b.m.rows[i][j], fmt.Sprintf("%s.rows[%d][%d]", where, i, j))
		}
	}
	sum += anySum(b.m.anyv, where+".anyv")
	for i, p := range b.ptrs {
		sum += itemVal(p, fmt.Sprintf("%s.ptrs[%d]", where, i))
	}
	for i, p := range b.list {
		sum += itemVal(p, fmt.Sprintf("%s.list[%d]", where, i))
	}
	return sum + int64(len(b.tag))
}

func leafArraySum(a [2]leaf, where string) int64 {
	return leafSum(a[0], where+"[0]") + leafSum(a[1], where+"[1]")
}

func makeBundle(base int64, mode int) bundle {
	b := bundle{
		ptrs: [3]*item{mkItem(base + 6), mkItem(base + 7), mkItem(base + 8)},
		tag:  "bundle",
	}
	b.m.anyv = mkItem(base + 5)
	for i := 0; i < 2; i++ {
		for j := 0; j < 2; j++ {
			b.m.rows[i][j] = leaf{p: mkItem(base + int64(1+i*2+j)), s: "leaf", n: base + int64(i*10+j)}
		}
	}
	if mode%2 == 0 {
		b.m.anyv = pair{p: mkItem(base + 9), s: "bany"}
		b.list = []*item{mkItem(base + 10), mkItem(base + 11)}
	} else {
		b.m.anyv = "bany-mode"
		b.list = nil
	}
	return b
}

func takeTwo(x, y bundle, arr [2]leaf, out *int64) {
	gcStress()
	sum := bundleSum(x, "take-x") + bundleSum(y, "take-y") + leafArraySum(arr, "take-arr")
	*out = sum
}

func run(base int64, mode int) (out bundle, sums [2]int64, s1, s2, sa, mut int64) {
	b1 := makeBundle(base, mode)
	b2 := makeBundle(base+100, mode^1)
	arr := [2]leaf{
		{p: mkItem(base + 20), s: "arr0", n: base},
		{p: mkItem(base + 21), s: "arr1", n: base + 1},
	}
	s1 = bundleSum(b1, "orig-b1")
	s2 = bundleSum(b2, "orig-b2")
	sa = leafArraySum(arr, "orig-arr")

	defer takeTwo(b1, b2, arr, &sums[0])
	defer func(x, y bundle, a [2]leaf) {
		gcStress()
		sums[1] = bundleSum(x, "closure-x") + bundleSum(y, "closure-y") + leafArraySum(a, "closure-arr")
	}(b1, b2, arr)

	// 注册后重绑局部嵌套字段/切片/any：defer 中的值拷贝必须保持原样。
	b1.m.rows[0][0].p = mkItem(base + 900)
	b1.m.rows[1][1].s = "mut-leaf"
	b1.m.anyv = "mutated-any"
	b1.ptrs[0] = mkItem(base + 901)
	b1.list = append(b1.list, mkItem(base+902))
	b1.tag = "mut-bundle"
	arr[0] = leaf{p: mkItem(base + 903), s: "mut-arr", n: 77}
	gcStress()
	mut = bundleSum(b1, "mutated-b1")
	if mut == s1 {
		ck(false, fmt.Sprintf("mutation did not change bundle checksum seed=%d", uafSeed))
	}
	return b1, sums, s1, s2, sa, mut
}

func main() {
	uafSeed = seedFromEnv()
	for r := 0; r < 6; r++ {
		base := int64(r*1000) + uafSeed%47
		mode := (r + int(uafSeed)) % 2
		out, sums, s1, s2, sa, mut := run(base, mode)
		gcStress()

		wantDefer := s1 + s2 + sa
		if sums[0] != wantDefer {
			ck(false, fmt.Sprintf("round %d: direct FCA defer sum=%d want=%d seed=%d", r, sums[0], wantDefer, uafSeed))
		}
		if sums[1] != wantDefer {
			ck(false, fmt.Sprintf("round %d: closure FCA defer sum=%d want=%d seed=%d", r, sums[1], wantDefer, uafSeed))
		}
		if got := bundleSum(out, "main-out"); got != mut {
			ck(false, fmt.Sprintf("round %d: returned bundle sum=%d want=%d seed=%d", r, got, mut, uafSeed))
		}
		if out.m.rows[0][0].p == nil || out.m.rows[0][0].p.v != base+900 {
			ck(false, fmt.Sprintf("round %d: returned nested pointer lost seed=%d", r, uafSeed))
		}
		if out.tag != "mut-bundle" || out.m.rows[1][1].s != "mut-leaf" {
			ck(false, fmt.Sprintf("round %d: returned nested scalar lost seed=%d", r, uafSeed))
		}
		if len(out.list) == 0 || out.list[len(out.list)-1] == nil || out.list[len(out.list)-1].v != base+902 {
			ck(false, fmt.Sprintf("round %d: returned appended list lost seed=%d", r, uafSeed))
		}

		var direct int64
		da := [2]leaf{
			{p: mkItem(base + 300), s: "d0", n: 1},
			{p: mkItem(base + 301), s: "d1", n: 2},
		}
		dx := makeBundle(base+200, mode)
		dy := makeBundle(base+250, mode^1)
		wantDirect := bundleSum(dx, "direct-x") + bundleSum(dy, "direct-y") + leafArraySum(da, "direct-arr")
		takeTwo(dx, dy, da, &direct)
		gcStress()
		if direct != wantDirect {
			ck(false, fmt.Sprintf("round %d: direct FCA call sum=%d want=%d seed=%d", r, direct, wantDirect, uafSeed))
		}
	}

	finish("l17_fca_defer_multiarg")
}
