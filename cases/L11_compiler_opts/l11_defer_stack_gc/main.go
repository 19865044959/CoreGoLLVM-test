// L11: defer 与栈增长/GC——递归多帧 defer 的 LIFO 顺序、panic 展开路径、
// 参数注册时求值、循环 defer、方法值接收者、defer 内触发深递归与 GC 后对象读取。
package main

import (
	"fmt"
	"os"
	"runtime"
	"strconv"
	"sync/atomic"
)

type dfNode struct {
	v   int64
	tag string
}

type dfCounter struct{ n int64 }

func (c *dfCounter) add() { c.n++ }

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

func dfRec(n int, trace *[]int) (res int64) {
	defer func() {
		res += int64(n)
		*trace = append(*trace, n)
	}()
	if n == 0 {
		return 0
	}
	return dfRec(n-1, trace) + 1
}

func dfPanic(n int, trace *[]int) {
	*trace = append(*trace, n)
	if n == 0 {
		panic("bottom")
	}
	defer func() {
		*trace = append(*trace, 100+n)
	}()
	dfPanic(n-1, trace)
}

func dfRunPanic(n int) (msg string, trace []int) {
	defer func() {
		if r := recover(); r != nil {
			msg = fmt.Sprint(r)
		}
	}()
	dfPanic(n, &trace)
	return "no panic", trace
}

func dfArgEval() (out string) {
	v := 1
	defer func(s string) { out = s }(strconv.Itoa(v))
	v = 2
	return "base"
}

func dfLoop(n int) (out []int) {
	for i := 0; i < n; i++ {
		i := i
		defer func() { out = append(out, i) }()
	}
	return out
}

func dfGCNode() (v int64, tag string) {
	node := &dfNode{v: 77, tag: "kept"}
	defer func() {
		v = node.v
		tag = node.tag
	}()
	gcStress()
	node.v = 78
	return node.v, node.tag
}

func dfMethodValue() (ret int64, c *dfCounter) {
	c = &dfCounter{}
	defer c.add()
	c.n += 10
	return c.n, c
}

func dfIfaceGC() (tag string) {
	var v any = &dfNode{v: 55, tag: "iface"}
	defer func() { tag = v.(*dfNode).tag }()
	gcStress()
	return "unused"
}

func dfDeep(n int) int64 {
	if n == 0 {
		return 0
	}
	return 1 + dfDeep(n-1)
}

func dfDeferDeep() (out int64) {
	defer func() { out = dfDeep(3000) }()
	return 1
}

func dfMid(n int) (out int64) {
	defer func() {
		if r := recover(); r != nil {
			out = -1
		}
	}()
	if n > 0 {
		return dfMid(n - 1)
	}
	panic("deep")
}

func main() {
	seed = loadSeed()
	if seed == 0 {
		seed = 1
	}

	depth := 2000 + int(seed%500)
	var trace []int
	res := dfRec(depth, &trace)
	want := int64(depth*(depth+1)/2 + depth)
	ck(res == want, "recursive defer named result")
	ck(len(trace) == depth+1, "recursive defer execution count")
	orderOK := len(trace) == depth+1 && trace[0] == 0 && trace[depth] == depth
	if orderOK {
		for i := 0; i <= depth; i += 97 {
			if trace[i] != i {
				orderOK = false
				break
			}
		}
	}
	ck(orderOK, "recursive defer LIFO order across stack growth")

	msg, ptrace := dfRunPanic(40)
	ck(msg == "bottom", "panic unwinding recover")
	wantPanicLen := 41 + 40
	panicOK := len(ptrace) == wantPanicLen
	if panicOK {
		for i := 0; i <= 40; i++ {
			if ptrace[i] != 40-i {
				panicOK = false
				break
			}
		}
		for i := 0; i < 40; i++ {
			if ptrace[41+i] != 101+i {
				panicOK = false
				break
			}
		}
	}
	ck(panicOK, "multi-frame defer order during panic")

	ck(dfArgEval() == "1", "defer argument evaluated at registration")

	loop := dfLoop(7)
	loop2 := dfLoop(9)
	loopOK := len(loop) == 7 && len(loop2) == 9
	if loopOK {
		for i := range loop {
			if loop[i] != 6-i {
				loopOK = false
				break
			}
		}
		for i := range loop2 {
			if loop2[i] != 8-i {
				loopOK = false
				break
			}
		}
	}
	ck(loopOK, "loop defer LIFO at open-coded boundary")

	v, tag := dfGCNode()
	ck(v == 78 && tag == "kept", "deferred closure reads object after GC")

	ret, c := dfMethodValue()
	ck(ret == 10 && c != nil && c.n == 11, "deferred method value pointer receiver")

	ck(dfDeferDeep() == 3000, "defer triggers deep stack growth")
	ck(dfIfaceGC() == "iface", "deferred interface assertion after GC")
	ck(dfMid(50) == -1, "panic recovered in innermost defer frame")

	gcStress()
	ck(res == want && len(trace) == depth+1 && trace[depth] == depth, "recursive defer results after final GC")
	ck(msg == "bottom" && len(ptrace) == wantPanicLen, "panic trace after final GC")

	finish("l11_defer_stack_gc")
}
