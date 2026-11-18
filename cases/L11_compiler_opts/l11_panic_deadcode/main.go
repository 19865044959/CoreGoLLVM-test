// L11: 含 panic 的死代码——常量假分支、不可达语句、未调用闭包里的
// panic 不得执行；运行期可达的 panic（显式、除零、越界）必须照常触发。
package main

import (
	"fmt"
	"os"
	"runtime"
	"strconv"
	"strings"
	"sync/atomic"
)

var (
	failed int32
	gcSink [][]byte
	seed   uint64 = 1
)

const debugBuild = false

var deadSide int64

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

//go:noinline
func runtimeZero() int64 {
	deadSide++
	return 0
}

//go:noinline
func sideBool() bool {
	deadSide += 100
	return true
}

type pdCode struct{ code int }

func (e *pdCode) Error() string { return "code" + strconv.Itoa(e.code) }

func deadBranches() (hits int64) {
	if false {
		panic("dead-if")
	}
	if 1 > 2 {
		panic("dead-cmp")
	}
	switch 3 {
	case 1:
		panic("dead-case-1")
	case 3:
		hits++
	case 5:
		panic("dead-case-5")
	}
	for i := 0; i < 0; i++ {
		panic("dead-loop")
	}
	if debugBuild {
		panic("dead-const")
	}
	if false && sideBool() {
		panic("dead-shortcircuit")
	}
	if true || sideBool() {
		hits++
	}
	_ = func() {
		panic("never-called-closure")
	}
	zero := runtimeZero()
	if false {
		_ = 7 / zero
		panic("dead-div")
	}
	return hits
}

func unreachableAfterReturn() int {
	return 3
	panic("dead-after-return")
}

func unreachableAfterIf() int {
	if true {
		return 4
	}
	panic("dead-after-if")
}

func livePanic(flag bool, msg string) string {
	if flag {
		panic(msg)
	}
	return "ok"
}

func panicCode() {
	panic(&pdCode{code: 7})
}

func recoverAny(fn func()) (r any) {
	defer func() { r = recover() }()
	fn()
	return nil
}

func panicString(fn func()) (out string, panicked bool) {
	defer func() {
		if r := recover(); r != nil {
			out = fmt.Sprint(r)
			panicked = true
		}
	}()
	fn()
	return "no-panic", false
}

func deferRecoverPanic(n int) (recovered string, count int) {
	defer func() {
		if r := recover(); r != nil {
			recovered = fmt.Sprint(r)
		}
	}()
	for i := 0; i < n; i++ {
		count++
		if i == n/2 {
			panic("loop-panic")
		}
	}
	return "no-panic", count
}

func nestedRecover() (out string) {
	defer func() {
		if r := recover(); r != nil {
			out = "outer:" + fmt.Sprint(r)
		}
	}()
	func() {
		defer func() {
			if r := recover(); r != nil {
				panic("wrapped:" + fmt.Sprint(r))
			}
		}()
		panic("inner")
	}()
	return "no-panic"
}

func boundsPanicLive(n, bad int) (out string) {
	defer func() {
		if r := recover(); r != nil {
			out = fmt.Sprint(r)
		}
	}()
	s := make([]int64, n)
	for i := 0; i < n; i++ {
		s[i] = int64(i)
		if i == bad {
			_ = s[i+n]
		}
	}
	return "no-panic"
}

func divLive() (out string) {
	defer func() {
		if r := recover(); r != nil {
			out = fmt.Sprint(r)
		}
	}()
	zero := runtimeZero()
	return fmt.Sprint(100 / zero)
}

func panicNil() (out string) {
	defer func() {
		if r := recover(); r != nil {
			out = fmt.Sprint(r)
		}
	}()
	panic(nil)
}

func panicAfterGC() (out string) {
	defer func() {
		if r := recover(); r != nil {
			out = fmt.Sprint(r)
		}
	}()
	gcStress()
	panic("after-gc")
}

func deadAfterPanic(flag bool) (out string) {
	defer func() {
		if r := recover(); r != nil {
			out = fmt.Sprint(r)
		}
	}()
	if flag {
		panic("first")
		out = "unreachable-after-panic"
	}
	return "fine"
}

func main() {
	seed = loadSeed()
	n := 8 + int(seed%8)
	depth := n/2 + 1

	deadSide = 0
	hits := deadBranches()
	ck(hits == 2, "dead branches changed live hit count")
	ck(deadSide == 1, "dead branch executed runtimeZero or sideBool")

	ck(unreachableAfterReturn() == 3, "unreachable code after return executed")
	ck(unreachableAfterIf() == 4, "unreachable code after constant if executed")

	ck(livePanic(false, "live-panic") == "ok", "live panic fired on false branch")
	got, panicked := panicString(func() { _ = livePanic(true, "live-panic") })
	ck(panicked && got == "live-panic", "live panic did not fire on true branch")

	rec := recoverAny(panicCode)
	pc, ok := rec.(*pdCode)
	ck(ok && pc.code == 7, "custom panic value lost")

	rs, count := deferRecoverPanic(n)
	ck(rs == "loop-panic", "recovered loop panic text mismatch")
	ck(count == depth, "loop panic count mismatch")

	rs = nestedRecover()
	ck(rs == "outer:wrapped:inner", "nested recover wrapping mismatch")

	rs = boundsPanicLive(n, n/3)
	ck(strings.Contains(rs, "index out of range"), "live bounds panic missing")
	ck(boundsPanicLive(n, -1) == "no-panic", "bounds panic fired on unreachable index")

	rs = divLive()
	ck(strings.Contains(rs, "integer divide by zero"), "live divide-by-zero missing")
	ck(deadSide == 2, "dead divide-by-zero path executed")

	rs = panicNil()
	ck(strings.Contains(rs, "panic called with nil argument"), "panic(nil) semantics mismatch")

	rs = panicAfterGC()
	ck(rs == "after-gc", "panic after GC not recovered")

	rs = deadAfterPanic(true)
	ck(rs == "first", "live panic before unreachable statement missing")
	ck(deadAfterPanic(false) == "fine", "dead path after guarded panic broken")

	// recover 出来的字符串保留到 GC 后仍一致。
	kept := []string{"live-panic", "outer:wrapped:inner", "after-gc", "recovered"}
	gcStress()
	ck(kept[0] == "live-panic" && kept[2] == "after-gc", "recovered strings corrupted after GC")
	ck(deadSide == 2, "side effect counters changed after GC")
	ck(hits == 2, "dead branch hits changed after GC")

	finish("l11_panic_deadcode")
}
