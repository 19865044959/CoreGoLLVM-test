// L14: 函数指针帧（接口/命名类型/运行期重绑定）。函数值以命名函数类型存入
// any、map[string]any 后经类型断言间接调用；运行期重绑定函数变量；panic 的
// traceback 必须落到真实目标函数符号，而不是接口断言或包装层。
package main

import (
	"fmt"
	"os"
	"reflect"
	"regexp"
	"runtime"
	"runtime/debug"
	"strings"
	"sync/atomic"
)

type fvPayload struct {
	id   int64
	name string
	next *fvPayload
}

type fvFunc func(*fvPayload)

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
func fvLeafAlpha(p *fvPayload) { panic(p) }

//go:noinline
func fvLeafBeta(p *fvPayload) { panic(p) }

//go:noinline
func fvRun(f func(*fvPayload), p *fvPayload) (stack string, ok bool) {
	defer func() {
		if e := recover(); e != nil {
			q, is := e.(*fvPayload)
			ok = is && q == p
			stack = string(debug.Stack())
		}
	}()
	f(p)
	return "", false
}

//go:noinline
func fvRunAny(v any, p *fvPayload) (stack string, ok bool) {
	defer func() {
		if e := recover(); e != nil {
			q, is := e.(*fvPayload)
			ok = is && q == p
			stack = string(debug.Stack())
		}
	}()
	v.(fvFunc)(p)
	return "", false
}

//go:noinline
func fvRunMap(m map[string]any, key string, p *fvPayload) (stack string, ok bool) {
	defer func() {
		if e := recover(); e != nil {
			q, is := e.(*fvPayload)
			ok = is && q == p
			stack = string(debug.Stack())
		}
	}()
	m[key].(fvFunc)(p)
	return "", false
}

func fvName(v any) string {
	rv := reflect.ValueOf(v)
	if rv.Kind() != reflect.Func {
		return ""
	}
	return runtime.FuncForPC(rv.Pointer()).Name()
}

func main() {
	p := &fvPayload{id: 131, name: "func-interface"}
	p.next = &fvPayload{id: 132, name: "next"}
	gcStress()

	// 1) 命名函数类型存入 any，类型断言后间接调用。
	var boxed any = fvFunc(fvLeafAlpha)
	ck(reflect.ValueOf(boxed).Kind() == reflect.Func, "boxed named func type keeps func kind")
	ck(fvName(boxed) == "main.fvLeafAlpha", "func in interface resolves to real target symbol")
	stack, ok := fvRunAny(boxed, p)
	ck(ok, "interface-held func payload identity")
	ck(strings.Contains(stack, "main.fvLeafAlpha("), "interface dispatch traceback has real target symbol")
	ck(strings.Contains(stack, "main.fvRunAny("), "interface dispatch traceback has dispatcher symbol")
	ck(strings.Contains(stack, "goroutine "), "interface dispatch traceback has goroutine header")
	ck(lineRe.MatchString(stack), "interface dispatch traceback has main.go:line")

	// 2) 函数表放在 map[string]any 中，按键取出后调用。
	table := map[string]any{
		"alpha": fvFunc(fvLeafAlpha),
		"beta":  fvFunc(fvLeafBeta),
	}
	ck(fvName(table["beta"]) == "main.fvLeafBeta", "map func entry resolves to real symbol")
	mapStack, mapOK := fvRunMap(table, "beta", p)
	ck(mapOK, "map func payload identity")
	ck(strings.Contains(mapStack, "main.fvLeafBeta("), "map dispatch traceback has indexed target symbol")
	ck(strings.Contains(mapStack, "main.fvRunMap("), "map dispatch traceback has map dispatcher symbol")
	ck(!strings.Contains(mapStack, "main.fvLeafAlpha("), "map dispatch selects only the indexed target")
	ck(lineRe.MatchString(mapStack), "map dispatch traceback has main.go:line")

	// 3) 运行期重绑定函数变量：调用必须落到最新目标。
	fn := fvFunc(fvLeafAlpha)
	ck(fvName(fn) == "main.fvLeafAlpha", "func var initial target symbol")
	fn = fvLeafBeta
	ck(fvName(fn) == "main.fvLeafBeta", "func var rebound target symbol")
	reboundStack, reboundOK := fvRun(fn, p)
	ck(reboundOK, "rebound func payload identity")
	ck(strings.Contains(reboundStack, "main.fvLeafBeta("), "rebound traceback has latest target symbol")
	ck(!strings.Contains(reboundStack, "main.fvLeafAlpha("), "rebound traceback has no stale target symbol")

	// 4) nil 函数值保持 nil，不被误当作有效目标。
	var nilFn fvFunc
	ck(nilFn == nil, "nil named func stays nil")
	ck(reflect.ValueOf(table["alpha"]).Kind() == reflect.Func, "map func value kind")

	gcStress()
	ck(p.id == 131 && p.name == "func-interface", "func pointer payload after GC")
	ck(p.next != nil && p.next.id == 132, "func pointer payload chain after GC")
	ck(fvName(table["alpha"]) == "main.fvLeafAlpha", "map func symbol after GC")
	finish("l14_funcval_interface_dispatch")
}
