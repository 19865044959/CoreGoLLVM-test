// L17: 混合 float/指针/string 参数超出寄存器后的溢出槽，以及函数体内多个
// 存活指针局部变量/指针数组在间接调用点被迫 spill 后的栈扫描。
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

type accum struct {
	total int64
	base  int64
	keep  *item
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
func probe(p *item, s string, w int64) int64 {
	gcStress()
	if p == nil || p.next == nil || p.name != "p" || p.next.name != "n" {
		ck(false, fmt.Sprintf("probe: pointer corrupted seed=%d", uafSeed))
		return 0
	}
	if s != "s0" && s != "s1" && s != "s2" && s != "s3" {
		ck(false, fmt.Sprintf("probe: string arg corrupted seed=%d", uafSeed))
	}
	return p.v + p.next.v + int64(len(s)) + w
}

// mixABI 有 8 个 float、10 个指针、4 个 string 与 1 个 *accum 参数，
// 必然产生栈上溢出槽；体内还持有 [10]*item 与 [4]string 数组跨 probe 调用。
//
//go:noinline
func mixABI(
	f0, f1, f2, f3, f4, f5, f6, f7 float64,
	p0, p1, p2, p3, p4, p5, p6, p7, p8, p9 *item,
	s0, s1, s2, s3 string,
	acc *accum,
) int64 {
	gcStress()
	ps := [10]*item{p0, p1, p2, p3, p4, p5, p6, p7, p8, p9}
	ss := [4]string{s0, s1, s2, s3}
	fs := [8]float64{f0, f1, f2, f3, f4, f5, f6, f7}

	sum := int64(0)
	for i := 0; i < 10; i++ {
		idx := (i * 3) % 10
		sum += probe(ps[idx], ss[i%4], int64(fs[i%8]))
	}

	gcStress()
	for i, p := range ps {
		if p == nil || p.v != acc.base+int64(i) || p.next == nil ||
			p.next.v != acc.base+100+int64(i) || p.name != "p" {
			ck(false, fmt.Sprintf("mixABI: spilled arg p%d corrupted seed=%d", i, uafSeed))
			continue
		}
		sum += p.v
	}
	for i, s := range ss {
		if len(s) != 2 {
			ck(false, fmt.Sprintf("mixABI: string array ss[%d] corrupted seed=%d", i, uafSeed))
		}
	}
	if acc.keep == nil || acc.keep.v != acc.base+900 || acc.keep.name != "keep" {
		ck(false, fmt.Sprintf("mixABI: accum keep corrupted seed=%d", uafSeed))
	} else {
		sum += acc.keep.v
	}
	gcStress()
	acc.total = sum
	return sum
}

// spillLocal 体内 8 个指针局部跨间接调用（gcMid）存活，强制分配栈槽。
//
//go:noinline
func spillLocal(base int64, gcMid func()) int64 {
	var ps [8]*item
	for i := range ps {
		ps[i] = &item{
			v:    base + int64(i)*5,
			name: "loc",
			next: &item{v: base + int64(i)*5 + 1, name: "ln"},
		}
	}
	sum := int64(0)
	for i := 0; i < 8; i++ {
		gcMid()
		p := ps[i]
		if p == nil || p.next == nil || p.v != base+int64(i)*5 || p.next.v != base+int64(i)*5+1 {
			ck(false, fmt.Sprintf("spillLocal: ps[%d] corrupted seed=%d", i, uafSeed))
			continue
		}
		sum += p.v + p.next.v
	}
	gcStress()
	return sum
}

func runMix(base int64, rnd int) int64 {
	ps := make([]*item, 10)
	for j := range ps {
		ps[j] = &item{
			v:    base + int64(j),
			name: "p",
			next: &item{v: base + 100 + int64(j), name: "n"},
		}
	}
	ss := []string{"s0", "s1", "s2", "s3"}
	fs := []float64{1, 2, 3, 4, 5, 6, 7, 8}
	acc := &accum{base: base, keep: &item{v: base + 900, name: "keep"}}

	got := mixABI(
		fs[0], fs[1], fs[2], fs[3], fs[4], fs[5], fs[6], fs[7],
		ps[0], ps[1], ps[2], ps[3], ps[4], ps[5], ps[6], ps[7], ps[8], ps[9],
		ss[0], ss[1], ss[2], ss[3],
		acc,
	)

	want := int64(0)
	for i := 0; i < 10; i++ {
		idx := (i * 3) % 10
		want += (base + int64(idx)) + (base + 100 + int64(idx)) + int64(len(ss[i%4])) + int64(fs[i%8])
	}
	for j := 0; j < 10; j++ {
		want += base + int64(j)
	}
	want += base + 900

	ck(got == want, fmt.Sprintf("round %d: mixABI got=%d want=%d seed=%d", rnd, got, want, uafSeed))
	gcStress()
	for j, p := range ps {
		if p == nil || p.v != base+int64(j) || p.next == nil ||
			p.next.v != base+100+int64(j) || p.name != "p" || p.next.name != "n" {
			ck(false, fmt.Sprintf("round %d: caller ps[%d] corrupted seed=%d", rnd, j, uafSeed))
		}
	}
	ck(acc.keep != nil && acc.keep.v == base+900 && acc.total == want,
		fmt.Sprintf("round %d: accum corrupted seed=%d", rnd, uafSeed))
	return got
}

func main() {
	uafSeed = seedFromEnv()

	for rnd := 0; rnd < 6; rnd++ {
		base := int64(rnd*1000) + uafSeed%29
		runMix(base, rnd)

		want := int64(0)
		for i := 0; i < 8; i++ {
			want += (base + int64(i)*5) + (base + int64(i)*5 + 1)
		}
		got := spillLocal(base, gcStress)
		ck(got == want, fmt.Sprintf("round %d: spillLocal got=%d want=%d seed=%d",
			rnd, got, want, uafSeed))
		gcStress()
	}

	finish("l17_spill_local_pressure")
}
