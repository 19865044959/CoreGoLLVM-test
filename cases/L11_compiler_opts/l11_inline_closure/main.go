// L11: 内联闭包——立即调用闭包、逃逸闭包、循环内闭包、递归闭包与
// //go:noinline 包装必须给出相同语义；GC 后闭包捕获的变量仍有效。
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

func finish(name string) {
	if n := atomic.LoadInt32(&failed); n != 0 {
		fmt.Printf("FAILED %s (%d failed checks, seed=%d)\n", name, n, seed)
		os.Exit(1)
	}
	fmt.Println("ALL PASS " + name)
}

type icBox struct {
	v   int64
	tag string
}

// makeCounter 返回捕获自身计数器的闭包，闭包环境必须逃逸到堆。
func makeCounter(base int64) func(int64) int64 {
	total := base
	return func(d int64) int64 {
		total += d
		return total
	}
}

//go:noinline
func applyClosure(f func(int64) int64, x int64) int64 { return f(x) }

// captureBox 的闭包捕获局部对象指针，返回的两个引用必须指向同一对象。
func captureBox(base int64) (func(int64) int64, *icBox) {
	b := &icBox{v: base, tag: "captured"}
	f := func(d int64) int64 {
		b.v += d
		return b.v + int64(len(b.tag))
	}
	return f, b
}

// loopClosures 每轮显式新建 v，闭包捕获的必须是各轮独立副本。
func loopClosures(n int) ([]func() int64, int64) {
	fs := make([]func() int64, n)
	var want int64
	for i := 0; i < n; i++ {
		v := int64(i*7 + 1)
		fs[i] = func() int64 { return v*v + v }
		want += v*v + v
	}
	return fs, want
}

func immediate(n int) int64 {
	var sum int64
	for i := 0; i < n; i++ {
		x := int64(i)
		func() {
			sum += x*3 + 1
		}()
	}
	return sum
}

func immediateFormula(n int) int64 {
	nn := int64(n)
	return 3*(nn-1)*nn/2 + nn
}

func fibClosure(n int) []int64 {
	out := make([]int64, n)
	var fib func(int) int64
	fib = func(k int) int64 {
		if k < 2 {
			return int64(k)
		}
		return fib(k-1) + fib(k-2)
	}
	for i := range out {
		out[i] = fib(i)
	}
	return out
}

func fibIter(n int) []int64 {
	out := make([]int64, n)
	a, b := int64(0), int64(1)
	for i := 0; i < n; i++ {
		out[i] = a
		a, b = b, a+b
	}
	return out
}

func nestedClosure(k, b, c int64) (int64, int64) {
	a := k
	outer := func(x int64) func(int64) int64 {
		a += x
		return func(y int64) int64 {
			a += y
			return a
		}
	}
	inner := outer(b)
	first := inner(c)
	second := inner(0)
	return first, second
}

func mutateCaptured() (int64, int64) {
	x := int64(10)
	inc := func() { x += 5 }
	get := func() int64 { return x }
	inc()
	inc()
	return x, get()
}

func deferredClosure(n int64) (r int64) {
	add := func(k int64) { r += k }
	defer func() { r *= 2 }()
	for i := int64(0); i < n; i++ {
		add(i)
	}
	return r
}

func closureArith(x int64) int64 {
	f := func(y int64) int64 {
		return (y*7 + 13) ^ (y >> 2)
	}
	return f(x)
}

//go:noinline
func closureArithNoInline(x int64) int64 {
	f := func(y int64) int64 {
		return (y*7 + 13) ^ (y >> 2)
	}
	return applyClosure(f, x)
}

func arithRef(x int64) int64 {
	return (x*7 + 13) ^ (x >> 2)
}

func main() {
	seed = loadSeed()
	n := 32 + int(seed%16)

	// 立即调用闭包与闭式公式。
	ck(immediate(n) == immediateFormula(n), "immediate closure sum mismatch")

	// 循环内闭包各自捕获独立副本。
	fs, want := loopClosures(n)
	gcStress()
	var got int64
	for _, f := range fs {
		got += f()
	}
	ck(got == want, "loop closure sum mismatch after GC")

	// 逃逸计数器闭包。
	base := int64(100 + seed%13)
	counter := makeCounter(base)
	var last int64
	for i := 0; i < n; i++ {
		last = counter(int64(i + 1))
	}
	ck(last == base+int64(n*(n+1)/2), "escaping counter closure mismatch")

	// 内联候选与 noinline 包装的算术闭包。
	xs := []int64{0, 1, -1, 7, -9, 1234567, -1234567, int64(seed % 1000)}
	for _, x := range xs {
		ck(closureArith(x) == arithRef(x), "inline closure arithmetic mismatch")
		ck(closureArithNoInline(x) == arithRef(x), "noinline closure arithmetic mismatch")
	}

	// 递归闭包与迭代版本。
	fibs := fibClosure(24)
	gcStress()
	iters := fibIter(24)
	for i := range fibs {
		ck(fibs[i] == iters[i], "recursive closure fib mismatch")
	}

	// 嵌套闭包共享捕获变量。
	f1, f2 := nestedClosure(base, 7, 11)
	ck(f1 == base+18 && f2 == base+18, "nested closure shared capture mismatch")

	// 多个闭包修改同一捕获变量。
	mv, mg := mutateCaptured()
	ck(mv == 20 && mg == 20, "mutate captured variable mismatch")

	// defer 中的闭包修改命名返回值。
	dn := int64(n%9 + 1)
	ck(deferredClosure(dn) == dn*(dn-1), "deferred closure result mismatch")

	// 闭包捕获的堆对象跨 GC。
	bf, box := captureBox(5)
	gcStress()
	ck(box.tag == "captured" && box.v == 5, "captured box corrupted by GC")
	ck(bf(3) == 16 && box.v == 8, "closure over heap box mismatch after GC")

	// 闭包作为参数经 noinline 包装调用。
	add := func(x int64) int64 { return x + base }
	ck(applyClosure(add, 9) == base+9, "closure argument through noinline mismatch")

	// 一批闭包保留到最终 GC 后再调用。
	kept := make([]func() int64, 0, n)
	for i := 0; i < n; i++ {
		k := int64(i*3 + 1)
		kept = append(kept, func() int64 { return k*k + 1 })
	}
	gcStress()
	for i, f := range kept {
		k := int64(i*3 + 1)
		ck(f() == k*k+1, "kept closure result corrupted after GC")
	}

	finish("l11_inline_closure")
}
