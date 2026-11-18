// L11: open-coded defer——参数求值时机、LIFO 顺序、命名返回值修改、
// defer 中的 recover、循环 defer 与 defer 内 GC 的语义必须与规范一致。
package main

import (
	"fmt"
	"os"
	"runtime"
	"strconv"
	"sync/atomic"
)

type dcNode struct {
	v   int64
	tag string
}

func (n *dcNode) bump() { n.v++ }

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

func deferOrder() (r int64) {
	defer func() { r++ }()
	defer func() { r *= 2 }()
	r = 5
	return
}

func deferArgs() (out string) {
	x := int64(1)
	defer func(v int64) { out += "a" + strconv.FormatInt(v, 10) }(x)
	x = 9
	defer func() { out += "b" + strconv.FormatInt(x, 10) }()
	return
}

func eightOpen() (s string) {
	defer func() { s += "0" }()
	defer func() { s += "1" }()
	defer func() { s += "2" }()
	defer func() { s += "3" }()
	defer func() { s += "4" }()
	defer func() { s += "5" }()
	defer func() { s += "6" }()
	defer func() { s += "7" }()
	return
}

func recoverDefer() (out string) {
	defer func() {
		if r := recover(); r != nil {
			out = fmt.Sprint("recovered:", r)
		}
	}()
	panic("boom")
}

func nestedRecover() (out string) {
	func() {
		defer func() { out = fmt.Sprint("inner:", recover()) }()
		panic("x")
	}()
	return
}

func multiNamed() (a, b int64) {
	defer func() { a += 10 }()
	defer func() { b += 20 }()
	a, b = 1, 2
	return
}

func conditionalDefer(flag bool) (r int64) {
	if flag {
		defer func() { r += 100 }()
	}
	defer func() { r += 1 }()
	r = 5
	return
}

func multiReturn(flag bool) (r int64) {
	defer func() { r *= 2 }()
	if flag {
		r = 10
		return
	}
	r = 20
	return
}

func deferMethod() int64 {
	n := &dcNode{v: 10}
	defer n.bump()
	n.v += 5
	return n.v
}

func deferGC() (ok bool) {
	n := &dcNode{v: 77, tag: "kept"}
	defer func() {
		runtime.GC()
		ok = n.v == 77 && n.tag == "kept"
	}()
	gcStress()
	return true
}

func counterDefers(n int) (count int, order []string) {
	for i := 0; i < n; i++ {
		i := i
		defer func() {
			count++
			order = append(order, strconv.Itoa(i))
		}()
	}
	return
}

func main() {
	seed = loadSeed()
	if seed == 0 {
		seed = 1
	}

	ck(deferOrder() == 11, "LIFO order and named result")
	ck(deferArgs() == "b9a1", "defer argument evaluation time")
	ck(eightOpen() == "76543210", "eight open-coded defers")
	ck(recoverDefer() == "recovered:boom", "recover in defer")
	ck(nestedRecover() == "inner:x", "nested recover in deferred closure")

	a, b := multiNamed()
	ck(a == 11 && b == 22, "multiple named results in defers")
	ck(conditionalDefer(true) == 106, "conditional defer taken")
	ck(conditionalDefer(false) == 6, "conditional defer skipped")
	ck(multiReturn(true) == 20, "early return with defer")
	ck(multiReturn(false) == 40, "normal return with defer")
	ck(deferMethod() == 15, "deferred method call after return value")
	ck(deferGC(), "defer closure sees object after GC")

	n := 5 + int(seed%8)
	count, order := counterDefers(n)
	ck(count == n && len(order) == n, "loop defers all executed")
	orderOK := true
	for i := 0; i < n; i++ {
		if order[i] != strconv.Itoa(n-1-i) {
			orderOK = false
		}
	}
	ck(orderOK, "loop defers LIFO order")

	gcStress()
	ck(deferOrder() == 11, "open-coded defer after GC")
	ck(deferMethod() == 15, "deferred method after GC")

	finish("l11_open_coded_defer")
}
