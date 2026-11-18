// L17: 闭包共享可变捕获单元。多个闭包捕获同一个 *item 变量与 seen 切片，
// 其中一个闭包重绑该单元；每步经接口调用后 GC，其他闭包必须看到新值。
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
	return &item{v: base, tag: "cc", next: &item{v: base + 5, tag: "cc-next"}}
}

func itemVal(x *item, where string) int64 {
	if x == nil {
		ck(false, fmt.Sprintf("%s: nil item seed=%d", where, uafSeed))
		return 0
	}
	if x.tag != "cc" || x.next == nil || x.next.tag != "cc-next" || x.next.v != x.v+5 {
		ck(false, fmt.Sprintf("%s: item chain corrupted seed=%d", where, uafSeed))
	}
	return x.v + x.next.v
}

type stepper interface {
	step() int64
}

type chain struct {
	fns []func() int64
	idx int
	tag string
}

func (c *chain) step() int64 {
	f := c.fns[c.idx]
	c.idx = (c.idx + 1) % len(c.fns)
	return f()
}

type cell struct {
	get     func() *item
	seenSum func() int64
}

func build(base int64) (*chain, cell) {
	cur := mkItem(base)
	seen := []*item{cur}
	var k int64
	fns := []func() int64{
		func() int64 { return cur.v },
		func() int64 {
			k++
			cur = mkItem(base + 10*k)
			seen = append(seen, cur)
			return cur.v
		},
		func() int64 { return cur.v*1000 + int64(len(seen)) },
		func() int64 { return int64(len(seen))*7 + cur.next.v%13 },
	}
	return &chain{fns: fns, tag: "chain"}, cell{
		get: func() *item { return cur },
		seenSum: func() int64 {
			var sum int64
			for i, p := range seen {
				sum += itemVal(p, fmt.Sprintf("seen[%d]", i))
			}
			return sum
		},
	}
}

func main() {
	uafSeed = seedFromEnv()
	for r := 0; r < 4; r++ {
		base := int64(r*1000) + uafSeed%53
		st := stepper(nil)
		var c cell
		st, c = build(base)

		modelV := base
		modelLen := int64(1)
		var modelK int64
		modelSeen := int64(2*base + 5)

		for i := 0; i < 16; i++ {
			var want int64
			switch i % 4 {
			case 0:
				want = modelV
			case 1:
				modelK++
				modelV = base + 10*modelK
				modelLen++
				modelSeen += 2*modelV + 5
				want = modelV
			case 2:
				want = modelV*1000 + modelLen
			case 3:
				want = modelLen*7 + (modelV+5)%13
			}

			got := st.step()
			if got != want {
				ck(false, fmt.Sprintf("round %d step %d: closure=%d want=%d k=%d len=%d seed=%d", r, i, got, want, modelK, modelLen, uafSeed))
			}

			gcStress()
			p := c.get()
			if p == nil || p.v != modelV {
				ck(false, fmt.Sprintf("round %d step %d: shared capture p.v=%v want=%d seed=%d", r, i, p, modelV, uafSeed))
			} else {
				itemVal(p, fmt.Sprintf("round %d step %d get", r, i))
			}
			if ss := c.seenSum(); ss != modelSeen {
				ck(false, fmt.Sprintf("round %d step %d: seen sum=%d want=%d seed=%d", r, i, ss, modelSeen, uafSeed))
			}
		}

		gcStress()
		if ss := c.seenSum(); ss != modelSeen {
			ck(false, fmt.Sprintf("round %d final: seen sum after GC=%d want=%d seed=%d", r, ss, modelSeen, uafSeed))
		}
		if p := c.get(); p == nil || p.v != modelV {
			ck(false, fmt.Sprintf("round %d final: captured cell lost seed=%d", r, uafSeed))
		}
	}

	finish("l17_closure_cell_rebind_chain")
}
