// L11: defer × panic 展开 × GC——panic 展开途中 deferred 闭包触发显式 GC，
// 捕获的堆对象与 panic 值必须保持完整；recover 中的 GC 后读取 named result、
// typed nil 与结构体 panic 值，重复 panic 的 defer 顺序必须稳定。
package main

import (
	"fmt"
	"os"
	"runtime"
	"strconv"
	"sync/atomic"
)

type dpNode struct {
	v   int64
	tag string
	p   *dpNode
}

type dpPanic struct {
	v   int64
	tag string
}

type dpStress struct {
	node *dpNode
	ip   *dpPanic
	tag  string
}

var (
	failed     int32
	gcSink     [][]byte
	seed       uint64 = 1
	dpRetained []*dpNode
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

func dpRecoverGC(n int) (msg string, out int64, trace []int) {
	nodes := make([]*dpNode, n)
	for i := range nodes {
		nodes[i] = &dpNode{v: int64(i * 3), tag: "n" + strconv.Itoa(i), p: &dpNode{v: int64(1000 + i)}}
	}
	dpRetained = append(dpRetained, nodes...)

	defer func() {
		r := recover()
		gcStress()
		if p, ok := r.(*dpPanic); ok {
			msg = p.tag
			out = p.v + nodes[n-1].v + nodes[0].v
		}
		trace = append(trace, 99)
	}()
	defer func() {
		trace = append(trace, 1)
		out += 1000
	}()
	defer func() {
		trace = append(trace, 2)
		out += 2000
		gcStress()
		_ = nodes[0].tag
	}()
	panic(&dpPanic{v: 7, tag: "boom"})
}

func dpRepanic() (msg string, trace []int) {
	defer func() {
		r := recover()
		if r != nil {
			gcStress()
			trace = append(trace, 90)
			if s, ok := r.(string); ok {
				msg = s
			}
		}
	}()
	func() {
		defer func() {
			trace = append(trace, 1)
		}()
		defer func() {
			r := recover()
			trace = append(trace, 2)
			if r != nil {
				panic("second")
			}
		}()
		panic("first")
	}()
	return "no", trace
}

func dpTypedNilDefer() (ok bool) {
	var p *dpNode
	var v any = p
	defer func() {
		gcStress()
		q, good := v.(*dpNode)
		ok = v != nil && good && q == nil
	}()
	return true
}

func dpStructPanic() (tag string, v int64) {
	defer func() {
		r := recover()
		gcStress()
		p, ok := r.(dpPanic)
		if ok {
			tag = p.tag
			v = p.v
		}
	}()
	panic(dpPanic{v: 42, tag: "struct"})
}

func dpDeferChainGC(n int) (sum int64, trace []int) {
	nodes := make([]*dpNode, n)
	for i := range nodes {
		nodes[i] = &dpNode{v: int64(i + 1), tag: "d" + strconv.Itoa(i)}
	}
	dpRetained = append(dpRetained, nodes...)

	defer func() {
		trace = append(trace, 10)
		gcStress()
		for _, nd := range nodes {
			sum += nd.v
		}
	}()
	defer func() {
		trace = append(trace, 20)
		sum += nodes[0].v
	}()
	defer func() {
		trace = append(trace, 30)
		sum += nodes[n-1].v
	}()
	return sum, trace
}

func main() {
	seed = loadSeed()
	if seed == 0 {
		seed = 1
	}
	n := 40 + int(seed%41)

	msg, out, trace := dpRecoverGC(n)
	ck(msg == "boom", "panic value after GC type assertion")
	ck(out == 7+int64(3*(n-1)), "recover named result after GC")
	ck(len(trace) == 3 && trace[0] == 2 && trace[1] == 1 && trace[2] == 99, "defer order during panic unwind")

	msg2, trace2 := dpRepanic()
	ck(msg2 == "second", "repanic recover message")
	ck(len(trace2) == 3 && trace2[0] == 2 && trace2[1] == 1 && trace2[2] == 90, "repanic defer order with GC")

	ck(dpTypedNilDefer(), "typed nil captured by deferred closure after GC")

	st, sv := dpStructPanic()
	ck(st == "struct" && sv == 42, "struct panic value after GC")

	sum, dtrace := dpDeferChainGC(n)
	wantSum := int64(n*(n+1)/2) + 1 + int64(n)
	ck(sum == wantSum, "deferred chain sum with GC")
	ck(len(dtrace) == 3 && dtrace[0] == 30 && dtrace[1] == 20 && dtrace[2] == 10, "deferred chain LIFO with GC")

	stress := &dpStress{
		node: &dpNode{v: 77, tag: "kept", p: &dpNode{v: 88, tag: "inner"}},
		ip:   &dpPanic{v: 5, tag: "held"},
		tag:  "stress",
	}
	dpRetained = append(dpRetained, stress.node)
	gcStress()
	ck(stress.node.v == 77 && stress.node.p != nil && stress.node.p.v == 88, "retained panic path nodes after GC")
	ck(stress.ip != nil && stress.ip.tag == "held" && stress.tag == "stress", "retained panic value after GC")

	oneSum, oneTrace := dpDeferChainGC(1)
	ck(oneSum == 3 && len(oneTrace) == 3, "single deferred chain")

	gcStress()
	retOK := true
	for _, nd := range dpRetained {
		if nd == nil || nd.tag == "" {
			retOK = false
			break
		}
		if nd.p != nil && nd.p.v < 0 {
			retOK = false
			break
		}
	}
	ck(retOK, "retained deferred-path nodes after final GC")

	finish("l11_defer_panic_gc")
}
