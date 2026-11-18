// L15: 文件名后缀与 //go:build 约束是交集。三个文件名都以 _linux 结尾，
// 由 tag 进一步按 GOARCH 划分；额外的矛盾文件在任何平台都不编译。
// 构建成功 + 唯一 init token 同时证明交集语义与后缀过滤。
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

func main() {
	wantImpl := "non-linux"
	wantToken := "y-nonlinux"
	if runtime.GOOS == "linux" {
		switch runtime.GOARCH {
		case "amd64":
			wantImpl = "linux-amd64"
			wantToken = "v-linux-amd64"
		case "arm64":
			wantImpl = "linux-arm64"
			wantToken = "w-linux-arm64"
		default:
			wantImpl = "linux-other"
			wantToken = "x-linux-other"
		}
	}
	p := &payloadNode{id: 67, name: "suffix-same-partition"}

	ck(impl() == wantImpl, fmt.Sprintf("impl got=%q want=%q (GOOS=%s GOARCH=%s)", impl(), wantImpl, runtime.GOOS, runtime.GOARCH))
	ck(len(tokens) == 1, fmt.Sprintf("contradictory files compiled: tokens=%v", tokens))
	if len(tokens) == 1 {
		ck(tokens[0] == wantToken, fmt.Sprintf("selected token got=%q want=%q", tokens[0], wantToken))
	}

	gcStress()
	ck(impl() == wantImpl, "tag-selected impl after GC")
	ck(len(tokens) == 1 && tokens[0] == wantToken, "only one suffix+tag file after GC")
	ck(p.id == 67 && p.name == "suffix-same-partition", "payload after GC")
	finish("l15_suffix_same_partition")
}
