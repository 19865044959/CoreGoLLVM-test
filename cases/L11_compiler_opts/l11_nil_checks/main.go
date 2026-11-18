// L11: 指针 nil 比较与循环内 nil 检查的语义——nil 接收者方法、typed nil
// 接口、nil map/slice/chan/func 以及 GC 后的 nil 判定都必须保持不变。
package main

import (
	"fmt"
	"os"
	"runtime"
	"sync/atomic"
)

var (
	failed  int32
	gcSink  [][]byte
	nilSink []*counter
)

type counter struct {
	n     int64
	label string
}

func (c *counter) incIfLive() int64 {
	if c == nil {
		return -1
	}
	c.n++
	return c.n
}

func (c *counter) nameOrNil() string {
	if c == nil {
		return "nil"
	}
	return c.label
}

func ck(ok bool, msg string) {
	if !ok {
		atomic.AddInt32(&failed, 1)
		fmt.Println("FAIL:", msg)
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

func finish(name string) {
	if n := atomic.LoadInt32(&failed); n != 0 {
		fmt.Printf("FAILED %s (%d failed checks)\n", name, n)
		os.Exit(1)
	}
	fmt.Println("ALL PASS " + name)
}

func expectPanic(name string, fn func()) {
	defer func() {
		if recover() == nil {
			ck(false, name+": expected panic")
		}
	}()
	fn()
}

func main() {
	var nc *counter
	ck(nc == nil, "zero pointer must be nil")
	ck(nc.incIfLive() == -1, "nil receiver method result mismatch")
	ck(nc.nameOrNil() == "nil", "nil receiver label mismatch")
	ck(nc == nil, "nil receiver call changed pointer")

	live := &counter{label: "live"}
	for i := int64(1); i <= 5; i++ {
		ck(live.incIfLive() == i, "live counter increment mismatch")
	}
	gcStress()
	ck(live != nil && live.n == 5 && live.label == "live", "live counter corrupted after GC")

	items := make([]*counter, 32)
	wantNils := 0
	var wantSum int64
	for i := range items {
		if i%3 == 0 {
			items[i] = nil
			wantNils++
		} else {
			items[i] = &counter{n: int64(i), label: "c"}
			wantSum += int64(i)
		}
	}
	nilSink = items
	count := func() (int, int64) {
		nils := 0
		sum := int64(0)
		for _, p := range items {
			if p == nil {
				nils++
				continue
			}
			sum += p.n
		}
		return nils, sum
	}
	n1, s1 := count()
	gcStress()
	n2, s2 := count()
	ck(n1 == wantNils && n2 == wantNils, "nil element count mismatch after GC")
	ck(s1 == wantSum && s2 == wantSum, "non-nil element sum mismatch after GC")

	var m map[string]int
	ck(m == nil && len(m) == 0, "nil map semantics mismatch")
	v, ok := m["missing"]
	ck(!ok && v == 0, "nil map read mismatch")
	for range m {
		ck(false, "nil map must not iterate")
	}
	delete(m, "x")
	expectPanic("nil map write", func() { m["x"] = 1 })
	ck(len(m) == 0, "nil map changed after panic")

	var ns []int
	ck(ns == nil && len(ns) == 0 && cap(ns) == 0, "nil slice semantics mismatch")
	for range ns {
		ck(false, "nil slice must not iterate")
	}
	ns = append(ns, 3, 1, 2)
	ck(len(ns) == 3 && ns[0] == 3 && ns[2] == 2, "nil slice append mismatch")
	ck(copy(make([]int, 2), ns) == 2, "copy into slice mismatch")
	var nilDst []int
	ck(copy(nilDst, ns) == 0, "copy into nil slice mismatch")

	var ip *counter
	var a any = ip
	ck(a != nil, "typed nil in interface must not equal nil")
	got, ok := a.(*counter)
	ck(ok && got == nil, "typed nil interface type assertion mismatch")
	var b any
	ck(b == nil, "empty interface must be nil")
	b = 5
	ck(b != nil, "non-nil interface must not be nil")

	var f func(int) int
	ck(f == nil, "nil func must be nil")
	expectPanic("nil func call", func() { _ = f(1) })

	var ch chan int
	ck(ch == nil && len(ch) == 0 && cap(ch) == 0, "nil chan semantics mismatch")
	select {
	case <-ch:
		ck(false, "receive from nil channel must not happen")
	default:
	}
	expectPanic("close nil chan", func() { close(ch) })

	var p1, p2 *counter
	ck(p1 == p2, "two nil pointers must compare equal")
	p1 = live
	ck(p1 != p2 && p1 == live, "nil vs live pointer comparison mismatch")
	p2 = p1
	ck(p1 == p2 && p2.n == 5, "pointer assignment comparison mismatch")

	loop := make([]*counter, 100)
	for i := range loop {
		if i%2 == 0 {
			loop[i] = nil
		} else {
			loop[i] = &counter{n: int64(i), label: "loop"}
		}
	}
	nilSink = append(nilSink, loop...)
	gcStress()
	nils, sum := 0, int64(0)
	for _, p := range loop {
		if p != nil {
			ck(p.label == "loop", "loop element label corrupted after GC")
			sum += p.n
		} else {
			nils++
		}
	}
	ck(nils == 50, "loop nil count mismatch after GC")
	ck(sum == 2500, "loop odd sum mismatch after GC")

	for i := 0; i < 100; i++ {
		var np *counter
		ck(np.incIfLive() == -1, "loop nil receiver check mismatch")
		if i%25 == 24 {
			gcStress()
		}
	}

	finish("l11_nil_checks")
}
