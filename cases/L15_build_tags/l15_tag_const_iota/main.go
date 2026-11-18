// L15: tag 控制常量（iota / 类型化常量 / 跨文件常量表达式）。GOOS 选择的文件
// 各自用 iota 定义 Level 枚举与 flavorBase，再由同一文件里的常量表达式派生
// derivedMask/derivedNext；main 用这些常量作数组长度、下标、switch 分支，
// 因此常量错误会直接表现为编译失败或运行期长度/取值不符。
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
	// 数组长度来自 tag 选择文件里的常量表达式。
	levelTable [derivedNext]string
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

func levelLabel(l Level) string {
	switch l {
	case LvLow:
		return "low"
	case LvMid:
		return "mid"
	case LvHigh:
		return "high"
	default:
		return "unknown"
	}
}

func main() {
	low, mid, high, base, mask, next := 1, 2, 3, 10, 22, 4
	flavor := "linux"
	if runtime.GOOS != "linux" {
		low, mid, high, base, mask, next = 4, 5, 6, 20, 45, 7
		flavor = "other"
	}

	p := &payloadNode{id: 111, name: "tag-const-iota"}

	ck(int(LvLow) == low, "iota start selected by tag")
	ck(int(LvMid) == mid, "iota continuation")
	ck(int(LvHigh) == high, "iota third value")
	ck(flavorName == flavor, "tag-selected string constant")
	ck(flavorBase == base, "tag-selected integer constant")
	ck(derivedMask == mask, "constant expression over selected constants")
	ck(derivedNext == next, "constant expression with conversion")
	ck(int(LvHigh-LvLow) == 2, "typed constant subtraction")
	ck(len(levelTable) == next, "array length uses tag-selected constant")

	levelTable[LvLow] = "low"
	levelTable[LvMid] = "mid"
	levelTable[LvHigh] = "high"
	ck(levelTable[0] == "", "array slot below first level untouched")
	ck(levelTable[LvLow] == "low" && levelTable[LvMid] == "mid" && levelTable[LvHigh] == "high",
		"typed enum values used as array indices")
	ck(levelLabel(LvLow) == "low" && levelLabel(LvMid) == "mid" && levelLabel(LvHigh) == "high",
		"switch cases use tag-selected constants")
	ck(levelLabel(Level(0)) == "unknown", "non-enum value falls through switch")

	gcStress()
	ck(len(levelTable) == next && levelTable[LvHigh] == "high", "constant-sized array after GC")
	ck(derivedMask == mask && derivedNext == next && flavorName == flavor, "constants after GC")
	ck(p.id == 111 && p.name == "tag-const-iota", "payload after GC")
	finish("l15_tag_const_iota")
}
