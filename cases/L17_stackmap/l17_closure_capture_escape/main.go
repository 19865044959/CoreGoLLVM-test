// L17: 闭包捕获指针逃逸。slice/map 中的闭包、goroutine 内调用的闭包、
// 嵌套闭包与大 struct 按值捕获，在 GC 后仍须持有有效指针。
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

type bundle struct {
	items [4]*item
	tag   string
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

func itemValue(i int, base int64) int64 { return base + int64(i)*7 + 3 }

func makeClosures(n int, base int64) ([]func() int64, map[string]func() int64, []*item) {
	fns := make([]func() int64, 0, n)
	tbl := make(map[string]func() int64, n)
	kept := make([]*item, 0, n)

	for i := 0; i < n; i++ {
		v := itemValue(i, base)
		it := &item{v: v, name: "cap", next: &item{v: v + 100, name: "n"}}
		prev := it
		fns = append(fns, func() int64 {
			return prev.v + prev.next.v + int64(len(prev.name))
		})
		idx := i
		tbl["f"+strconv.Itoa(idx)] = func() int64 { return it.v * 2 }
		kept = append(kept, it)
	}
	return fns, tbl, kept
}

// makeBundle 返回按值捕获 [4]*item 的闭包；整个 bundle 随闭包逃逸到堆上。
func makeBundle(base int64) func() int64 {
	b := bundle{tag: "bundle"}
	for i := range b.items {
		b.items[i] = &item{v: base + int64(i)*3, name: "bi"}
	}
	return func() int64 {
		s := int64(len(b.tag))
		for _, p := range b.items {
			if p == nil {
				ck(false, fmt.Sprintf("bundle closure: nil item seed=%d", uafSeed))
				continue
			}
			s += p.v
		}
		return s
	}
}

// nestedFactory 返回的闭包再返回一个闭包，两层捕获同一个 *item。
func nestedFactory(base int64) func() func() (*item, int64) {
	root := &item{v: base + 70, name: "root", next: &item{v: base + 71, name: "rn"}}
	outer := func() func() (*item, int64) {
		gcStress()
		return func() (*item, int64) {
			gcStress()
			return root, root.next.v
		}
	}
	return outer
}

func main() {
	uafSeed = seedFromEnv()

	for rnd := 0; rnd < 5; rnd++ {
		base := int64(rnd*500) + uafSeed%11
		n := 8 + int(uafSeed%4)

		fns, tbl, kept := makeClosures(n, base)
		gcStress()

		for i := 0; i < n; i++ {
			v := itemValue(i, base)
			want := v + (v + 100) + int64(len("cap"))
			got := fns[i]()
			ck(got == want, fmt.Sprintf("round %d: fns[%d] got=%d want=%d seed=%d",
				rnd, i, got, want, uafSeed))

			want2 := v * 2
			got2 := tbl["f"+strconv.Itoa(i)]()
			ck(got2 == want2, fmt.Sprintf("round %d: tbl f%d got=%d want=%d seed=%d",
				rnd, i, got2, want2, uafSeed))

			if kept[i] == nil || kept[i].v != v || kept[i].next == nil ||
				kept[i].next.v != v+100 || kept[i].name != "cap" {
				ck(false, fmt.Sprintf("round %d: kept[%d] corrupted seed=%d", rnd, i, uafSeed))
			}
		}

		// goroutine 内调用闭包，跨 GC 取回结果。
		resCh := make(chan int64, 1)
		go func() {
			k := 1 + int(uafSeed)%3
			if k >= n {
				k = n - 1
			}
			resCh <- fns[k]()
		}()
		gcStress()
		gotG := <-resCh
		vG := itemValue(1+int(uafSeed)%3, base)
		if 1+int(uafSeed)%3 >= n {
			vG = itemValue(n-1, base)
		}
		wantG := vG + (vG + 100) + int64(len("cap"))
		ck(gotG == wantG, fmt.Sprintf("round %d: goroutine closure got=%d want=%d seed=%d",
			rnd, gotG, wantG, uafSeed))

		bf := makeBundle(base)
		gcStress()
		wantB := int64(len("bundle"))
		for i := 0; i < 4; i++ {
			wantB += base + int64(i)*3
		}
		ck(bf() == wantB, fmt.Sprintf("round %d: bundle closure got=%d want=%d seed=%d",
			rnd, bf(), wantB, uafSeed))

		nf := nestedFactory(base)
		inner := nf()
		gcStress()
		p, nv := inner()
		ck(p != nil && p.v == base+70 && p.name == "root" &&
			p.next != nil && p.next.v == base+71 && nv == base+71,
			fmt.Sprintf("round %d: nested closure corrupted seed=%d", rnd, uafSeed))
		gcStress()
		ck(p.v == base+70 && p.next.v == base+71,
			fmt.Sprintf("round %d: nested closure pointer after GC seed=%d", rnd, uafSeed))

		// 最后再 GC 并重复调用全部闭包。
		gcStress()
		for i := 0; i < n; i++ {
			v := itemValue(i, base)
			ck(fns[i]() == v+(v+100)+int64(len("cap")),
				fmt.Sprintf("round %d: fns[%d] after final GC seed=%d", rnd, i, uafSeed))
		}
	}

	finish("l17_closure_capture_escape")
}
