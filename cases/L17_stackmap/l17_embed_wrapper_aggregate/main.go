// L17: 嵌入 wrapper 的聚合参数。多层嵌入的聚合按值装箱进接口、按值传参，GC 后 promoted 方法与字段仍有效。
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

type core struct {
	tag  string
	head *item
	list []*item
}

type wrapA struct{ core }

type wrapB struct {
	wrapA
	extra *item
}

type wrapC struct {
	wrapB
	name string
}

type sizer interface {
	total(where string) int64
	Peek() *item
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

// total 通过嵌入链被 wrapC 提升；GC 落在方法内部，receiver 副本中的指针必须被扫描。
//
//go:noinline
func (c core) total(where string) int64 {
	gcStress()
	if c.head == nil || c.head.next == nil {
		ck(false, where+": core head corrupted")
		return 0
	}
	sum := c.head.v + c.head.next.v
	for _, it := range c.list {
		if it == nil {
			ck(false, where+": core list nil")
			continue
		}
		sum += it.v
	}
	gcStress()
	return sum
}

//go:noinline
func (c core) Peek() *item {
	gcStress()
	return c.head
}

//go:noinline
func useSizer(s sizer, where string) int64 {
	gcStress()
	v := s.total(where)
	gcStress()
	if s.Peek() == nil {
		ck(false, where+": Peek returned nil")
	}
	return v
}

//go:noinline
func useB(w wrapB) int64 {
	gcStress()
	v := w.total("byval-B")
	gcStress()
	if w.extra == nil || w.extra.name != "extra" {
		ck(false, "byval-B: extra pointer corrupted")
	}
	return v
}

//go:noinline
func useC(w wrapC) int64 {
	gcStress()
	v := w.total("byval-C")
	gcStress()
	if w.wrapB.extra == nil || w.wrapB.extra.name != "extra" || w.name != "top" {
		ck(false, "byval-C: embedded wrapper fields corrupted")
	}
	return v
}

func mkWrap(base int64, n int) wrapC {
	w := wrapC{}
	w.tag = "core"
	w.head = &item{v: base + 1, name: "head", next: &item{v: base + 2, name: "tail"}}
	w.list = make([]*item, 0, n)
	for i := 0; i < n; i++ {
		w.list = append(w.list, &item{v: base + 10 + int64(i)*2, name: "list"})
	}
	w.extra = &item{v: base + 50, name: "extra"}
	w.name = "top"
	return w
}

func expectedTotal(base int64, n int) int64 {
	sum := base + 1 + base + 2
	for i := 0; i < n; i++ {
		sum += base + 10 + int64(i)*2
	}
	return sum
}

func verifyWrap(w wrapC, base int64, n int, where string) {
	bad := w.tag != "core" || w.name != "top" ||
		w.head == nil || w.head.v != base+1 || w.head.next == nil || w.head.next.v != base+2 ||
		w.wrapB.extra == nil || w.wrapB.extra.v != base+50 || w.wrapB.extra.name != "extra" ||
		len(w.list) != n
	if !bad {
		for i := 0; i < n; i++ {
			if w.list[i] == nil || w.list[i].v != base+10+int64(i)*2 || w.list[i].name != "list" {
				bad = true
				break
			}
		}
	}
	ck(!bad, where+": embedded aggregate corrupted after GC")
}

func main() {
	uafSeed = seedFromEnv()
	const rounds = 8

	for rnd := 0; rnd < rounds; rnd++ {
		base := int64(rnd*1000) + uafSeed%17
		n := 3 + int((uafSeed+int64(rnd))%4)
		w := mkWrap(base, n)
		want := expectedTotal(base, n)

		gcStress()
		verifyWrap(w, base, n, fmt.Sprintf("round %d built", rnd))

		gb := useB(w.wrapB)
		ck(gb == want, fmt.Sprintf("round %d useB=%d want=%d seed=%d", rnd, gb, want, uafSeed))
		gcStress()
		verifyWrap(w, base, n, fmt.Sprintf("round %d after useB", rnd))

		gcc := useC(w)
		ck(gcc == want, fmt.Sprintf("round %d useC=%d want=%d seed=%d", rnd, gcc, want, uafSeed))
		gcStress()
		verifyWrap(w, base, n, fmt.Sprintf("round %d after useC", rnd))

		var s sizer = w
		gv := useSizer(s, "iface-value")
		ck(gv == want, fmt.Sprintf("round %d iface value=%d want=%d seed=%d", rnd, gv, want, uafSeed))
		gcStress()
		p := s.Peek()
		if p == nil || p.v != base+1 || p.name != "head" {
			ck(false, fmt.Sprintf("round %d iface Peek after GC seed=%d", rnd, uafSeed))
		}

		var sp sizer = &w
		gp := useSizer(sp, "iface-ptr")
		ck(gp == want, fmt.Sprintf("round %d iface ptr=%d want=%d seed=%d", rnd, gp, want, uafSeed))
		gcStress()
		verifyWrap(w, base, n, fmt.Sprintf("round %d after iface ptr", rnd))

		// 方法表达式：promoted 方法需要按嵌入偏移调整 receiver。
		f := wrapB.total
		gx := f(w.wrapB, "expr")
		ck(gx == want, fmt.Sprintf("round %d method expr=%d want=%d seed=%d", rnd, gx, want, uafSeed))
		gcStress()
		verifyWrap(w, base, n, fmt.Sprintf("round %d after expr", rnd))
	}

	finish("l17_embed_wrapper_aggregate")
}
