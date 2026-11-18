// L11: 编译期常量表达式（大整数/位运算/移位/字符串常量）与运行时逐项计算
// 必须完全一致；常量数组长度、数组下标与 GC 后的堆数据也逐项校验。
package main

import (
	"fmt"
	"os"
	"runtime"
	"sync/atomic"
)

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
		b[len(b)-1] = byte(255 - i)
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

const (
	cA   uint64 = 1 << 40
	cB   uint64 = cA*7919 + 12345
	cC   uint64 = cB % 1000003
	cD   uint64 = (cA | cB) &^ 0xFF
	cE   uint64 = cC ^ cD
	cF   uint64 = ^uint64(0) >> 7
	cG   uint64 = (cB + cE) * 3 % (1 << 32)
	cH   int64  = -1024 >> 3
	cI   int64  = -1024 / 8
	cJ   uint32 = ^uint32(0) & 0xF0F0F0F0
	cK   uint64 = (12345 + 4095) &^ 4095
	cSh  uint   = 7
	cL   uint64 = (1 << cSh) * 129
	cM   uint64 = cB>>17<<3 | 1
	cN   uint64 = 1<<63 - 1 + 1
	cNam        = "const" + "-" + "fold"
	cNL         = len(cNam)
	cNR         = 'A' + 25
	cGrid       = 16
	cArrLen     = len(cNam) * cGrid
)

// 运行期变量版本：不依赖编译期折叠，用来与常量逐项对照。
var (
	rA = uint64(1) << 40
	rB = rA*7919 + 12345
	rC = rB % 1000003
	rD = (rA | rB) &^ 0xFF
	rE = rC ^ rD
	rF = ^uint64(0) >> 7
	rG = (rB + rE) * 3 % (1 << 32)
	rH = int64(-1024) >> 3
	rI = int64(-1024) / 8
	rJ = ^uint32(0) & 0xF0F0F0F0
	rK = (uint64(12345) + 4095) &^ 4095
	rL = (uint64(1) << cSh) * 129
	rM = rB>>17<<3 | 1
	rN = uint64(1) << 63
)

// foldRuntime 从 1 出发逐步移位/运算，保证值在运行期产生。
func foldRuntime() []uint64 {
	a := uint64(1)
	for i := 0; i < 40; i++ {
		a <<= 1
	}
	b := a*7919 + 12345
	c := b % 1000003
	d := (a | b) &^ 0xFF
	e := c ^ d
	f := ^uint64(0) >> 7
	g := (b + e) * 3 % (1 << 32)
	m := b>>17<<3 | 1
	return []uint64{a, b, c, d, e, f, g, m}
}

func main() {
	consts := []uint64{cA, cB, cC, cD, cE, cF, cG, cM}
	runtimeVals := foldRuntime()
	ck(len(consts) == len(runtimeVals), "constant table size mismatch")
	for i := 0; i < len(consts) && i < len(runtimeVals); i++ {
		ck(consts[i] == runtimeVals[i], "const vs runtime mismatch")
	}

	ck(cA == rA, "cA mismatch")
	ck(cB == rB, "cB mismatch")
	ck(cC == rC, "cC mismatch")
	ck(cD == rD, "cD mismatch")
	ck(cE == rE, "cE mismatch")
	ck(cF == rF, "cF mismatch")
	ck(cG == rG, "cG mismatch")
	ck(cH == rH, "cH mismatch")
	ck(cI == rI, "cI mismatch")
	ck(cJ == rJ, "cJ mismatch")
	ck(cK == rK, "cK mismatch")
	ck(cL == rL, "cL mismatch")
	ck(cM == rM, "cM mismatch")
	ck(cN == rN, "cN mismatch")

	ck(cH == -128 && cI == -128, "negative shift/division mismatch")
	ck(cJ == 0xF0F0F0F0, "uint32 mask mismatch")
	ck(cK == 16384, "round-up constant mismatch")
	ck(cL == 16512, "shifted constant mismatch")
	ck(cF == 144115188075855871, "max-shift constant mismatch")
	ck(cN == 1<<63, "large constant mismatch")
	ck(cNL == 10, "string constant length mismatch")
	ck(cNam[0] == 'c', "string constant first byte mismatch")
	ck(cNR == 'Z', "rune constant mismatch")
	ck(cNam[5] == '-', "string constant index mismatch")
	ck(len(cNam) == cNL, "len(constant string) mismatch")

	var arr [cArrLen]int64
	for i := range arr {
		arr[i] = int64(i) * 3
	}
	total := int64(0)
	for _, v := range arr {
		total += v
	}
	wantTotal := int64(cArrLen) * int64(cArrLen-1) / 2 * 3
	ck(total == wantTotal, "constant-sized array sum mismatch")
	ck(len(arr) == 160, "constant-sized array length mismatch")

	var grid [cGrid][cGrid]int64
	for i := 0; i < cGrid; i++ {
		for j := 0; j < cGrid; j++ {
			grid[i][j] = int64(i*cGrid + j)
		}
	}
	gcStress()
	gsum := int64(0)
	for i := 0; i < cGrid; i++ {
		for j := 0; j < cGrid; j++ {
			gsum += grid[i][j]
		}
	}
	ck(gsum == int64(cGrid*cGrid*(cGrid*cGrid-1)/2), "constant grid sum corrupted after GC")

	// 运行期回绕（不是常量表达式，避免编译期溢出诊断）。
	var u8 uint8 = 255
	u8++
	ck(u8 == 0, "uint8 wrap mismatch")
	var i8 int8 = 127
	i8++
	ck(i8 == -128, "int8 wrap mismatch")
	var u32 uint32 = 1<<32 - 1
	u32 += 2
	ck(u32 == 1, "uint32 wrap mismatch")
	var u64 uint64 = 1<<63 - 1
	u64++
	ck(u64 == 1<<63, "uint64 carry mismatch")

	heap := make([]uint64, len(consts))
	copy(heap, consts)
	gcStress()
	for i := range heap {
		ck(heap[i] == runtimeVals[i], "constant values corrupted after GC")
	}

	finish("l11_const_fold")
}
