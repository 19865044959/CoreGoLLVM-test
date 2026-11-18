// L14: 方法链 panic traceback。指针接收者方法逐层调用并经过嵌入类型提升方法，
// debug.Stack 必须给出每一层的真实方法符号（含接收者形态）与 main.go:行号；
// panic payload 指针链在后续 GC 与 clobberfree 下保持有效。
package main

import (
	"fmt"
	"os"
	"regexp"
	"runtime"
	"runtime/debug"
	"strings"
	"sync/atomic"
)

type chainPayload struct {
	id   int64
	name string
	next *chainPayload
}

// methodBase 被嵌入，panicNow 是提升方法。
type methodBase struct {
	payload *chainPayload
	owner   string
}

//go:noinline
func (b *methodBase) panicNow() { panic(b.payload) }

type methodWorker struct {
	*methodBase
	tag string
}

//go:noinline
func (w *methodWorker) step1() { w.step2() }

//go:noinline
func (w *methodWorker) step2() { w.step3() }

//go:noinline
func (w *methodWorker) step3() { w.panicNow() }

//go:noinline
func runMethodChain(w *methodWorker) (stack string, ok bool) {
	defer func() {
		if e := recover(); e != nil {
			q, is := e.(*chainPayload)
			ok = is && q == w.payload && q.next != nil && q.next.id == q.id+1
			stack = string(debug.Stack())
		}
	}()
	w.step1()
	return "", false
}

var (
	failed int32
	gcSink [][]byte
	lineRe = regexp.MustCompile(`main\.go:\d+`)
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
		fmt.Printf("FAILED %s (%d failed checks)\n", name, n)
		os.Exit(1)
	}
	fmt.Println("ALL PASS " + name)
}

func main() {
	head := &chainPayload{id: 1, name: "head"}
	head.next = &chainPayload{id: 2, name: "tail"}
	w := &methodWorker{
		methodBase: &methodBase{payload: head, owner: "methods"},
		tag:        "method-chain",
	}
	gcStress()

	stack, ok := runMethodChain(w)
	ck(ok, "recovered payload identity through method chain")
	ck(strings.Contains(stack, "main.(*methodWorker).step1("), "traceback has step1 method symbol")
	ck(strings.Contains(stack, "main.(*methodWorker).step2("), "traceback has step2 method symbol")
	ck(strings.Contains(stack, "main.(*methodWorker).step3("), "traceback has step3 method symbol")
	ck(strings.Contains(stack, "main.(*methodBase).panicNow("), "traceback has promoted method symbol")
	ck(strings.Contains(stack, "main.runMethodChain("), "traceback has recovering function symbol")
	ck(strings.Contains(stack, "goroutine "), "traceback has goroutine header")
	ck(len(lineRe.FindAllString(stack, -1)) >= 5, "traceback has main.go:line for every method frame")

	// 方法链帧的顺序必须由内向外：panicNow -> step3 -> step2 -> step1。
	idxPanic := strings.Index(stack, "main.(*methodBase).panicNow(")
	idx3 := strings.Index(stack, "main.(*methodWorker).step3(")
	idx2 := strings.Index(stack, "main.(*methodWorker).step2(")
	idx1 := strings.Index(stack, "main.(*methodWorker).step1(")
	ck(idxPanic >= 0 && idx3 > idxPanic && idx2 > idx3 && idx1 > idx2, "method frames ordered inner to outer")

	gcStress()
	ck(w.payload != nil && w.payload.id == 1 && w.payload.name == "head", "payload after another GC")
	ck(w.payload.next != nil && w.payload.next.id == 2 && w.payload.next.name == "tail", "payload chain after another GC")
	finish("l14_method_chain_traceback")
}
