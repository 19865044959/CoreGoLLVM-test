// L12: const 链跨包传播（module）。unit 导出 typed/untyped 常量，shape 用
// 它们做数组长度与 const 表达式，main 再用两个子包的常量计算数组长度与
// init 填表公式。验证编译期常量传播、类型保持与运行期截断。
package main

import (
	"fmt"
	"os"
	"runtime"
	"strconv"
	"sync"
	"sync/atomic"

	"l12_crosspkg_const_chain/shape"
	"l12_crosspkg_const_chain/unit"
)

const (
	TotalLen  = unit.MaxLen + shape.ShapeArea // 48 + 96
	TableMask = unit.Mask
	Derived   = TotalLen*2 + unit.KindCount // 288 + 3

	typedWidth unit.Kind = unit.KindLarge
)

var table [TotalLen]int64

func init() {
	for i := range table {
		table[i] = int64(i%int(TableMask)) + int64(unit.Scale)
	}
}

var (
	failed  int32
	gcSink  [][]byte
	seedVal int64
	seedStr string
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
		fmt.Printf("FAILED %s (%d failed checks) seed=%s\n", name, n, seedStr)
		os.Exit(1)
	}
	fmt.Println("ALL PASS " + name)
}

func readSeed() int64 {
	s := os.Getenv("UAF_SEED")
	if s == "" {
		return 1
	}
	v, err := strconv.ParseInt(s, 10, 64)
	if err != nil || v < 0 {
		return 1
	}
	return v
}

func kindLabel(k unit.Kind) string {
	switch k {
	case unit.KindNone:
		return "none"
	case unit.KindSmall:
		return "small"
	case unit.KindLarge:
		return "large"
	default:
		return "other"
	}
}

func main() {
	seedVal = readSeed()
	seedStr = strconv.FormatInt(seedVal, 10)

	ck(unit.Shift == 4, "unit.Shift")
	ck(unit.Scale == 16, "unit.Scale")
	ck(unit.Mask == 15, "unit.Mask")
	ck(unit.MaxLen == 48, "unit.MaxLen")
	ck(unit.Combined == 33, "unit.Combined")
	ck(unit.KindCount == 3, "unit.KindCount")
	ck(unit.Weighted == 20, "unit.Weighted")

	// Typed constants keep their defined type; untyped constants take their
	// default type when boxed for %T.
	ck(fmt.Sprintf("%T", typedWidth) == "unit.Kind", "typedWidth type")
	ck(fmt.Sprintf("%T", unit.KindSmall) == "unit.Kind", "typed iota constant type")
	ck(fmt.Sprintf("%T", unit.Shift) == "int", "untyped constant default type")
	ck(fmt.Sprintf("%T", unit.Weighted) == "unit.Priority", "weighted constant type")

	// Cross-package const propagation.
	ck(shape.ShapeScale == 32, "shape.ShapeScale")
	ck(shape.ShapeArea == 96, "shape.ShapeArea")
	ck(shape.ShapeMask == 15, "shape.ShapeMask")
	ck(shape.KindOfShape == unit.KindLarge, "shape.KindOfShape")
	ck(TotalLen == 144 && Derived == 291, "main const chain")

	ck(len(shape.Labels) == 48, "shape.Labels length from imported const")
	ck(len(shape.Defaults) == 48, "shape.Defaults length from imported const")
	ck(len(table) == 144, "main table length from two-package const sum")
	ck(shape.InitDefaultsOK, "shape init saw wrong array lengths")
	for i := 0; i < 48; i++ {
		if shape.Labels[i] != fmt.Sprintf("k%02d", i) {
			ck(false, fmt.Sprintf("label %d = %q", i, shape.Labels[i]))
			break
		}
		if shape.Defaults[i] != int32(i)*int32(unit.Scale)+int32(shape.ShapeScale) {
			ck(false, fmt.Sprintf("default %d = %d", i, shape.Defaults[i]))
			break
		}
	}
	for i := 0; i < len(table); i++ {
		if table[i] != int64(i%15)+16 {
			ck(false, fmt.Sprintf("table %d = %d", i, table[i]))
			break
		}
	}

	ck(shape.KindNames[unit.KindNone] == "none", "typed const map key none")
	ck(shape.KindNames[unit.KindSmall] == "small", "typed const map key small")
	ck(shape.KindNames[unit.KindLarge] == "large", "typed const map key large")
	ck(kindLabel(unit.KindLarge) == "large", "switch on typed const")
	ck(kindLabel(unit.Combined&0x3) == "small", "const bit expression")

	// Non-constant conversions truncate at run time; constant conversions of
	// these values would be compile errors.
	rawKind := 300
	k := unit.Kind(rawKind)
	rawPri := 70000
	p := unit.Priority(rawPri)
	ck(k == 44, fmt.Sprintf("runtime uint8 conversion %d", k))
	ck(p == 4464, fmt.Sprintf("runtime int16 conversion %d", p))

	// Read-only concurrent view of const-derived tables with explicit GC.
	var wg sync.WaitGroup
	var bad int32
	workers := 8
	start := int(seedVal % 144)
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < 300; i++ {
				idx := (start + w*9 + i) % 144
				if table[idx] != int64(idx%15)+16 {
					atomic.AddInt32(&bad, 1)
					return
				}
				j := idx % 48
				if shape.Defaults[j] != int32(j)*16+32 || shape.Labels[j] != fmt.Sprintf("k%02d", j) {
					atomic.AddInt32(&bad, 1)
					return
				}
				if shape.KindNames[unit.Kind(idx%3)] == "" {
					atomic.AddInt32(&bad, 1)
					return
				}
			}
		}(w)
	}
	for r := 0; r < 3; r++ {
		for i := 0; i < 16; i++ {
			b := make([]byte, 8192)
			b[i%len(b)] = byte(r)
			gcSink = append(gcSink[:0], b)
		}
		runtime.GC()
	}
	wg.Wait()
	gcSink = gcSink[:0]
	ck(atomic.LoadInt32(&bad) == 0, "concurrent const-derived table read failed under GC")

	gcStress()
	ck(len(table) == 144 && table[143] == int64(143%15)+16, "main table changed after GC")
	ck(shape.Labels[47] == "k47" && shape.Defaults[47] == int32(47)*16+32, "shape tables changed after GC")
	ck(shape.KindOfShape == unit.KindLarge && unit.Combined == 33, "constants changed after GC")

	finish("l12_crosspkg_const_chain")
}
