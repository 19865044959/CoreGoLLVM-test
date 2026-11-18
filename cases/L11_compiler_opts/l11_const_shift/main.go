// L11: 常量移位——任意精度常量移位、常量数组长度、运行期 0..127 位移、
// 窄类型截断、符号右移与负移位计数 panic 必须与参考语义逐项一致。
package main

import (
	"fmt"
	"math/big"
	"os"
	"runtime"
	"strconv"
	"strings"
	"sync/atomic"
)

var (
	failed int32
	gcSink [][]byte
	seed   uint64 = 1
)

const (
	csBase    = 1 << 4
	csScaled  = 3 << 10
	csNeg     = -5 << 3
	csRight   = -64 >> 3
	csMask    = (1 << 63) - 1
	csHuge    = 1 << 100
	csNegHuge = -1 << 100
	csArrLen  = 1 << 5
	csTop     = 1 << 63
	csIdx     = (1 << 4) + 1
)

var csArray = [csArrLen]byte{}
var csMatrix [1 << 3][1 << 2]int64

func ck(ok bool, msg string) {
	if !ok {
		atomic.AddInt32(&failed, 1)
		fmt.Printf("FAIL: %s (UAF_SEED=%d)\n", msg, seed)
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

func loadSeed() uint64 {
	s := os.Getenv("UAF_SEED")
	if s == "" {
		return 1
	}
	n, err := strconv.ParseUint(s, 10, 64)
	if err != nil {
		return 1
	}
	return n
}

func finish(name string) {
	if n := atomic.LoadInt32(&failed); n != 0 {
		fmt.Printf("FAILED %s (%d failed checks, seed=%d)\n", name, n, seed)
		os.Exit(1)
	}
	fmt.Println("ALL PASS " + name)
}

func shiftLeft64(x int64, s uint) int64     { return x << s }
func shiftRight64(x int64, s uint) int64    { return x >> s }
func shiftLeftU64(x uint64, s uint) uint64  { return x << s }
func shiftRightU64(x uint64, s uint) uint64 { return x >> s }
func shiftLeft8(x int8, s uint) int8        { return x << s }
func shiftRight8(x int8, s uint) int8       { return x >> s }
func shiftLeftU8(x uint8, s uint) uint8     { return x << s }
func shiftRightU8(x uint8, s uint) uint8    { return x >> s }

func refLeft64(x int64, s uint) int64 {
	if s >= 64 {
		return 0
	}
	z := new(big.Int).Lsh(big.NewInt(x), s)
	z.Mod(z, new(big.Int).Lsh(big.NewInt(1), 64))
	return int64(z.Uint64())
}

func refRight64(x int64, s uint) int64 {
	if s >= 64 {
		if x < 0 {
			return -1
		}
		return 0
	}
	return new(big.Int).Rsh(big.NewInt(x), s).Int64()
}

func refLeftU64(x uint64, s uint) uint64 {
	if s >= 64 {
		return 0
	}
	z := new(big.Int).SetUint64(x)
	z.Lsh(z, s)
	z.Mod(z, new(big.Int).Lsh(big.NewInt(1), 64))
	return z.Uint64()
}

func refRightU64(x uint64, s uint) uint64 {
	if s >= 64 {
		return 0
	}
	return new(big.Int).Rsh(new(big.Int).SetUint64(x), s).Uint64()
}

func refLeft8(x int8, s uint) int8     { return int8(refLeft64(int64(x), s)) }
func refRight8(x int8, s uint) int8    { return int8(refRight64(int64(x), s)) }
func refLeftU8(x uint8, s uint) uint8  { return uint8(refLeftU64(uint64(x), s)) }
func refRightU8(x uint8, s uint) uint8 { return uint8(refRightU64(uint64(x), s)) }

func constShiftClass(x int) int {
	switch x {
	case 1 << 3:
		return 1
	case 3 << 4:
		return 2
	case -1 << 5:
		return 3
	default:
		return 0
	}
}

//go:noinline
func negativeShiftCount() int { return -1 }

func negShiftPanic() (out string) {
	defer func() {
		if r := recover(); r != nil {
			out = fmt.Sprint(r)
		}
	}()
	s := negativeShiftCount()
	_ = 1 << s
	return "no-panic"
}

func main() {
	seed = loadSeed()

	// 编译期常量移位。
	ck(csBase == 16, "const 1<<4 mismatch")
	ck(csScaled/3 == 1<<10, "const 3<<10 mismatch")
	ck(csNeg == -40, "const -5<<3 mismatch")
	ck(csRight == -8, "const -64>>3 mismatch")
	ck(csMask == 1<<63-1, "const (1<<63)-1 mismatch")
	ck(uint64(csTop) == 1<<63, "const top bit mismatch")
	ck(csTop>>63 == 1, "const top>>63 mismatch")
	ck(csHuge>>100 == 1, "const 1<<100 >> 100 mismatch")
	ck(csHuge>>99 == 2, "const 1<<100 >> 99 mismatch")
	ck(csHuge>>64 == 1<<36, "const 1<<100 >> 64 mismatch")
	ck(csNegHuge>>100 == -1, "const -1<<100 >> 100 mismatch")
	ck((csHuge+csHuge)>>101 == 1, "const sum shift mismatch")
	ck(len(csArray) == 32, "constant array length mismatch")

	// 常量作为数组下标与 case 标签。
	for i := 0; i < csArrLen; i++ {
		csArray[i] = byte(i << 1)
	}
	ck(csArray[csIdx] == byte(csIdx<<1), "constant array index mismatch")
	ck(constShiftClass(1<<3) == 1 && constShiftClass(3<<4) == 2 && constShiftClass(-1<<5) == 3, "constant shift case label mismatch")
	ck(constShiftClass(7) == 0, "constant shift default mismatch")

	masks := map[uint64]string{1 << 63: "top", 1 << 32: "mid", (1 << 8) - 1: "low"}
	ck(masks[uint64(csTop)] == "top" && masks[1<<32] == "mid" && masks[255] == "low", "constant shift map key mismatch")

	// 常量维度的二维数组。
	for i := 0; i < len(csMatrix); i++ {
		for j := 0; j < len(csMatrix[i]); j++ {
			csMatrix[i][j] = int64(i<<4 + j)
		}
	}
	var matrixSum int64
	for i := range csMatrix {
		for j := range csMatrix[i] {
			matrixSum += csMatrix[i][j]
		}
	}
	ck(matrixSum == 1840, "constant matrix sum mismatch")

	// 运行期移位与 big.Int 参考逐项对照。
	xs := []int64{-1, 0, 1, 5, -5, 12345, -12345, 1 << 62, -(1 << 62), int64(seed%100000) - 50000}
	shifts := []uint{0, 1, 7, 8, 15, 31, 32, 62, 63, 64, 65, 100, 127}
	var kept []int64
	for _, x := range xs {
		for _, s := range shifts {
			ck(shiftLeft64(x, s) == refLeft64(x, s), "int64 left shift mismatch")
			ck(shiftRight64(x, s) == refRight64(x, s), "int64 right shift mismatch")
			kept = append(kept, shiftLeft64(x, s))
		}
	}

	us := []uint64{0, 1, 2, 1 << 63, ^uint64(0), uint64(seed) * 2654435761}
	for _, x := range us {
		for _, s := range shifts {
			ck(shiftLeftU64(x, s) == refLeftU64(x, s), "uint64 left shift mismatch")
			ck(shiftRightU64(x, s) == refRightU64(x, s), "uint64 right shift mismatch")
		}
	}

	i8s := []int8{-128, -7, -1, 0, 1, 7, 127}
	u8s := []uint8{0, 1, 127, 128, 255}
	shifts8 := []uint{0, 1, 6, 7, 8, 9, 15, 63, 64, 100}
	for _, x := range i8s {
		for _, s := range shifts8 {
			ck(shiftLeft8(x, s) == refLeft8(x, s), "int8 left shift mismatch")
			ck(shiftRight8(x, s) == refRight8(x, s), "int8 right shift mismatch")
		}
	}
	for _, x := range u8s {
		for _, s := range shifts8 {
			ck(shiftLeftU8(x, s) == refLeftU8(x, s), "uint8 left shift mismatch")
			ck(shiftRightU8(x, s) == refRightU8(x, s), "uint8 right shift mismatch")
		}
	}

	// 超大移位计数（>= 位宽）不 panic，结果为 0 或符号填充。
	huge := ^uint(0)
	ck(shiftLeft64(-5, huge) == 0, "huge-count int64 left shift mismatch")
	ck(shiftRight64(-5, huge) == -1, "huge-count int64 right shift mismatch")
	ck(shiftLeftU64(7, huge) == 0, "huge-count uint64 left shift mismatch")
	ck(shiftRightU64(7, huge) == 0, "huge-count uint64 right shift mismatch")
	ck(shiftLeft8(-5, huge) == 0, "huge-count int8 left shift mismatch")
	ck(shiftRight8(-5, huge) == -1, "huge-count int8 right shift mismatch")

	// 负移位计数运行期 panic。
	out := negShiftPanic()
	ck(out != "no-panic" && strings.Contains(out, "negative shift amount"), "negative shift count did not panic")

	// 常量数组与移位结果跨 GC。
	gcStress()
	ck(csArray[csIdx] == byte(csIdx<<1), "constant array corrupted after GC")
	for i := csIdx; i < csArrLen; i++ {
		ck(csArray[i] == byte(i<<1), "constant array tail corrupted after GC")
	}
	for i := range csMatrix {
		for j := range csMatrix[i] {
			ck(csMatrix[i][j] == int64(i<<4+j), "constant matrix corrupted after GC")
		}
	}
	idx := 0
	for _, x := range xs {
		for _, s := range shifts {
			if kept[idx] != refLeft64(x, s) {
				ck(false, "retained shift result corrupted after GC")
			}
			idx++
		}
	}

	finish("l11_const_shift")
}
