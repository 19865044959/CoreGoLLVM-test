// L05: 闭包捕获堆分配的 *cnode/slice，闭包本身存入切片长期持有；
// 多轮调用、垃圾分配与 GC 后校验捕获变量、返回值和指针字段。
package main

import (
	"fmt"
	"os"
	"runtime"
	"strconv"
	"sync/atomic"
)

const (
	closureCount = 300
	callPasses   = 4
)

var (
	failed int32
	gcSink [][]byte
)

type cnode struct {
	v    int64
	name string
}

type closer struct {
	call func(int64) int64
	get  func() (int64, int, *cnode)
	base int64
	name string
}

func ck(ok bool, msg string) {
	if !ok {
		atomic.AddInt32(&failed, 1)
		fmt.Println("FAIL:", msg)
	}
}

func ckf(ok bool, format string, args ...interface{}) {
	if !ok {
		atomic.AddInt32(&failed, 1)
		fmt.Printf("FAIL: "+format+"\n", args...)
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

func finish(name string, seed uint64) {
	if n := atomic.LoadInt32(&failed); n != 0 {
		fmt.Printf("FAILED %s (%d failed checks, seed=%d)\n", name, n, seed)
		os.Exit(1)
	}
	fmt.Println("ALL PASS " + name)
}

// makeCloser 返回三个逃逸到堆上的闭包：调用闭包、观察闭包，以及被捕获的节点。
// 闭包同时捕获指针与 slice，验证逃逸对象的栈位图/堆扫描。
//
//go:noinline
func makeCloser(base int64, name string, seed uint64) (closer, *cnode) {
	n := &cnode{v: base, name: name}
	calls := 0
	hist := make([]byte, 0, 64)
	call := func(d int64) int64 {
		n.v += d
		hist = append(hist, byte((n.v+int64(calls)+int64(seed))%251))
		calls++
		return n.v + int64(len(hist))
	}
	get := func() (int64, int, *cnode) {
		return n.v, len(hist), n
	}
	return closer{call: call, get: get, base: base, name: name}, n
}

func main() {
	seed := loadSeed()

	closures := make([]closer, 0, closureCount)
	expectedV := make([]int64, 0, closureCount)
	expectedN := make([]int, 0, closureCount)
	nodes := make([]*cnode, 0, closureCount)

	for i := 0; i < closureCount; i++ {
		base := int64(i)*1000 + int64(seed%97) - 17
		name := fmt.Sprintf("cl-%04d", i)
		c, n := makeCloser(base, name, seed)
		closures = append(closures, c)
		expectedV = append(expectedV, base)
		expectedN = append(expectedN, 0)
		nodes = append(nodes, n)
		if n.name != name || n.v != base {
			ckf(false, "closure=%d captured node wrong at creation", i)
		}
		if i%50 == 49 {
			gcStress()
		}
	}

	for pass := 0; pass < callPasses; pass++ {
		for i := range closures {
			d := int64((i*7+pass*13+int(seed))%19) - 9
			expectedV[i] += d
			expectedN[i]++
			got := closures[i].call(d)
			want := expectedV[i] + int64(expectedN[i])
			if got != want {
				ckf(false, "closure=%d pass=%d ret=%d want=%d", i, pass, got, want)
			}
		}
		if pass%2 == 1 {
			gcStress()
		}
	}

	gcStress()
	for i := range closures {
		v, n, p := closures[i].get()
		if v != expectedV[i] || n != expectedN[i] {
			ckf(false, "closure=%d captured v/len got=(%d,%d) want=(%d,%d)", i, v, n, expectedV[i], expectedN[i])
		}
		if p == nil || p != nodes[i] || p.v != expectedV[i] || p.name != fmt.Sprintf("cl-%04d", i) {
			ckf(false, "closure=%d captured pointer corrupted after GC", i)
		}
	}

	// 循环内创建的闭包在循环结束后调用，必须保留每次迭代的捕获值。
	var late []func() int
	for i := 0; i < 16; i++ {
		i := i
		v := &cnode{v: int64(i) * 3, name: fmt.Sprintf("late-%02d", i)}
		late = append(late, func() int { return int(v.v) + len(v.name) })
	}
	gcStress()
	for i, f := range late {
		if got, want := f(), i*3+len(fmt.Sprintf("late-%02d", i)); got != want {
			ckf(false, "late closure=%d got=%d want=%d", i, got, want)
		}
	}

	// 闭包自身被丢弃后，新闭包不得复用旧捕获状态。
	closures = nil
	gcStress()
	fresh, node := makeCloser(42, "fresh", seed)
	if fresh.call(8) != 50+1 || node.v != 50 || node.name != "fresh" {
		ck(false, "fresh closure state after old closure drop")
	}
	runtime.KeepAlive(late)

	finish("l05_closure_escape_alloc", seed)
}
