// L15: GOARCH 选择 import 边来打破 import 环。amd64 下 a 依赖 b，
// 非 amd64 下 b 依赖 a；两条边分别由文件名后缀 _amd64 与 !amd64 约束互斥提供。
// 运行期校验依赖方向、包 init 顺序，并在 TORTURE_ROUNDS 轮中反复取值。
package main

import (
	"fmt"
	"os"
	"runtime"
	"strconv"
	"sync/atomic"

	"l15_cycle_goarch_edges/a"
	"l15_cycle_goarch_edges/b"
	"l15_cycle_goarch_edges/trace"
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
		buf := make([]byte, 4096)
		buf[0] = byte(i)
		gcSink = append(gcSink[:0], buf)
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
	amd64 := runtime.GOARCH == "amd64"
	wantA := "a-standalone"
	wantB := "b->a"
	wantOrder := []string{"trace", "a", "b", "main"}
	if amd64 {
		wantA = "a->b"
		wantB = "b-standalone"
		wantOrder = []string{"trace", "b", "a", "main"}
	}
	p := &payloadNode{id: 33, name: "cycle-goarch"}

	ck(a.Edge() == wantA, fmt.Sprintf("a edge got=%q want=%q (GOARCH=%s)", a.Edge(), wantA, runtime.GOARCH))
	ck(b.Edge() == wantB, fmt.Sprintf("b edge got=%q want=%q", b.Edge(), wantB))

	seq := trace.Snapshot()
	ck(len(seq) == 4, fmt.Sprintf("init markers got=%d want=4", len(seq)))
	for i, name := range wantOrder {
		got := "<missing>"
		if i < len(seq) {
			got = seq[i]
		}
		ck(got == name, fmt.Sprintf("init slot %d got=%q want=%q", i, got, name))
	}

	gcStress()
	ck(a.Edge() == wantA && b.Edge() == wantB, "tag-selected edges after GC")
	ck(p.id == 33 && p.name == "cycle-goarch", "payload after GC")

	rounds := envInt("TORTURE_ROUNDS", 20000)
	seed := envInt("UAF_SEED", 1)
	wantLen := len(wantA) + 1 + len(wantB)
	gcEvery := rounds / 16
	if gcEvery < 1 {
		gcEvery = 1
	}
	var sum int
	for r := 0; r < rounds; r++ {
		s := a.Edge() + "|" + b.Edge()
		if len(s) != wantLen {
			ck(false, fmt.Sprintf("edge length round=%d seed=%d got=%d want=%d", r, seed, len(s), wantLen))
			break
		}
		sum += len(s)
		if r%256 == 0 {
			buf := make([]byte, 1024)
			buf[0] = byte(r)
			gcSink = append(gcSink[:0], buf)
		}
		if r%gcEvery == 0 {
			runtime.GC()
		}
	}
	ck(sum == rounds*wantLen, fmt.Sprintf("torture checksum got=%d want=%d seed=%d", sum, rounds*wantLen, seed))

	runtime.GC()
	runtime.GC()
	ck(a.Edge() == wantA && b.Edge() == wantB, "tag-selected edges after final GC")
	ck(len(trace.Snapshot()) == 4, "init trace unchanged after GC")
	finish("l15_cycle_goarch_edges")
}
