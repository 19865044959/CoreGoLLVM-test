// L15: ignore/true/false 都是普通 tag 名而非布尔字面量。三个哨兵文件默认
// 被排除（且含语法错误与重复符号），而 !ignore && !true && !false 的取反
// 文件必须参与编译：这同时验证 tag 词法、取反语义与文件排除时机。
package main

import (
	"fmt"
	"os"
	"runtime"
	"sync/atomic"
)

type payloadNode struct {
	id   int64
	name string
}

var (
	failed int32
	gcSink [][]byte
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
	p := &payloadNode{id: 102, name: "ignore-sentinel"}

	ck(marker() == "base", fmt.Sprintf("sentinel file selected: marker=%q", marker()))
	ck(liveMarker == "base", fmt.Sprintf("sentinel duplicate definition visible: liveMarker=%q", liveMarker))
	ck(sentinelNeg == "neg-ok", fmt.Sprintf("negated constraint file missing: sentinelNeg=%q", sentinelNeg))
	ck(len(baseTokens) == 1, fmt.Sprintf("sentinel init ran: tokens=%v", baseTokens))
	if len(baseTokens) == 1 {
		ck(baseTokens[0] == "base", fmt.Sprintf("init token got=%q want=base", baseTokens[0]))
	}

	gcStress()
	ck(marker() == "base" && liveMarker == "base" && sentinelNeg == "neg-ok", "constraint state after GC")
	ck(len(baseTokens) == 1 && baseTokens[0] == "base", "sentinel init absent after GC")
	ck(p.id == 102 && p.name == "ignore-sentinel", "payload after GC")
	finish("l15_tag_ignore_sentinel")
}
