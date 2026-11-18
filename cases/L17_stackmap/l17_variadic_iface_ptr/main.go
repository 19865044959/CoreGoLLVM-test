// L17: 变参 interface 持针。callee 的 ...any 变参切片、caller 的 []any 在 GC 后仍须指向有效对象。
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

type group struct {
	head  *item
	list  []*item
	label string
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

type lcg struct{ s uint64 }

func newLCG(seed int64) *lcg {
	return &lcg{s: uint64(seed)*0x9e3779b97f4a7c15 + 0x2545f4914f6cdd1d}
}

func (l *lcg) next() uint64 {
	l.s = l.s*6364136223846793005 + 1442695040888963407
	return l.s >> 33
}

func (l *lcg) intn(n int) int {
	if n <= 0 {
		return 0
	}
	return int(l.next() % uint64(n))
}

func (l *lcg) perm(n int) []int {
	p := make([]int, n)
	for i := range p {
		p[i] = i
	}
	for i := n - 1; i > 0; i-- {
		j := l.intn(i + 1)
		p[i], p[j] = p[j], p[i]
	}
	return p
}

func mkItem(v int64) *item {
	return &item{v: v, name: "it", next: &item{v: v + 1000, name: "next"}}
}

// fold 反复扫描变参切片；每次读取都经过动态类型断言，指针失效会立刻反映到 checksum。
func fold(args []any) int64 {
	sum := int64(0)
	for i, a := range args {
		switch x := a.(type) {
		case *item:
			if x == nil || x.next == nil {
				ck(false, fmt.Sprintf("fold arg[%d]: nil item", i))
				continue
			}
			if x.name != "it" || x.next.name != "next" {
				ck(false, fmt.Sprintf("fold arg[%d]: item names corrupted", i))
			}
			sum += x.v + x.next.v
		case *group:
			if x == nil || x.head == nil || x.head.next == nil {
				ck(false, fmt.Sprintf("fold arg[%d]: nil group", i))
				continue
			}
			sum += x.head.v + x.head.next.v
			for _, it := range x.list {
				if it == nil {
					ck(false, fmt.Sprintf("fold arg[%d]: nil group list", i))
					continue
				}
				sum += it.v
			}
			if x.label != "grp" {
				ck(false, fmt.Sprintf("fold arg[%d]: group label corrupted", i))
			}
		case []*item:
			for _, it := range x {
				if it == nil {
					ck(false, fmt.Sprintf("fold arg[%d]: nil slice item", i))
					continue
				}
				sum += it.v
			}
		case string:
			sum += int64(len(x))
		default:
			ck(false, fmt.Sprintf("fold arg[%d]: unexpected type %T", i, a))
		}
	}
	return sum
}

//go:noinline
func consume(args ...any) (int64, int64) {
	gcStress()
	a := fold(args)
	gcStress()
	b := fold(args)
	return a, b
}

type collector struct {
	base int64
	keep []*item
}

//go:noinline
func (c *collector) add(prefix int64, vals ...any) int64 {
	gcStress()
	sum := prefix
	for i, v := range vals {
		switch x := v.(type) {
		case *item:
			if x == nil {
				ck(false, fmt.Sprintf("method vals[%d]: nil item", i))
				continue
			}
			sum += x.v
		case *group:
			if x == nil || x.head == nil {
				ck(false, fmt.Sprintf("method vals[%d]: nil group", i))
				continue
			}
			sum += x.head.v
		case []*item:
			for _, it := range x {
				if it == nil {
					ck(false, fmt.Sprintf("method vals[%d]: nil slice item", i))
					continue
				}
				sum += it.v
			}
		}
	}
	gcStress()
	return sum
}

func main() {
	uafSeed = seedFromEnv()
	rng := newLCG(uafSeed)
	const rounds = 8

	for rnd := 0; rnd < rounds; rnd++ {
		base := int64(rnd*10000) + int64(rng.intn(97))
		n := 8 + rng.intn(7)

		nodes := make([]*item, 0, n)
		exp := int64(0)
		for i := 0; i < n; i++ {
			it := mkItem(base + int64(i)*3)
			nodes = append(nodes, it)
			exp += it.v + it.next.v
		}

		g := &group{
			head:  mkItem(base + 7),
			list:  []*item{mkItem(base + 11), mkItem(base + 13), mkItem(base + 17)},
			label: "grp",
		}
		exp += g.head.v + g.head.next.v + g.list[0].v + g.list[1].v + g.list[2].v
		label := "variadic"
		exp += int64(len(label))

		args := make([]any, 0, n+2)
		args = append(args, g)
		args = append(args, label)
		for _, k := range rng.perm(n) {
			args = append(args, nodes[k])
		}

		a, b := consume(args...)
		ck(a == exp, fmt.Sprintf("round %d consume pass1 got=%d want=%d seed=%d", rnd, a, exp, uafSeed))
		ck(b == exp, fmt.Sprintf("round %d consume pass2 got=%d want=%d seed=%d", rnd, b, exp, uafSeed))

		gcStress()
		for i, it := range nodes {
			if it == nil || it.v != base+int64(i)*3 || it.next == nil || it.next.v != it.v+1000 || it.name != "it" {
				ck(false, fmt.Sprintf("round %d caller node[%d] corrupted seed=%d", rnd, i, uafSeed))
				break
			}
		}
		if g.head == nil || g.head.v != base+7 || len(g.list) != 3 || g.list[2].v != base+17 || g.label != "grp" {
			ck(false, fmt.Sprintf("round %d caller group corrupted seed=%d", rnd, uafSeed))
		}

		col := &collector{base: base}
		allVals := make([]any, 0, len(nodes)+1)
		allVals = append(allVals, g)
		for _, it := range nodes {
			allVals = append(allVals, it)
		}
		got := col.add(base+5, allVals...)
		want := base + 5 + g.head.v
		for _, it := range nodes {
			want += it.v
		}
		ck(got == want, fmt.Sprintf("round %d method variadic got=%d want=%d seed=%d", rnd, got, want, uafSeed))

		gcStress()
		if col.base != base || g.head == nil || g.head.v != base+7 {
			ck(false, fmt.Sprintf("round %d caller objects after method GC seed=%d", rnd, uafSeed))
		}
	}

	finish("l17_variadic_iface_ptr")
}
