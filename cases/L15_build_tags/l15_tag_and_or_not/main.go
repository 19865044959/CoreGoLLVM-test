// L15: AND/OR/NOT 多标签组合。and/or/not 三个函数分别由多标签表达式选择实现，
// 期望值由当前 GOOS/GOARCH 推导；combo 验证自定义 tag 上的
// "tagX && (tagY || !tagY)" 括号组合（默认 !tagX 分支）。
package main

import (
	"fmt"
	"os"
	"runtime"
	"sync/atomic"
)

type tagNode struct {
	v    int64
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
	p := &tagNode{v: 24, name: "tags"}
	wantAnd := runtime.GOOS == "linux" && runtime.GOARCH == "amd64"
	wantOr := runtime.GOOS == "linux" || runtime.GOOS == "windows"
	wantNot := runtime.GOOS != "windows"

	gotAnd := andImpl() == "and"
	gotOr := orImpl() == "or"
	gotNot := notImpl() == "not"
	ck(gotAnd == wantAnd, "AND constraint (linux && amd64) selection")
	ck(gotOr == wantOr, "OR constraint (linux || windows) selection")
	ck(gotNot == wantNot, "NOT constraint (!windows) selection")

	// 默认无自定义 tag 时走 !tagX；-tags=tagX 时组合约束应选择 on。
	wantCombo := "off"
	if os.Getenv("EXPECT_TAGS") == "on" {
		wantCombo = "on"
	}
	ck(comboImpl() == wantCombo, "custom tag AND/OR/NOT combination")

	gcStress()
	ck(p.v == 24 && p.name == "tags", "tags payload after GC")
	ck(andImpl() == "and" || !wantAnd, "AND impl stable after GC")
	finish("l15_tag_and_or_not")
}
