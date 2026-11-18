// L05: 大数组零化 + GC。反复 new 固定大小数组（跨 32KB 大对象阈值的
// [4095]*node / [4097]*node、[512]/[600] 含指针 cell、[2048]any、约 1MB 的
// [1024]含 blob 的 struct 数组）。每代先验证全零，再写满、GC 校验；丢弃部分
// 代后再次分配同型数组，确认复用已回收 span 时仍被完整零化（clobberfree 下
// 尤其敏感）。
package main

import (
	"fmt"
	"os"
	"runtime"
	"strconv"
	"sync/atomic"
)

const (
	bzGens   = 4
	bzBigLen = 1024
)

var (
	failed int32
	gcSink [][]byte
)

type bzNode struct {
	id   int64
	name string
}

type bzCell struct {
	p *bzNode
	s string
	b []byte
	n int64
}

type bzFixed struct {
	blob [bzBigLen]byte
	tag  string
	p    *bzNode
}

type bzPtrSmall [4095]*bzNode
type bzPtrLarge [4097]*bzNode
type bzCellSmall [512]bzCell
type bzCellLarge [600]bzCell
type bzAnyArr [2048]any
type bzFixedArr [1024]bzFixed

func ck(ok bool, msg string) {
	if !ok {
		atomic.AddInt32(&failed, 1)
		fmt.Println("FAIL:", msg)
	}
}

func ckf(ok bool, format string, args ...interface{}) {
	if !ok {
		atomic.AddInt32(&failed, 1)
		fmt.Printf("FAIL: "+format+"\n", args...)
	}
}

func loadSeed() uint64 {
	s := os.Getenv("UAF_SEED")
	if s == "" {
		return 1
	}
	n, err := strconv.ParseUint(s, 10, 64)
	if err != nil {
		return 1
	}
	return n
}

func finish(name string, seed uint64) {
	if n := atomic.LoadInt32(&failed); n != 0 {
		fmt.Printf("FAILED %s (%d failed checks, seed=%d)\n", name, n, seed)
		os.Exit(1)
	}
	fmt.Println("ALL PASS " + name)
}

func bzGC() {
	for i := 0; i < 8; i++ {
		b := make([]byte, 128<<10)
		b[0] = byte(i)
		gcSink = append(gcSink[:0], b)
	}
	gcSink = gcSink[:0]
	runtime.GC()
	runtime.GC()
}

func bzZeroPtrs(tag string, a []*bzNode) bool {
	for i := range a {
		if a[i] != nil {
			ckf(false, "%s non-zero pointer at=%d", tag, i)
			return false
		}
	}
	return true
}

func bzZeroCells(tag string, a []bzCell) bool {
	for i := range a {
		if a[i].p != nil || a[i].s != "" || a[i].b != nil || a[i].n != 0 {
			ckf(false, "%s non-zero cell at=%d", tag, i)
			return false
		}
	}
	return true
}

func bzZeroAnys(tag string, a []any) bool {
	for i := range a {
		if a[i] != nil {
			ckf(false, "%s non-zero any at=%d", tag, i)
			return false
		}
	}
	return true
}

func bzZeroFixed(tag string, a []bzFixed) bool {
	for i := range a {
		if a[i].p != nil || a[i].tag != "" {
			ckf(false, "%s non-zero fixed at=%d", tag, i)
			return false
		}
		for j := range a[i].blob {
			if a[i].blob[j] != 0 {
				ckf(false, "%s non-zero fixed blob at=%d,%d", tag, i, j)
				return false
			}
		}
	}
	return true
}

func bzFillPtrs(a []*bzNode, gen int) {
	for i := range a {
		a[i] = &bzNode{id: int64(gen*1000000 + i), name: fmt.Sprintf("p-%d-%d", gen, i)}
	}
}

func bzFillCells(a []bzCell, gen int, seed uint64) {
	for i := range a {
		n := 2 + (i+gen)%6
		b := make([]byte, n)
		for j := range b {
			b[j] = byte((seed*7 + uint64(gen)*31 + uint64(i)*13 + uint64(j)*3) % 251)
		}
		a[i] = bzCell{
			p: &bzNode{id: int64(gen*1000000 + i), name: fmt.Sprintf("c-%d-%d", gen, i)},
			s: fmt.Sprintf("s-%d-%d", gen, i),
			b: b,
			n: int64(gen*1000000 + i),
		}
	}
}

