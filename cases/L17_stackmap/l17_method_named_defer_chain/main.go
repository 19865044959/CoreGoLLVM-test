// L17: 方法命名返回 struct + 多个 defer + panic/recover。defer 注册顺序 LIFO，
// 每个 defer 内 GC 并读取命名返回值、receiver 与 defer 时求值的实参。
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

type result struct {
	head      *item
	hook      *item
	list      []*item
	tag       string
	total     int64
	recovered bool
}

type proc struct {
	keep *item
	ops  []string
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
func (p *proc) mark() {
	gcStress()
	p.ops = append(p.ops, "mark")
}

//go:noinline
func (p *proc) stamp(out *result, base int64) {
	gcStress()
	p.ops = append(p.ops, "stamp")
	out.total += 100
	ck(out.head != nil && out.head.v == base+1 && out.head.next != nil && out.head.next.v == base+2,
		fmt.Sprintf("stamp: named return head corrupted seed=%d", uafSeed))
	ck(p.keep != nil && p.keep.v == base+900 && p.keep.name == "keep",
		fmt.Sprintf("stamp: receiver keep corrupted seed=%d", uafSeed))
	out.hook = &item{v: base + 500, name: "stamp"}
}

// run 注册 5 个循环 defer、1 个方法值 defer、1 个闭包 defer 和 1 个 recover defer，
// 然后 panic；所有 defer 最后都改写同一个命名返回 struct。
//
//go:noinline
func (p *proc) run(base int64) (out result) {
	out = result{head: &item{v: base + 1, name: "head"}, tag: "init", total: 1}
	p.ops = append(p.ops, "body")

	for i := 0; i < 5; i++ {
		defer func(i int) {
			gcStress()
			out.total += int64(i + 1)
			p.ops = append(p.ops, "loop")
			if out.head == nil || out.head.v != base+1 || out.head.next == nil || out.head.next.v != base+2 {
				ck(false, fmt.Sprintf("loop defer %d: named return head corrupted seed=%d", i, uafSeed))
			}
			out.list = append(out.list, &item{v: base + int64(20+i), name: "defer"})
		}(i)
	}

	defer p.mark()
	defer p.stamp(&out, base)
	defer func() {
		if r := recover(); r != nil {
			out.tag = "recovered"
			out.recovered = true
			gcStress()
			ck(out.head != nil && out.head.v == base+1,
				fmt.Sprintf("recover defer: head corrupted seed=%d", uafSeed))
			p.ops = append(p.ops, "recover")
		}
	}()

	gcStress()
	out.head.next = &item{v: base + 2, name: "tail"}
	panic("chain")
}

func verify(out result, base int64, rnd int) {
	ck(out.recovered, fmt.Sprintf("round %d: panic not recovered seed=%d", rnd, uafSeed))
	ck(out.tag == "recovered", fmt.Sprintf("round %d: tag=%q seed=%d", rnd, out.tag, uafSeed))
	ck(out.total == 116, fmt.Sprintf("round %d: total=%d want=116 seed=%d", rnd, out.total, uafSeed))
	ck(out.head != nil && out.head.v == base+1 && out.head.name == "head" &&
		out.head.next != nil && out.head.next.v == base+2,
		fmt.Sprintf("round %d: named return head corrupted seed=%d", rnd, uafSeed))
	ck(out.hook != nil && out.hook.v == base+500,
		fmt.Sprintf("round %d: stamp hook corrupted seed=%d", rnd, uafSeed))
	ck(len(out.list) == 5, fmt.Sprintf("round %d: list len=%d seed=%d", rnd, len(out.list), uafSeed))
	for i := 0; i < len(out.list) && i < 5; i++ {
		// defer LIFO：i=4 的闭包最先追加，因此 list 按 24,23,22,21,20 排列。
		if out.list[i] == nil || out.list[i].v != base+int64(24-i) || out.list[i].name != "defer" {
			ck(false, fmt.Sprintf("round %d: list[%d] corrupted seed=%d", rnd, i, uafSeed))
		}
	}
}

func main() {
	uafSeed = seedFromEnv()

	for rnd := 0; rnd < 10; rnd++ {
		base := int64(rnd*1000) + uafSeed%23
		p := &proc{keep: &item{v: base + 900, name: "keep"}}
		out := p.run(base)

		gcStress()
		verify(out, base, rnd)
		ck(p.keep != nil && p.keep.v == base+900 && p.keep.name == "keep",
			fmt.Sprintf("round %d: proc keep corrupted seed=%d", rnd, uafSeed))
		ck(len(p.ops) == 9, fmt.Sprintf("round %d: ops len=%d seed=%d", rnd, len(p.ops), uafSeed))
		if len(p.ops) == 9 {
			ck(p.ops[0] == "body" && p.ops[1] == "recover" && p.ops[2] == "stamp" && p.ops[3] == "mark",
				fmt.Sprintf("round %d: defer order wrong: %v seed=%d", rnd, p.ops, uafSeed))
			for i := 4; i < 9; i++ {
				ck(p.ops[i] == "loop", fmt.Sprintf("round %d: ops[%d]=%q seed=%d", rnd, i, p.ops[i], uafSeed))
			}
		}
		gcStress()
		verify(out, base, rnd)
	}

	finish("l17_method_named_defer_chain")
}
