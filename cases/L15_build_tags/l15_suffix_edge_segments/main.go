// L15: 文件名后缀只取末尾 1~2 段，且大小写敏感。本用例覆盖大写段、
// 重复段、GOARCH 位于中间、三段文件名中的 GOOS_GOARCH 成对等边界；
// 每个被选中的文件在 init 登记 token，main 按 GOOS/GOARCH 推导期望集合。
package main

import (
	"fmt"
	"os"
	"runtime"
	"sort"
	"strings"
	"sync/atomic"
)

type payloadNode struct {
	id   int64
	name string
}

var (
	failed int32
	gcSink [][]byte
	tokens []string
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

func expectedTokens() []string {
	// 末段不是当前 GOOS/GOARCH 的普通文件：大写段不参与匹配，永远编译。
	want := []string{"M_LINUX", "M_LINUX_AMD64"}
	// 末段是当前 GOOS 的单段匹配；即使前面出现 GOARCH/普通段也不参与过滤。
	if runtime.GOOS == "linux" {
		want = append(want, "M_LINUX_LINUX", "M_LINUX_AMD64_LINUX", "M_ARM_LINUX", "M_WINDOWS_AMD64_LINUX")
	}
	// 倒数第二段是合法 GOOS、末段是合法 GOARCH 时按成对后缀匹配。
	if runtime.GOOS == "windows" && runtime.GOARCH == "amd64" {
		want = append(want, "M_LINUX_WINDOWS_AMD64")
	}
	// 末段是当前 GOARCH 的单段匹配；倒数第二段不是 GOOS 时不成对。
	switch runtime.GOARCH {
	case "amd64":
		want = append(want, "M_386_AMD64", "M_UNIX_AMD64", "M_JS_WASM_AMD64")
	case "386":
		want = append(want, "M_LINUX_AMD64_386")
	}
	sort.Strings(want)
	return want
}

func main() {
	want := expectedTokens()
	got := append([]string(nil), tokens...)
	sort.Strings(got)
	p := &payloadNode{id: 74, name: "suffix-edge-segments"}

	ck(strings.Join(got, ",") == strings.Join(want, ","),
		fmt.Sprintf("selected suffix files got=%v want=%v (GOOS=%s GOARCH=%s)", got, want, runtime.GOOS, runtime.GOARCH))
	ck(len(got) == len(want), fmt.Sprintf("token count got=%d want=%d", len(got), len(want)))

	gcStress()
	got2 := append([]string(nil), tokens...)
	sort.Strings(got2)
	ck(strings.Join(got2, ",") == strings.Join(want, ","), "selected suffix files after GC")
	ck(p.id == 74 && p.name == "suffix-edge-segments", "payload after GC")
	finish("l15_suffix_edge_segments")
}
