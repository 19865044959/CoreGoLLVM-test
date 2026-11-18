// L15: module 变量依赖 + tag 选择的常量。conf 的包级变量初始化器读取
// GOOS 选择的 PlatformBias/PlatformName，mid/out/main 的变量沿依赖链逐层
// 派生；main 校验值链、包间 init 顺序、指针切片，并在 TORTURE_ROUNDS 轮
// 的 GC 压力下反复遍历 tag 参与构造的对象图。
package main

import (
	"fmt"
	"os"
	"runtime"
	"strconv"
	"sync/atomic"

	"l15_module_tag_var_chain/conf"
	"l15_module_tag_var_chain/mid"
	"l15_module_tag_var_chain/out"
	"l15_module_tag_var_chain/trace"
)

type payloadNode struct {
	id   int64
	name string
}

var (
	failed int32
	gcSink [][]byte
	// main 的包级变量依赖 out/conf 的初始化结果。
	mainValue = out.Total + int64(conf.PlatformBias)
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
	bias := int64(7)
	name := "linux"
	if runtime.GOOS != "linux" {
		bias = 3
		name = "other"
	}
	base := bias * 2
	scale := base + 1
	total := scale*2 + base

	p := &payloadNode{id: 71, name: "module-tag-var"}

	ck(conf.PlatformBias == bias, "tag-selected constant PlatformBias")
	ck(conf.PlatformName == name, "tag-selected constant PlatformName")
	ck(conf.Base == base, "conf var depends on tag-selected constant")
	ck(mid.Scale == scale, "mid var depends on conf var")
	ck(len(mid.Table) == 3 && mid.Table[0] == base && mid.Table[1] == base*2 && mid.Table[2] == scale,
		"mid table built from conf var")
	ck(out.Total == total, "out var depends on mid and conf")
	ck(mainValue == total+bias, "main var depends on out and conf")
	ck(out.Start == int(bias)%3, "start index uses tag-selected constant")

	ck(len(out.Items) == 3, "out item slice length")
	labels := [3]string{name, "mid", "out"}
	ids := [3]int64{bias, scale, total}
	for i := 0; i < 3; i++ {
		ck(out.Items[i] != nil && out.Items[i].ID == ids[i] && out.Items[i].Label == labels[i],
			fmt.Sprintf("item[%d] identity", i))
	}
	ck(conf.Node.Next != nil && conf.Node.Next.ID == base && conf.Node.Next.Label == name+"-next",
		"conf node chain built in init")

	seq := trace.Snapshot()
	ck(len(seq) == 5, "exactly five package init markers")
	for _, pkg := range []string{"trace", "conf", "mid", "out", "main"} {
		ck(countOf(seq, pkg) == 1, "init marker "+pkg+" appears exactly once")
	}
	ck(indexOf(seq, "trace") == 0, "trace initialized first")
	ck(indexOf(seq, "conf") > indexOf(seq, "trace"), "conf after trace")
	ck(indexOf(seq, "mid") > indexOf(seq, "conf"), "mid after conf")
	ck(indexOf(seq, "out") > indexOf(seq, "mid"), "out after mid")
	ck(indexOf(seq, "main") > indexOf(seq, "out"), "main after out")

	rounds := envInt("TORTURE_ROUNDS", 4000)
	seed := envInt("UAF_SEED", 1)
	var sum int64
	gcEvery := rounds / 16
	if gcEvery < 1 {
		gcEvery = 1
	}
	for r := 0; r < rounds; r++ {
		start := (r + seed) % 3
		it := out.Items[start]
		if it == nil || it.ID != ids[start] || it.Label != labels[start] {
			ck(false, fmt.Sprintf("item corrupted round=%d start=%d seed=%d", r, start, seed))
			break
		}
		sum += it.ID
		if r%64 == 0 {
			buf := make([]byte, 1024)
			buf[0] = byte(r)
			gcSink = append(gcSink[:0], buf)
		}
		if r%gcEvery == 0 {
			runtime.GC()
		}
	}

	// 独立闭式公式：ids 以 3 为周期从 seed%3 开始循环。
	cycle := ids[seed%3] + ids[(seed+1)%3] + ids[(seed+2)%3]
	wantSum := int64(rounds/3)*cycle
	for i := 0; i < rounds%3; i++ {
		wantSum += ids[(seed+i)%3]
	}
	ck(sum == wantSum, fmt.Sprintf("item checksum mismatch seed=%d rounds=%d", seed, rounds))

	runtime.GC()
	runtime.GC()
	for i := 0; i < 3; i++ {
		ck(out.Items[i] != nil && out.Items[i].ID == ids[i] && out.Items[i].Label == labels[i],
			fmt.Sprintf("item[%d] after GC", i))
	}
	ck(conf.Node.Next != nil && conf.Node.Next.Label == name+"-next", "conf chain after GC")
	ck(mainValue == total+bias && p.id == 71 && p.name == "module-tag-var", "derived values after GC")
	finish("l15_module_tag_var_chain")
}
