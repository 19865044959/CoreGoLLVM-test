// L15: 跨文件包级变量依赖可通过函数体传递。B 依赖 viaFunc 函数体里的 A，
// C 依赖 chainC 函数体里的 B，tag 文件的 Tagged 依赖 viaTagged 函数体里的 C，
// main 的 head 依赖上述全部变量。初始化顺序必须沿函数体做拓扑排序。
package main

import (
	"fmt"
	"os"
	"runtime"
	"strconv"
	"strings"
	"sync/atomic"
)

type valueNode struct {
	id   int64
	next *valueNode
}

var (
	failed      int32
	gcSink      [][]byte
	trace       []string
	initSawVars bool
	head        = buildHead()
)

func note(name string, v int64) int64 {
	trace = append(trace, name)
	return v
}

// buildHead 的函数体引用全部变量，使 head 排在这些变量之后初始化。
func buildHead() *valueNode {
	trace = append(trace, "head")
	return &valueNode{id: Tagged, next: &valueNode{id: C, next: &valueNode{id: B, next: &valueNode{id: A}}}}
}

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

func envInt(key string, def int) int {
	v := os.Getenv(key)
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < 1 {
		return def
	}
	return n
}

func main() {
	tagName := "tagged-linux"
	tagged := int64(16)
	if runtime.GOOS != "linux" {
		tagName = "tagged-other"
		tagged = 17
	}
	wantTrace := "A,B,C," + tagName + ",head,init-tag"
	p := &valueNode{id: 19}

	ck(A == 5, fmt.Sprintf("A got=%d want=5", A))
	ck(B == 6, fmt.Sprintf("B got=%d want=6", B))
	ck(C == 13, fmt.Sprintf("C got=%d want=13", C))
	ck(Tagged == tagged, fmt.Sprintf("Tagged got=%d want=%d (GOOS=%s)", Tagged, tagged, runtime.GOOS))
	ck(strings.Join(trace, ",") == wantTrace,
		fmt.Sprintf("var init order got=%q want=%q", strings.Join(trace, ","), wantTrace))
	ck(initSawVars, "init ran before function-mediated variables were initialized")
	ck(head != nil && head.id == Tagged && head.next != nil && head.next.id == C &&
		head.next.next != nil && head.next.next.id == B && head.next.next.next != nil &&
		head.next.next.next.id == A && head.next.next.next.next == nil, "initializer-built chain")

	gcStress()
	ck(A == 5 && B == 6 && C == 13 && Tagged == tagged, "values after GC")
	ck(head != nil && head.id == Tagged && head.next.id == C && head.next.next.id == B &&
		head.next.next.next.id == A, "chain after GC")

	rounds := envInt("TORTURE_ROUNDS", 20000)
	seed := envInt("UAF_SEED", 1)
	ids := [4]int64{Tagged, C, B, A}
	gcEvery := rounds / 16
	if gcEvery < 1 {
		gcEvery = 1
	}
	var sum int64
	for r := 0; r < rounds; r++ {
		start := (r + seed) % 4
		n := head
		for i := 0; i < start && n != nil; i++ {
			n = n.next
		}
		if n == nil || n.id != ids[start] {
			ck(false, fmt.Sprintf("chain corrupted round=%d start=%d seed=%d", r, start, seed))
			break
		}
		sum += n.id
		if r%256 == 0 {
			buf := make([]byte, 1024)
			buf[0] = byte(r)
			gcSink = append(gcSink[:0], buf)
		}
		if r%gcEvery == 0 {
			runtime.GC()
		}
	}
	cycle := ids[0] + ids[1] + ids[2] + ids[3]
	wantSum := int64(rounds/4) * cycle
	for i := 0; i < rounds%4; i++ {
		wantSum += ids[(seed+i)%4]
	}
	ck(sum == wantSum, fmt.Sprintf("torture checksum got=%d want=%d seed=%d rounds=%d", sum, wantSum, seed, rounds))

	runtime.GC()
	runtime.GC()
	ck(strings.Join(trace, ",") == wantTrace, "var init trace unchanged after GC")
	ck(head != nil && head.id == Tagged && head.next.id == C && head.next.next.id == B &&
		head.next.next.next.id == A, "chain after final GC")
	ck(p.id == 19, "payload after GC")
	finish("l15_var_func_dep_files")
}
