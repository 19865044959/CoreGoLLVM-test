// L15: 构建标签切断 import 环。linux 下 a 导入 b（b 的 linux 文件不导入 a），
// 非 linux 下 b 导入 a（a 的非 linux 文件不导入 b），因此任一平台都无环且
// import 方向相反。运行期校验被选中的依赖边、包 init 顺序与 GC 后对象。
package main

import (
	"fmt"
	"os"
	"runtime"
	"strconv"
	"sync/atomic"

	"l15_tag_cycle_break/a"
	"l15_tag_cycle_break/b"
	"l15_tag_cycle_break/trace"
)

type payloadNode struct {
	id   int64
	name string
}

var (
	failed int32
	gcSink [][]byte
)

func init() { trace.Mark("main") }

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

func indexOf(seq []string, s string) int {
	for i, v := range seq {
		if v == s {
			return i
		}
	}
	return -1
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

func envInt(key string, def int) int {
	v := os.Getenv(key)
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < 1 {
		return def
	}
	return n
}

func main() {
	linux := runtime.GOOS == "linux"
	wantPartner := "a-standalone"
	wantHelper := "b->a"
	wantOrder := []string{"trace", "a", "b", "main"}
	if linux {
		wantPartner = "a->b"
		wantHelper = "b-local:local-b"
		wantOrder = []string{"trace", "b", "a", "main"}
	}
	p := &payloadNode{id: 55, name: "tag-cycle-break"}

	ck(a.Partner() == wantPartner, fmt.Sprintf("a->b edge got=%q want=%q", a.Partner(), wantPartner))
	ck(b.Helper() == wantHelper, fmt.Sprintf("b->a edge got=%q want=%q", b.Helper(), wantHelper))
	ck(a.A == "a" && a.LocalA() == "local-a" && b.Name == "b" && b.LocalB() == "local-b",
		"common package declarations")

	seq := trace.Snapshot()
	ck(len(seq) == 4, fmt.Sprintf("init markers got=%d want=4", len(seq)))
	for _, name := range wantOrder {
		ck(countOf(seq, name) == 1, "init marker "+name+" appears exactly once")
	}
	for i, name := range wantOrder {
		got := "<missing>"
		if i < len(seq) {
			got = seq[i]
		}
		ck(indexOf(seq, name) == i, fmt.Sprintf("init slot %d got=%q want=%q", i, got, name))
	}

	gcStress()
	ck(a.Partner() == wantPartner && b.Helper() == wantHelper, "tag-selected edges after GC")
	ck(p.id == 55 && p.name == "tag-cycle-break", "payload after GC")

	rounds := envInt("TORTURE_ROUNDS", 5000)
	seed := envInt("UAF_SEED", 1)
	gcEvery := rounds / 16
	if gcEvery < 1 {
		gcEvery = 1
	}
	wantLen := len(wantPartner) + 1 + len(wantHelper)
	var sum int64
	for r := 0; r < rounds; r++ {
		s := a.Partner() + "|" + b.Helper()
		if len(s) != wantLen {
			ck(false, fmt.Sprintf("edge length round=%d seed=%d got=%d want=%d", r, seed, len(s), wantLen))
			break
		}
		sum += int64(len(s))
		if r%128 == 0 {
			buf := make([]byte, 1024)
			buf[0] = byte(r)
			gcSink = append(gcSink[:0], buf)
		}
		if r%gcEvery == 0 {
			runtime.GC()
		}
	}
	ck(sum == int64(rounds)*int64(wantLen), fmt.Sprintf("torture checksum got=%d want=%d seed=%d", sum, int64(rounds)*int64(wantLen), seed))

	runtime.GC()
	runtime.GC()
	ck(a.Partner() == wantPartner && b.Helper() == wantHelper, "tag-selected edges after final GC")
	ck(len(trace.Snapshot()) == 4, "init trace unchanged after GC")
	finish("l15_tag_cycle_break")
}
