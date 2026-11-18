// L15: 文件名后缀的解析顺序。规则是"看末尾 1~2 段"：如果最后一段是当前
// GOOS 或当前 GOARCH，就按该单段匹配；只有当倒数第二段是合法 GOOS 且末段
// 是合法 GOARCH 时，才要求两者同时匹配。更早的段落只是文件名主体。
package main

import (
	"fmt"
	"os"
	"runtime"
	"sort"
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
	// 末段不是 GOOS/GOARCH 的文件在任何平台都参与编译。
	want := []string{"extra_linux_amd64_v2", "look_x86_64"}
	switch runtime.GOOS {
	case "linux":
		// _linux 匹配；pair_amd64_linux 的末段也是 linux，amd64 在中间不参与过滤。
		want = append(want, "last_linux", "pair_amd64_linux")
	case "windows":
		// combo_linux_windows 末段 windows 匹配，中间的 linux 不参与过滤。
		want = append(want, "last_windows", "combo_linux_windows")
		if runtime.GOARCH == "amd64" {
			want = append(want, "pair_windows_amd64")
		}
	case "darwin":
		// pair_arm64_darwin 的末段是 GOOS；pair_darwin_arm64 是成对后缀。
		want = append(want, "pair_arm64_darwin")
		if runtime.GOARCH == "arm64" {
			want = append(want, "pair_darwin_arm64")
		}
	}
	sort.Strings(want)
	p := &payloadNode{id: 83, name: "suffix-parse-order"}

	got := snapshot()
	ck(equalStrings(got, want), fmt.Sprintf("selected files got=%v want=%v", got, want))

	gcStress()
	ck(equalStrings(snapshot(), want), "selected files after GC")
	ck(p.id == 83 && p.name == "suffix-parse-order", "payload after GC")
	finish("l15_suffix_parse_order")
}
