// L15: build 表达式语法。成对文件覆盖：
//   - 旧式 // +build：空格=OR、逗号=AND、多行=AND，及其严格补集；
//   - //go:build：无空格括号、&& 优先于 ||、! 作用于括号分组。
// 每对约束都是严格互补，构建成功 + 运行期期望值共同证明表达式解析正确。
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
	linux := runtime.GOOS == "linux"
	darwin := runtime.GOOS == "darwin"
	windows := runtime.GOOS == "windows"
	amd64 := runtime.GOARCH == "amd64"

	wantOS := "other"
	if linux {
		wantOS = "linux"
	}
	wantOR := "plain"
	if linux || darwin {
		wantOR = "unixish"
	}
	wantCombo := "other"
	if linux && amd64 {
		wantCombo = "linux-amd64"
	}
	wantTight := "no"
	if linux && amd64 {
		wantTight = "yes"
	}
	wantPrec := "miss"
	if (linux && amd64) || windows {
		wantPrec = "hit"
	}
	wantGroup := "nogroup"
	if linux && (!windows || amd64) {
		wantGroup = "grouped"
	}

	p := &payloadNode{id: 91, name: "expr-legacy"}

	ck(legacyOS() == wantOS, "legacy +build !/positive selects by GOOS")
	ck(legacyOR() == wantOR, "legacy space-separated OR and multi-line AND")
	ck(legacyCombo() == wantCombo, "legacy comma-separated AND")
	ck(modernTight() == wantTight, "go:build parenthesized expression without spaces")
	ck(modernPrec() == wantPrec, "go:build && precedence over ||")
	ck(modernGroup() == wantGroup, "go:build NOT over parenthesized expression")

	gcStress()
	ck(legacyOS() == wantOS && legacyOR() == wantOR && legacyCombo() == wantCombo, "legacy selections after GC")
	ck(modernTight() == wantTight && modernPrec() == wantPrec && modernGroup() == wantGroup, "modern selections after GC")
	ck(p.id == 91 && p.name == "expr-legacy", "payload after GC")
	finish("l15_build_expr_legacy")
}
