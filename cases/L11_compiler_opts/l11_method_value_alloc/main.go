// L11: 方法值分配——方法值在创建时绑定接收者；值接收者复制、指针接收者
// 共享对象、方法表达式/接口方法值/nil 指针方法与 GC 后调用都必须语义一致。
package main

import (
	"fmt"
	"os"
	"runtime"
	"strconv"
	"sync/atomic"
)

var (
	failed int32
	gcSink [][]byte
	seed   uint64 = 1
)

func ck(ok bool, msg string) {
	if !ok {
		atomic.AddInt32(&failed, 1)
		fmt.Printf("FAIL: %s (UAF_SEED=%d)\n", msg, seed)
	}
}

func gcStress() {
	for i := 0; i < 16; i++ {
		b := make([]byte, 4096)
		b[0] = byte(i)
		b[len(b)-1] = byte(255 - i)
		gcSink = append(gcSink[:0], b)
	}
	gcSink = gcSink[:0]
	runtime.GC()
	runtime.GC()
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

func tortureMultiplier() int {
	s := os.Getenv("TORTURE_ROUNDS")
	if s == "" {
		return 1
	}
	n, err := strconv.Atoi(s)
	if err != nil || n <= 0 {
		return 1
	}
	if n > 50 {
		n = 50
	}
	return n
}

func finish(name string) {
	if n := atomic.LoadInt32(&failed); n != 0 {
		fmt.Printf("FAILED %s (%d failed checks, seed=%d)\n", name, n, seed)
		os.Exit(1)
	}
	fmt.Println("ALL PASS " + name)
}

type mvCounter struct {
	n   int64
	tag string
}

func (c mvCounter) Value() int64     { return c.n }
func (c mvCounter) Describe() string { return c.tag }
func (c *mvCounter) Bump(d int64) int64 {
	c.n += d
	return c.n
}

type mvIface interface {
	Value() int64
	Bump(int64) int64
}

type mvValueIface interface {
	Value() int64
}

type mvBig struct {
	data [8]int64
	sum  int64
}

func (b mvBig) Sum() int64     { return b.sum }
func (b mvBig) At(i int) int64 { return b.data[i] }

type mvNilSafe struct{ base int64 }

func (p *mvNilSafe) Get() int64 {
	if p == nil {
		return -1
	}
	return p.base
}

type mvPair struct {
	vf   func() int64
	bf   func(int64) int64
	orig int64
	cur  int64
}

func main() {
	seed = loadSeed()
	rounds := 4 * tortureMultiplier()
	n := 48 + int(seed%16)

	// 值接收者方法值在创建时复制接收者。
	c := mvCounter{n: 10, tag: "x"}
	f := c.Value
	c.n = 99
	ck(f() == 10, "value method value did not copy receiver")

	// 指针接收者方法值共享同一对象。
	c2 := &mvCounter{n: 1}
	g := c2.Bump
	ck(g(4) == 5 && c2.n == 5, "pointer method value did not share receiver")
	ck(g(1) == 6, "pointer method value second call mismatch")

	// 重新赋值变量不改变已创建的方法值。
	c3 := mvCounter{n: 7}
	f3 := c3.Value
	c3 = mvCounter{n: 8}
	ck(f3() == 7, "value method value followed variable reassignment")

	p1 := &mvCounter{n: 3}
	h := p1.Bump
	p1 = &mvCounter{n: 100}
	ck(h(1) == 4, "pointer method value followed pointer reassignment")
	ck(p1.n == 100, "reassigned pointer receiver modified unexpectedly")

	// 切片元素的方法值各自独立。
	cs := []mvCounter{{n: 1}, {n: 2}, {n: 3}}
	fs := make([]func() int64, len(cs))
	for i := range cs {
		fs[i] = cs[i].Value
	}
	for i := range cs {
		cs[i].n *= 10
	}
	for i := range fs {
		ck(fs[i]() == int64(i+1), "slice element method value copied wrong receiver")
	}

	// map 中的方法值。
	m := map[string]func() int64{"a": cs[0].Value, "b": cs[1].Value}
	ck(m["a"]() == 10 && m["b"]() == 20, "map method value mismatch")

	// 方法表达式与直接调用一致。
	expr := mvCounter.Value
	ck(expr(mvCounter{n: 42}) == 42, "method expression value mismatch")
	ptrExpr := (*mvCounter).Bump
	pp := &mvCounter{n: 1}
	ck(ptrExpr(pp, 2) == 3 && pp.n == 3, "method expression pointer mismatch")

	// 大接收者按值复制。
	big := mvBig{sum: 36}
	for i := range big.data {
		big.data[i] = int64(i + 1)
	}
	bf := big.Sum
	ba := big.At
	big.sum = 999
	big.data[0] = 1000
	ck(bf() == 36, "big value method value did not copy receiver")
	ck(ba(0) == 1, "big value method value array element changed")

	// 字符串字段同样在创建时复制。
	cd := mvCounter{n: 1, tag: "orig"}
	df := cd.Describe
	cd.tag = "changed"
	ck(df() == "orig", "value method value tag not copied")

	// 接口上的方法值绑定到当时的动态值。
	var i mvIface = &mvCounter{n: 5}
	gv := i.Bump
	ck(gv(5) == 10, "interface method value bump mismatch")

	c4 := mvCounter{n: 6}
	var vi mvValueIface = c4
	fv := vi.Value
	c4.n = 7
	ck(fv() == 6, "interface value method value not copied")

	// nil 指针接收者的方法值本身合法。
	var np *mvNilSafe
	nf := np.Get
	ck(nf() == -1, "nil receiver method value mismatch")

	// 大量方法值跨多轮 GC 后仍绑定各自的接收者。
	pairs := make([]mvPair, 0, rounds*n)
	for r := 0; r < rounds; r++ {
		for j := 0; j < n; j++ {
			cc := mvCounter{n: int64(r*n+j)*3 + 1, tag: "retained"}
			vf := cc.Value
			bl := cc.Bump
			orig := cc.n
			got := bl(2)
			ck(vf() == orig, "value method value changed with receiver")
			ck(got == orig+2, "pointer method value bump mismatch")
			pairs = append(pairs, mvPair{vf: vf, bf: bl, orig: orig, cur: got})
		}
		gcStress()
	}
	gcStress()
	for idx := range pairs {
		p := &pairs[idx]
		ck(p.vf() == p.orig, "retained value method value corrupted after GC")
		want := p.cur + 1
		ck(p.bf(1) == want, "retained pointer method value lost receiver after GC")
		p.cur = want
	}

	// 大接收者方法值批量保留后再 GC。
	bigs := make([]mvBig, rounds*n/4)
	bigFs := make([]func() int64, len(bigs))
	for idx := range bigs {
		bigs[idx].sum = int64(idx)
		bigs[idx].data[0] = int64(idx * 2)
		bigFs[idx] = bigs[idx].Sum
	}
	gcStress()
	for idx := range bigs {
		bigs[idx].sum = -1
		ck(bigFs[idx]() == int64(idx), "big retained method value corrupted after GC")
	}

	gcStress()
	for idx := range pairs {
		ck(pairs[idx].vf() == pairs[idx].orig, "value method value corrupted after second GC")
		ck(pairs[idx].bf(1) == pairs[idx].cur+1, "pointer method value corrupted after second GC")
		pairs[idx].cur++
	}

	finish("l11_method_value_alloc")
}
