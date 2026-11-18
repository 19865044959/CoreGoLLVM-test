// L11: nil 函数值与接口中的 nil 检查——nil func 比较/字段/map/slice、
// typed nil 装入接口、defer nil func 的 panic/recover，以及 GC 后
// 非 nil 函数值仍可调用。
package main

import (
	"fmt"
	"os"
	"runtime"
	"strconv"
	"sync/atomic"
)

type nfHolder struct {
	cb  func(int) int
	tag string
}

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

func nfDouble(x int) int { return x * 2 }

func nfApply(f func(int) int, x int) int {
	if f == nil {
		return -1
	}
	return f(x)
}

func nfReturnNil() func(int) int { return nil }

func nfCallRecover(f func()) (out string) {
	defer func() {
		if r := recover(); r != nil {
			out = fmt.Sprint("recovered:", r)
		}
	}()
	f()
	return "ok"
}

func nfDeferNil() (out string) {
	defer func() {
		if r := recover(); r != nil {
			out = "deferred-nil-recovered"
		}
	}()
	var f func()
	defer f()
	return "normal"
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

func nfCallSlice(fs []func(int) int, x int) (int64, int64) {
	var sum int64
	var nilCount int64
	for _, f := range fs {
		if f == nil {
			nilCount++
			continue
		}
		sum += int64(f(x))
	}
	return sum, nilCount
}

func main() {
	seed = loadSeed()
	if seed == 0 {
		seed = 1
	}

	var f func(int) int
	ck(f == nil, "zero func value is nil")
	ck(nfApply(f, 5) == -1, "nil func checked before call")

	f = nfDouble
	gcStress()
	ck(f != nil && f(21) == 42, "func value after GC")
	ck(nfApply(f, 8) == 16, "non-nil func through helper after GC")

	h := &nfHolder{tag: "holder"}
	ck(h.cb == nil, "nil func struct field")
	h.cb = nfDouble
	ck(h.cb(11) == 22, "func struct field call")
	gcStress()
	ck(h.cb != nil && h.cb(11) == 22 && h.tag == "holder", "func struct field after GC")

	m := map[string]func() int{
		"ok": func() int { return 7 },
	}
	missing, ok := m["missing"]
	ck(!ok && missing == nil, "missing map func is nil")
	ck(m["ok"]() == 7, "map func call")

	fs := []func(int) int{nfDouble, nil, nfDouble}
	sum, nilCount := nfCallSlice(fs, 5)
	ck(sum == 20 && nilCount == 1, "slice func nil count and sum")
	gcStress()
	sum, nilCount = nfCallSlice(fs, 6)
	ck(sum == 24 && nilCount == 1, "slice func after GC")

	out := nfCallRecover(func() { fs[1](3) })
	ck(contains(out, "nil pointer dereference"), "calling nil func panics")

	var typedNilFunc func()
	var a any = typedNilFunc
	ck(a != nil, "typed nil func in interface is not nil")
	got, tok := a.(func())
	ck(tok && got == nil, "typed nil func assertion")
	out = nfCallRecover(func() { a.(func())() })
	ck(contains(out, "nil pointer dereference"), "calling typed nil func via interface panics")

	var empty any
	ck(empty == nil, "empty interface is nil")

	r := nfReturnNil()
	ck(r == nil, "function returning nil func")
	var ra any = nfReturnNil()
	ck(ra != nil, "nil func boxed in interface is not nil")
	rg, rok := ra.(func(int) int)
	ck(rok && rg == nil, "returned nil func assertion")

	ck(nfCallRecover(func() { panic("boom") }) == "recovered:boom", "recover live panic")
	ck(nfCallRecover(func() {}) == "ok", "recover normal path")
	ck(nfDeferNil() == "deferred-nil-recovered", "defer nil func recovered")

	h.cb = nil
	ck(h.cb == nil, "func field reset to nil")
	gcStress()
	ck(h.cb == nil && h.tag == "holder", "nil func field after GC")

	finish("l11_nil_func_iface")
}