func bzFillAnys(a []any, gen int, seed uint64) {
	for i := range a {
		switch (i + gen) % 4 {
		case 0:
			a[i] = int64(gen*1000000 + i)
		case 1:
			a[i] = fmt.Sprintf("a-%d-%d", gen, i)
		case 2:
			a[i] = &bzNode{id: int64(gen*1000000 + i), name: fmt.Sprintf("n-%d-%d", gen, i)}
		default:
			a[i] = []byte{byte(gen), byte(i), byte((seed + uint64(i)) % 251)}
		}
	}
}

func bzFillFixed(a []bzFixed, gen int, seed uint64) {
	for i := range a {
		for j := range a[i].blob {
			a[i].blob[j] = byte((seed*11 + uint64(gen)*17 + uint64(i)*5 + uint64(j)*3) % 251)
		}
		a[i].tag = fmt.Sprintf("f-%d-%d", gen, i)
		a[i].p = &bzNode{id: int64(gen*1000000 + i), name: fmt.Sprintf("fp-%d-%d", gen, i)}
	}
}

func bzVerifyPtrs(tag string, a []*bzNode, gen int) {
	for i := 0; i < len(a); i += 397 {
		ckf(a[i] != nil, "%s nil ptr at=%d", tag, i)
		if a[i] != nil {
			ckf(a[i].id == int64(gen*1000000+i), "%s ptr id at=%d got=%d", tag, i, a[i].id)
			ckf(a[i].name == fmt.Sprintf("p-%d-%d", gen, i), "%s ptr name at=%d", tag, i)
		}
	}
}

func bzVerifyCells(tag string, a []bzCell, gen int, seed uint64) {
	for i := 0; i < len(a); i += 37 {
		ckf(a[i].p != nil, "%s nil cell ptr at=%d", tag, i)
		if a[i].p != nil {
			ckf(a[i].p.id == int64(gen*1000000+i), "%s cell ptr id at=%d", tag, i)
		}
		ckf(a[i].s == fmt.Sprintf("s-%d-%d", gen, i), "%s cell string at=%d", tag, i)
		ckf(len(a[i].b) == 2+(i+gen)%6, "%s cell body len at=%d", tag, i)
		if len(a[i].b) > 0 {
			ckf(a[i].b[0] == byte((seed*7+uint64(gen)*31+uint64(i)*13)%251), "%s cell byte at=%d", tag, i)
		}
	}
}

func bzVerifyAnys(tag string, a []any, gen int) {
	for i := 0; i < len(a); i += 131 {
		ckf(a[i] != nil, "%s nil any at=%d", tag, i)
		switch (i + gen) % 4 {
		case 0:
			x, ok := a[i].(int64)
			ckf(ok && x == int64(gen*1000000+i), "%s any int at=%d", tag, i)
		case 1:
			s, ok := a[i].(string)
			ckf(ok && s == fmt.Sprintf("a-%d-%d", gen, i), "%s any string at=%d", tag, i)
		case 2:
			p, ok := a[i].(*bzNode)
			ckf(ok && p != nil && p.id == int64(gen*1000000+i), "%s any ptr at=%d", tag, i)
		default:
			b, ok := a[i].([]byte)
			ckf(ok && len(b) == 3, "%s any bytes at=%d", tag, i)
		}
	}
}

func bzVerifyFixed(tag string, a []bzFixed, gen int, seed uint64) {
	for i := 0; i < len(a); i += 97 {
		ckf(a[i].p != nil, "%s nil fixed ptr at=%d", tag, i)
		ckf(a[i].tag == fmt.Sprintf("f-%d-%d", gen, i), "%s fixed tag at=%d", tag, i)
		ckf(a[i].blob[0] == byte((seed*11+uint64(gen)*17+uint64(i)*5)%251), "%s fixed blob[0] at=%d", tag, i)
		ckf(a[i].blob[len(a[i].blob)-1] == byte((seed*11+uint64(gen)*17+uint64(i)*5+uint64(len(a[i].blob)-1)*3)%251), "%s fixed blob tail at=%d", tag, i)
	}
}

type bzPin struct {
	gen int
	v   any
}

