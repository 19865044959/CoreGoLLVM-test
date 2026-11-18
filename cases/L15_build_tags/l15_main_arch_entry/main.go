// L15: main 入口由 GOARCH 文件名后缀选择。三个互斥入口各自定义入口常量、
// init 标记和 main；共享文件的包级变量引用被选中入口的常量，因此变量初始化、
// init 顺序与入口选择都必须一致。
package main

import (
	"fmt"
	"os"
	"runtime"
	"sync/atomic"
)

type entryPayload struct {
	name string
	bias int64
}

var (
	failed      int32
	gcSink      [][]byte
	entryInits  []string
	derivedBias = entryBias + 1
	payload     = &entryPayload{name: entryName, bias: derivedBias}
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

func runMain(name string, bias int64) {
	wantName := "generic"
	wantBias := int64(3)
	switch runtime.GOARCH {
	case "amd64":
		wantName = "amd64"
		wantBias = 11
	case "arm64":
		wantName = "arm64"
		wantBias = 7
	}

	ck(name == wantName, fmt.Sprintf("tag-selected main got=%q want=%q (GOARCH=%s)", name, wantName, runtime.GOARCH))
	ck(bias == wantBias, fmt.Sprintf("entry bias got=%d want=%d", bias, wantBias))
	ck(len(entryInits) == 1, fmt.Sprintf("entry init count got=%d want=1 (%v)", len(entryInits), entryInits))
	if len(entryInits) == 1 {
		ck(entryInits[0] == "entry-"+wantName, fmt.Sprintf("entry init token got=%q want=%q", entryInits[0], "entry-"+wantName))
	}
	ck(derivedBias == wantBias+1, fmt.Sprintf("shared var derived from selected entry got=%d want=%d", derivedBias, wantBias+1))
	ck(payload != nil && payload.name == wantName && payload.bias == wantBias+1, "package var built before main")

	gcStress()
	ck(name == wantName && bias == wantBias, "entry selection after GC")
	ck(len(entryInits) == 1 && entryInits[0] == "entry-"+wantName, "entry init after GC")
	ck(derivedBias == wantBias+1 && payload != nil && payload.bias == wantBias+1, "derived payload after GC")
	finish("l15_main_arch_entry")
}
