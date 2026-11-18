// L15: 同一包的初始化阶段顺序。无论文件字典序如何，全部包级变量的
// 初始化器都先执行完毕，然后才按文件字典序执行各文件的 init。tag 二选一
// 文件在 b 槽位同时提供变量和 init，用来验证它不会打乱两个阶段的边界。
package main

import (
	"fmt"
	"os"
	"runtime"
	"strings"
	"sync/atomic"
)

type payloadNode struct {
	v    int64
	name string
}

var (
	failed           int32
	gcSink           [][]byte
	trace            []string
	initSawAllValues bool
)

func note(name string, v int64) int64 {
	trace = append(trace, name)
	return v
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

func main() {
	tagName := "var-tag-linux"
	tagInit := "init-tag-linux"
	tagVal := int64(10)
	if runtime.GOOS != "linux" {
		tagName = "var-tag-other"
		tagInit = "init-tag-other"
		tagVal = 20
	}
	wantTrace := "var-a," + tagName + ",var-m,init-a1,init-a2," + tagInit + ",init-z"
	p := &payloadNode{v: 7, name: "phase-payload"}

	ck(VA == 1, fmt.Sprintf("VA got=%d want=1", VA))
	ck(VTag == tagVal, fmt.Sprintf("VTag got=%d want=%d (GOOS=%s)", VTag, tagVal, runtime.GOOS))
	ck(VM == tagVal+1, fmt.Sprintf("VM got=%d want=%d", VM, tagVal+1))
	ck(strings.Join(trace, ",") == wantTrace,
		fmt.Sprintf("init trace got=%q want=%q", strings.Join(trace, ","), wantTrace))
	ck(payload != nil && payload.v == VM && payload.name == "phase-payload", "var-phase payload")
	ck(initSawAllValues, "init phase did not see fully initialized variables")

	seenInit := false
	for _, s := range trace {
		if strings.HasPrefix(s, "init-") {
			seenInit = true
			continue
		}
		ck(!seenInit, "package variable initializer ran after an init function: "+s)
	}

	gcStress()
	ck(VA == 1 && VTag == tagVal && VM == tagVal+1, "values after GC")
	ck(payload != nil && payload.v == VM && payload.name == "phase-payload", "payload after GC")
	ck(strings.Join(trace, ",") == wantTrace, "init trace unchanged after GC")
	ck(p.v == 7 && p.name == "phase-payload", "local payload after GC")
	finish("l15_init_phase_order")
}