func main() {
	seed := loadSeed()
	pins := make([]bzPin, 0, bzGens*6)

	for gen := 0; gen < bzGens; gen++ {
		ps := new(bzPtrSmall)
		pl := new(bzPtrLarge)
		cs := new(bzCellSmall)
		cl := new(bzCellLarge)
		aa := new(bzAnyArr)
		fa := new(bzFixedArr)
		ckf(ps != nil && pl != nil && cs != nil && cl != nil && aa != nil && fa != nil, "gen=%d new returned nil", gen)

		// 关键不变量：新分配（含复用已释放 span）必须全零。
		bzZeroPtrs(fmt.Sprintf("gen=%d ptrSmall fresh", gen), ps[:])
		bzZeroPtrs(fmt.Sprintf("gen=%d ptrLarge fresh", gen), pl[:])
		bzZeroCells(fmt.Sprintf("gen=%d cellSmall fresh", gen), cs[:])
		bzZeroCells(fmt.Sprintf("gen=%d cellLarge fresh", gen), cl[:])
		bzZeroAnys(fmt.Sprintf("gen=%d any fresh", gen), aa[:])
		bzZeroFixed(fmt.Sprintf("gen=%d fixed fresh", gen), fa[:])

		bzFillPtrs(ps[:], gen)
		bzFillPtrs(pl[:], gen)
		bzFillCells(cs[:], gen, seed)
		bzFillCells(cl[:], gen, seed)
		bzFillAnys(aa[:], gen, seed)
		bzFillFixed(fa[:], gen, seed)

		bzGC()
		bzVerifyPtrs(fmt.Sprintf("gen=%d ptrSmall", gen), ps[:], gen)
		bzVerifyPtrs(fmt.Sprintf("gen=%d ptrLarge", gen), pl[:], gen)
		bzVerifyCells(fmt.Sprintf("gen=%d cellSmall", gen), cs[:], gen, seed)
		bzVerifyCells(fmt.Sprintf("gen=%d cellLarge", gen), cl[:], gen, seed)
		bzVerifyAnys(fmt.Sprintf("gen=%d any", gen), aa[:], gen)
		bzVerifyFixed(fmt.Sprintf("gen=%d fixed", gen), fa[:], gen, seed)

		// 偶数代 pin 住数组，奇数代整代丢弃；下代分配应复用其 span 并仍全零。
		if gen%2 == 0 {
			pins = append(pins, bzPin{gen, ps}, bzPin{gen, pl}, bzPin{gen, cs}, bzPin{gen, cl}, bzPin{gen, aa}, bzPin{gen, fa})
		} else {
			ps, pl, cs, cl, aa, fa = nil, nil, nil, nil, nil, nil
			bzGC()
		}
	}

	// 最后一轮丢弃全部数组并重新分配，确认最末代仍然零化。
	bzGC()
	final := new(bzFixedArr)
	ckf(bzZeroFixed("final fixed fresh", final[:]), "final fixed not zeroed")
	finalCells := new(bzCellLarge)
	ckf(bzZeroCells("final cells fresh", finalCells[:]), "final cells not zeroed")
	runtime.KeepAlive(final)
	runtime.KeepAlive(finalCells)

	// pin 数组跨后续 GC 后内容必须保留。
	bzGC()
	for _, p := range pins {
		switch a := p.v.(type) {
		case *bzPtrSmall:
			bzVerifyPtrs(fmt.Sprintf("pin gen=%d ptrSmall", p.gen), a[:], p.gen)
		case *bzPtrLarge:
			bzVerifyPtrs(fmt.Sprintf("pin gen=%d ptrLarge", p.gen), a[:], p.gen)
		case *bzCellSmall:
			bzVerifyCells(fmt.Sprintf("pin gen=%d cellSmall", p.gen), a[:], p.gen, seed)
		case *bzCellLarge:
			bzVerifyCells(fmt.Sprintf("pin gen=%d cellLarge", p.gen), a[:], p.gen, seed)
		case *bzAnyArr:
			bzVerifyAnys(fmt.Sprintf("pin gen=%d any", p.gen), a[:], p.gen)
		case *bzFixedArr:
			bzVerifyFixed(fmt.Sprintf("pin gen=%d fixed", p.gen), a[:], p.gen, seed)
		default:
			ckf(false, "pin gen=%d unexpected type %T", p.gen, p.v)
		}
	}

	finish("l05_big_array_rezero", seed)
}
