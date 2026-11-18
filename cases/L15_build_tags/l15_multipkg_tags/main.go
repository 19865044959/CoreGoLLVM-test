// L15: 多包 + 构建标签。main -> flags 与 engine 两个包：flags 用自定义 tagFast
// 选择模式/常量，engine 用 GOOS 标签选择平台实现。默认构建走 slow + 当前平台。
package main

import (
	"fmt"
	"os"
	"runtime"
	"sync/atomic"

	"l15_multipkg_tags/engine"
	"l15_multipkg_tags/flags"
)

type mpNode struct {
	id   int64
	name string
	next *mpNode
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
	p := &mpNode{id: 12, name: "multipkg-tags"}
	p.next = &mpNode{id: 13, name: "next"}

	wantMode := os.Getenv("EXPECT_MODE")
	if wantMode == "" {
		wantMode = "slow"
	}
	wantScale := int64(2)
	if wantMode == "fast" {
		wantScale = 7
	}

	settings := flags.Default()
	ck(settings.Mode == wantMode, "custom tag selects flags mode")
	ck(settings.Scale == wantScale, "custom tag selects flags scale")

	rec := engine.Run(5)
	ck(rec != nil, "cross-package result allocated")
	ck(rec.ID == 5 && rec.Tag == "engine", "cross-package result identity")
	ck(rec.Mode == wantMode, "engine sees flags mode through import")
	ck(rec.Value == 5*3+wantScale, "engine computation uses tag-selected constant")

	wantPlatform := "other-engine"
	if runtime.GOOS == "linux" {
		wantPlatform = "linux-engine"
	}
	ck(rec.Platform == wantPlatform, "GOOS tag inside imported package")

	gcStress()
	ck(rec.Mode == wantMode && rec.Value == 5*3+wantScale && rec.Platform == wantPlatform, "cross-package result after GC")
	ck(p.id == 12 && p.name == "multipkg-tags" && p.next != nil && p.next.id == 13, "main payload after GC")
	finish("l15_multipkg_tags")
}
