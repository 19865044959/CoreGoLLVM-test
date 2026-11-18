// L15: 构建标签词表组合。使用工具链真实提供的标签（GOOS/GOARCH、unix 元标签、
// 编译器标签 gc/gccgo、release 标签 go1.N）与形似但从未设置的自定义标签
// （linuxx/go1/gc1/Linux 等）构造组合；每个命中文件在 init 中登记 token，
// 运行期与由 runtime.GOOS/GOARCH/Compiler 推导的期望集合逐项比对。
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
	id    int64
	label string
}

var (
	failed int32
	gcSink [][]byte
	tokens []string
)

func recordToken(s string) { tokens = append(tokens, s) }

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

func isUnixOS(goos string) bool {
	switch goos {
	case "aix", "android", "darwin", "dragonfly", "freebsd",
		"hurd", "illumos", "ios", "linux", "netbsd", "openbsd", "solaris":
		return true
	}
	return false
}

func expectedTokens() []string {
	var want []string
	switch runtime.GOOS {
	case "linux":
		want = append(want, "os-linux")
	case "windows":
		want = append(want, "os-windows")
	}
	if isUnixOS(runtime.GOOS) {
		want = append(want, "meta-unix")
	}
	switch runtime.GOARCH {
	case "amd64":
		want = append(want, "arch-amd64")
	case "arm64":
		want = append(want, "arch-arm64")
	}
	if runtime.Compiler == "gc" {
		want = append(want, "compiler-gc")
	} else {
		want = append(want, "compiler-gccgo")
	}
	// 目标工具链为 go1.24.x：go1.24 标签成立、go1.25 不成立。
	want = append(want, "release-go1.24")
	if runtime.GOOS == "linux" && runtime.GOARCH == "amd64" && runtime.Compiler == "gc" {
		want = append(want, "exact-combo")
	}
	want = append(want, "lookalike-absent")
	sort.Strings(want)
	return want
}

func main() {
	p := &payloadNode{id: 90, label: "vocab"}

	got := append([]string(nil), tokens...)
	sort.Strings(got)
	want := expectedTokens()

	ck(len(got) == len(want), fmt.Sprintf("token count got=%d want=%d [%s]", len(got), len(want), strings.Join(got, ",")))
	for i := 0; i < len(got) && i < len(want); i++ {
		ck(got[i] == want[i], fmt.Sprintf("token[%d]=%q want %q", i, got[i], want[i]))
	}
	ck(strings.Join(got, ",") == strings.Join(want, ","), "tag vocabulary selection mismatch")
	ck(countOf(tokens, "lookalike") == 0, "lookalike tag unexpectedly satisfied")

	gcStress()
	ck(p.id == 90 && p.label == "vocab", "payload after GC")
	ck(len(tokens) == len(want), "tag init set stable after GC")
	finish("l15_tag_vocab_bitset")
}

func countOf(seq []string, s string) int {
	n := 0
	for _, v := range seq {
		if v == s {
			n++
		}
	}
	return n
}
