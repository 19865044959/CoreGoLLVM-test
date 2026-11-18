// L01: 泛型 + 接口 + 闭包组合——泛型接口用类型参数做方法签名，闭包携带
// 状态与指针载荷，中间件链经接口派发/方法值调用；GC 后状态与载荷仍正确。
package main

import (
	"fmt"
	"os"
	"runtime"
	"strconv"
	"sync"
	"sync/atomic"
)

type record struct {
	ID   int64
	Path []string
	Hits int64
}

type Mapper[T any] func(T) T

type Transformer[T any] interface {
	Apply(T) T
	Label() string
}

type Chain[T any] struct {
	label string
	steps []Mapper[T]
	calls int64
}

func NewChain[T any](label string) *Chain[T] {
	return &Chain[T]{label: label}
}

func (c *Chain[T]) Add(m Mapper[T]) *Chain[T] {
	c.steps = append(c.steps, m)
	return c
}

func (c *Chain[T]) Apply(v T) T {
	c.calls++
	for _, f := range c.steps {
		if f != nil {
			v = f(v)
		}
	}
	return v
}

func (c *Chain[T]) Label() string { return c.label }

func (c *Chain[T]) Calls() int64 { return c.calls }

func compose[T any](fs ...Mapper[T]) Mapper[T] {
	return func(v T) T {
		for _, f := range fs {
			if f != nil {
				v = f(v)
			}
		}
		return v
	}
}

// runAll 只在泛型函数内把 T 用于接口类型，检验实例化后的方法集派发。
func runAll[T any](ts []Transformer[T], v T) (T, []string) {
	labels := make([]string, 0, len(ts))
	for _, t := range ts {
		v = t.Apply(v)
		labels = append(labels, t.Label())
	}
	return v, labels
}

var _ Transformer[int64] = (*Chain[int64])(nil)
var _ Transformer[*record] = (*Chain[*record])(nil)

var (
	failed  int32
	gcSink  [][]byte
	uafSeed int64 = 1
)

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
		gcSink = append(gcSink[:0], b)
	}
	gcSink = gcSink[:0]
	runtime.GC()
	runtime.GC()
}

func finish(name string) {
	if n := atomic.LoadInt32(&failed); n != 0 {
		fmt.Printf("FAILED %s seed=%d (%d failed checks)\n", name, uafSeed, n)
		os.Exit(1)
	}
	fmt.Println("ALL PASS " + name)
}

func envSeed() int64 {
	s := os.Getenv("UAF_SEED")
	if s == "" {
		return 1
	}
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil || n <= 0 {
		return 1
	}
	return n
}

func main() {
	uafSeed = envSeed()

	// 闭包状态 + 链式顺序：seen 由两个闭包共享。
	ch := NewChain[int64]("int")
	var seen int64
	ch.Add(func(v int64) int64 { seen++; return v + 1 })
	ch.Add(nil) // nil 步骤必须跳过
	ch.Add(func(v int64) int64 { seen *= 2; return v * 3 })
	got := ch.Apply(7)
	ck(got == 24, "closure chain value mismatch")
	ck(seen == 2, "closure captured state mismatch")
	ck(ch.Calls() == 1, "chain call counter mismatch")
	got = ch.Apply(2)
	ck(got == 9, "second apply value mismatch")
	ck(seen == 6 && ch.Calls() == 2, "closure state after second apply")

	ck(compose[int64](func(v int64) int64 { return v + 1 }, nil,
		func(v int64) int64 { return v * 3 })(5) == 18, "compose with nil step")
	ck(compose[int64]()(9) == 9, "empty compose must be identity")

	// 接口切片派发 + 标签顺序。
	ts := []Transformer[int64]{
		NewChain[int64]("a").Add(func(v int64) int64 { return v + 2 }),
		NewChain[int64]("b").Add(func(v int64) int64 { return v * 4 }),
	}
	res, labels := runAll(ts, 5)
	ck(res == 28, "generic interface dispatch value mismatch")
	ck(len(labels) == 2 && labels[0] == "a" && labels[1] == "b", "interface dispatch order mismatch")

	// 方法值调用与直接调用一致。
	mv := ch.Apply
	ck(mv(4) == ch.Apply(4), "method value disagrees with direct call")

	// 指针载荷 + 闭包捕获：GC 后 record 内容必须保留。
	rec := &record{ID: 3, Path: []string{"start"}}
	base := int64(10)
	rch := NewChain[*record]("rec")
	rch.Add(func(r *record) *record {
		r.Path = append(r.Path, "a")
		r.Hits += base
		return r
	})
	seedDelta := uafSeed % 5
	rch.Add(func(r *record) *record {
		r.Path = append(r.Path, "b")
		r.ID += seedDelta
		return r
	})
	rch.Add(nil)
	out := rch.Apply(rec)
	ck(out == rec, "pointer payload identity lost through chain")
	ck(len(rec.Path) == 3 && rec.Path[0] == "start" && rec.Path[1] == "a" && rec.Path[2] == "b",
		"closure path mutation mismatch")
	ck(rec.Hits == 10, "closure captured counter mismatch")
	ck(rec.ID == 3+seedDelta, "closure captured delta mismatch")

	// 让接口与闭包持有 record，GC 后再经接口调用。
	var tr Transformer[*record] = rch
	gcStress()
	out = tr.Apply(rec)
	ck(out == rec && len(rec.Path) == 5, "interface-held closure lost pointer payload after GC")
	ck(rec.Hits == 20, "closure counter corrupted after GC")
	ck(rec.ID == 3+2*seedDelta, "payload id corrupted after GC")

	// typed-nil 与 nil 接口的边界。
	var nilChain *Chain[int64]
	var typedNil Transformer[int64] = nilChain
	ck(typedNil != nil, "typed nil lost through generic interface")
	var nilTr Transformer[int64]
	ck(nilTr == nil, "zero generic interface must be nil")

	// 并发：每个 worker 的闭包状态互相隔离，结果可复算。
	workers := 4
	n := 60 + int(uafSeed%30)
	results := make([]int64, workers)
	counters := make([]int64, workers)
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			var counter int64
			local := NewChain[int64](fmt.Sprintf("w%d", w))
			local.Add(func(v int64) int64 {
				counter++
				return v + int64(w)
			})
			local.Add(nil)
			var v int64
			for i := 0; i < n; i++ {
				v = local.Apply(v)
			}
			results[w] = v
			counters[w] = counter
		}(w)
	}
	wg.Wait()
	gcStress()

	for w := 0; w < workers; w++ {
		ck(results[w] == int64(n*w), fmt.Sprintf("worker %d closure result mismatch", w))
		ck(counters[w] == int64(n), fmt.Sprintf("worker %d closure counter mismatch", w))
	}

	finish("l01_generic_iface_closure")
}
