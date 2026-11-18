// L17: 闭包 + defer 组合。闭包捕获指针、defer 在闭包内运行，GC 后捕获变量仍有效。
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

type ctx struct {
	tag  string
	root *item
	list []*item
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

// newWorker 返回的闭包持有 ctx 指针，闭包内的 defer 在 GC 后读取同一捕获指针。
//
//go:noinline
func newWorker(c *ctx, scale int64) func(int64) int64 {
	return func(x int64) (out int64) {
		defer func() {
			gcStress()
			if c == nil || c.root == nil || c.root.next == nil || c.root.next.name != "tail" {
				ck(false, "worker: captured ctx corrupted in defer")
				return
			}
			out += c.root.v
		}()
		gcStress()
		out = x*scale + int64(len(c.tag))
		return
	}
}

// closureDefers 为每个元素创建闭包；闭包内 defer 捕获的是 per-iteration 指针副本。
//
//go:noinline
func closureDefers(c *ctx) []func() int64 {
	fns := make([]func() int64, 0, len(c.list))
	for i := range c.list {
		it := c.list[i]
		fns = append(fns, func() (sum int64) {
			defer func() {
				gcStress()
				if it == nil || c.root == nil {
					ck(false, "closureDefers: captured pointer corrupted")
					return
				}
				sum += it.v
			}()
			gcStress()
			sum = it.v + c.root.v
			return
		})
	}
	return fns
}

// deferredClosure: 闭包作为 defer 目标，命名返回值被 defer 闭包改写。
//
//go:noinline
func deferredClosure(p *item, delta int64) (sum int64) {
	gcStress()
	f := func() {
		gcStress()
		if p == nil {
			ck(false, "deferredClosure: captured p nil")
			return
		}
		sum += delta
	}
	defer f()
	sum = p.v
	return
}

func main() {
	uafSeed = seedFromEnv()
	const rounds = 10

	for rnd := 0; rnd < rounds; rnd++ {
		base := int64(rnd*1000) + uafSeed%11
		n := 3 + int(uafSeed+int64(rnd))%4
		c := &ctx{
			tag:  "closure",
			root: &item{v: base + 1, name: "root", next: &item{v: base + 2, name: "tail"}},
		}
		for i := 0; i < n; i++ {
			c.list = append(c.list, &item{v: base + 10 + int64(i)*3, name: "list"})
		}

		workers := make([]func(int64) int64, 0, n)
		for i := 0; i < n; i++ {
			workers = append(workers, newWorker(c, int64(i+2)))
		}
		gcStress()
		for i, fn := range workers {
			got := fn(int64(i) + 1)
			want := (int64(i)+1)*int64(i+2) + int64(len(c.tag)) + c.root.v
			ck(got == want, fmt.Sprintf("round %d worker[%d]=%d want=%d seed=%d", rnd, i, got, want, uafSeed))
		}
		gcStress()
		for i, fn := range workers {
			got := fn(int64(i) + 3)
			want := (int64(i)+3)*int64(i+2) + int64(len(c.tag)) + c.root.v
			ck(got == want, fmt.Sprintf("round %d worker[%d] after GC=%d want=%d seed=%d", rnd, i, got, want, uafSeed))
		}

		fns := closureDefers(c)
		gcStress()
		for i, fn := range fns {
			got := fn()
			want := 2*c.list[i].v + c.root.v
			ck(got == want, fmt.Sprintf("round %d list closure[%d]=%d want=%d seed=%d", rnd, i, got, want, uafSeed))
		}
		gcStress()
		for i, fn := range fns {
			got := fn()
			want := 2*c.list[i].v + c.root.v
			ck(got == want, fmt.Sprintf("round %d list closure[%d] after GC=%d want=%d seed=%d", rnd, i, got, want, uafSeed))
		}

		delta := base%37 + 5
		got := deferredClosure(c.root, delta)
		gcStress()
		want := c.root.v + delta
		ck(got == want, fmt.Sprintf("round %d deferred closure=%d want=%d seed=%d", rnd, got, want, uafSeed))

		gcStress()
		if c.root == nil || c.root.v != base+1 || c.root.next == nil || c.root.next.v != base+2 {
			ck(false, fmt.Sprintf("round %d ctx root corrupted after closures seed=%d", rnd, uafSeed))
		}
		for i := range c.list {
			if c.list[i] == nil || c.list[i].v != base+10+int64(i)*3 || c.list[i].name != "list" {
				ck(false, fmt.Sprintf("round %d ctx list[%d] corrupted seed=%d", rnd, i, uafSeed))
				break
			}
		}
	}

	finish("l17_closure_defer_combo")
}
