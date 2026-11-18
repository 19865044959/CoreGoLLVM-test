// L15: 文件名后缀矩阵。覆盖 _GOOS、_GOARCH、_GOOS_GOARCH 三类后缀，以及
// _linux_custom、_unix、_linux_amd64_extra、_foo 这些"看起来像后缀但规则不识别"
// 的普通文件。每个文件在 init 登记 token，main 按 runtime.GOOS/GOARCH 推导
// 期望集合，验证后缀矩阵的选入/排除完全正确。
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
	failed       int32
	gcSink       [][]byte
	suffixTokens []string
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
	want := []string{
		"ordinary-extra", "ordinary-foo", "ordinary-linux-custom", "ordinary-unix",
	}
	switch runtime.GOOS {
	case "linux":
		want = append(want, "goos-linux")
	case "windows":
		want = append(want, "goos-windows")
	case "darwin":
		want = append(want, "goos-darwin")
	}
	switch runtime.GOARCH {
	case "amd64":
		want = append(want, "goarch-amd64")
	case "arm64":
		want = append(want, "goarch-arm64")
	case "386":
		want = append(want, "goarch-386")
	}
	pair := runtime.GOOS + "-" + runtime.GOARCH
	switch pair {
	case "linux-amd64", "linux-arm64", "windows-amd64", "darwin-arm64":
		want = append(want, "pair-"+pair)
	}
	sort.Strings(want)
	return want
}

func main() {
	p := &payloadNode{id: 101, name: "suffix-matrix"}

	got := append([]string(nil), suffixTokens...)
	sort.Strings(got)
	want := expectedTokens()

	ck(len(got) == len(want), fmt.Sprintf("token count got=%d want=%d [%s]", len(got), len(want), strings.Join(got, ",")))
	for i := 0; i < len(got) && i < len(want); i++ {
		ck(got[i] == want[i], fmt.Sprintf("token[%d]=%q want %q", i, got[i], want[i]))
	}
	ck(strings.Join(got, ",") == strings.Join(want, ","), "filename suffix matrix selection mismatch")
	ck(ordinaryValue() == "ordinary", "ordinary-named file compiled")

	gcStress()
	got2 := append([]string(nil), suffixTokens...)
	sort.Strings(got2)
	ck(strings.Join(got2, ",") == strings.Join(want, ","), "suffix selection stable after GC")
	ck(ordinaryValue() == "ordinary" && p.id == 101 && p.name == "suffix-matrix", "values after GC")
	finish("l15_filename_suffix_matrix")
}
