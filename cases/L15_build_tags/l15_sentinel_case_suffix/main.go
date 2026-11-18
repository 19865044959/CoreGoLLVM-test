// L15: tag 哨兵与大小写。Ignore/TRUE/False/Linux 是未设置的普通 tag 名，
// 不能按单词大小写折叠成 ignore/true/false/内建 GOOS；同时文件名里出现
// ignore/true/false 字样不是特殊后缀，文件必须照常编译。
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
	want := []string{"base", "name-false", "name-ignore", "name-true", "neg-case"}
	got := append([]string(nil), tokens...)
	sort.Strings(want)
	sort.Strings(got)
	p := &payloadNode{id: 106, name: "sentinel-case"}

	ck(marker() == "base", fmt.Sprintf("uppercase sentinel selected: marker=%q", marker()))
	ck(negCase == "neg-ok", fmt.Sprintf("negative sentinel file missing: negCase=%q", negCase))
	ck(ignoreNameOK == "ignore-name" && trueNameOK == "true-name" && falseNameOK == "false-name",
		"sentinel-like filename suffix was treated as special")
	ck(strings.Join(got, ",") == strings.Join(want, ","),
		fmt.Sprintf("init tokens got=%v want=%v", got, want))
	ck(len(got) == len(want), fmt.Sprintf("token count got=%d want=%d", len(got), len(want)))

	gcStress()
	got2 := append([]string(nil), tokens...)
	sort.Strings(got2)
	ck(strings.Join(got2, ",") == strings.Join(want, ","), "sentinel tokens after GC")
	ck(marker() == "base" && negCase == "neg-ok", "sentinel state after GC")
	ck(p.id == 106 && p.name == "sentinel-case", "payload after GC")
	finish("l15_sentinel_case_suffix")
}
