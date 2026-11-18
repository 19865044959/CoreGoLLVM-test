// L14: 多重 recover 语义。同一函数注册两个 defer：内层 recover 吃掉 panic，
// 外层 recover 必须拿到 nil；两层抓到的栈都带真实闭包符号与 main.go:行号。
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

type mrPayload struct {
	id   int64
	name string
	next *mrPayload
}

type mrResult struct {
	inner          any
	innerOK        bool
	innerStack     string
	outerRecover   any
	outerStack     string
	wrapperRecover any
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

//go:noinline
func mrLeaf(p *mrPayload) { panic(p) }

//go:noinline
func mrMid(p *mrPayload) { mrLeaf(p) }

// mrMulti 注册两个 defer：func2 先执行并 recover 原始 panic，func1 后执行，
// 此时 recover() 必须返回 nil。两个 defer 都抓栈用于校验符号。
//
//go:noinline
func mrMulti(p *mrPayload, out *mrResult) {
	defer func() {
		out.outerRecover = recover()
		out.outerStack = string(debug.Stack())
	}()
	defer func() {
		if e := recover(); e != nil {
			q, is := e.(*mrPayload)
			out.inner = e
			out.innerOK = is && q == p && q.next != nil && q.next.id == p.id+1
			out.innerStack = string(debug.Stack())
		}
	}()
	mrMid(p)
}

//go:noinline
func mrWrapper(p *mrPayload, out *mrResult) {
	defer func() { out.wrapperRecover = recover() }()
	mrMulti(p, out)
}

func main() {
	p := &mrPayload{id: 51, name: "multi-recover"}
	p.next = &mrPayload{id: 52, name: "next"}
	gcStress()

	out := &mrResult{}
	mrWrapper(p, out)

	ck(out.innerOK, "inner recover sees original panic value")
	if q, is := out.inner.(*mrPayload); is {
		ck(q == p && q.id == 51, "inner recovered payload identity")
	} else {
		ck(false, "inner recovered value is *mrPayload")
	}
	ck(out.outerRecover == nil, "outer recover returns nil after inner recovery")
	ck(out.wrapperRecover == nil, "caller recover returns nil after recovery")

	ck(strings.Contains(out.innerStack, "main.mrMulti.func2("), "inner stack has recovering closure symbol")
	ck(strings.Contains(out.innerStack, "main.mrMulti("), "inner stack has multi recover function")
	ck(strings.Contains(out.innerStack, "main.mrMid("), "inner stack keeps mid frame")
	ck(strings.Contains(out.innerStack, "main.mrLeaf("), "inner stack keeps leaf frame")
	ck(lineRe.MatchString(out.innerStack), "inner stack has main.go:line")

	ck(strings.Contains(out.outerStack, "main.mrMulti.func1("), "outer stack has last closure symbol")
	ck(strings.Contains(out.outerStack, "main.mrMulti("), "outer stack has multi recover function")
	ck(lineRe.MatchString(out.outerStack), "outer stack has main.go:line")

	gcStress()
	ck(p.id == 51 && p.name == "multi-recover" && p.next != nil && p.next.id == 52, "payload after GC")
	finish("l14_multi_recover")
}
