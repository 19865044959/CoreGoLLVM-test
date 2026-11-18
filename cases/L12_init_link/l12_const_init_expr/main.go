// L12: const 初始化。常量链、typed/untyped 默认类型、移位、字符串/rune/
// float/complex 常量、min/max 常量折叠，以及常量作为数组长度与 init 期
// 表值。主程序校验常量值与默认类型，并在 GC 后复核 init 填充的表。
package main

import (
	"fmt"
	"os"
	"runtime"
	"strconv"
	"sync"
	"sync/atomic"
)

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

func isInt(v any) bool     { _, ok := v.(int); return ok }
func isFloat64(v any) bool { _, ok := v.(float64); return ok }
func isRune(v any) bool    { _, ok := v.(int32); return ok }
func isString(v any) bool  { _, ok := v.(string); return ok }
func isComplex(v any) bool { _, ok := v.(complex128); return ok }
func isUint8(v any) bool   { _, ok := v.(uint8); return ok }

func checkConsts() {
	ck(base == 7 && doubled == 14, "constant chain")
	ck(shifted == 1024 && mask == 1023, "shift and mask constants")
	ck(helloLen == 5, "len of constant string")
	ck(tag == "hello!", "constant string concatenation")
	ck(letter == 'A' && letterStr == "A", "rune constant and string conversion")
	ck(rounded == 6.0, "float constant expression")
	ck(creal == 2 && cimag == 3, "complex constant parts")
	ck(lo == 1 && hi == 3, "min/max constant folding")
	ck(arrLen == 16, "constant array length")
	ck(u8max == 255, "typed uint8 constant")
	ck(modeA == 0 && modeB == 1 && modeC == 2, "iota constant block")
	ck(modeShift == 16, "iota-dependent shift constant")

	ck(isInt(defInt) && defInt == 14, "untyped int default type")
	ck(isFloat64(defFloat) && defFloat == 6.0, "untyped float default type")
	ck(isRune(defRune) && defRune == 'A', "untyped rune default type")
	ck(isString(defStr) && defStr == "hello!", "untyped string default type")
	ck(isComplex(defCplx) && real(defCplx) == 2 && imag(defCplx) == 3,
		"untyped complex default type")
	ck(isUint8(defU8) && defU8 == 255, "typed constant keeps its type")
	ck(narrow == 10, "runtime conversion wraps 266 to int8")

	ck(len(table) == int(arrLen), "array length from constant")
	ck(len(strTable) == 3, "string table length")
	ck(strTable[0] == "A" && strTable[1] == "hello!" && strTable[2] == "hello",
		"constant-initialized string table")
	for i := 0; i < len(table); i++ {
		want := i*int(doubled) + int(mask%97) + int(u8max)%7
		ck(table[i] == want, "init table value from constants")
	}
	ck(initSum == 2576, "init sum computed from constant-filled table")
}

func main() {
	seedVal = readSeed()
	seedStr = strconv.FormatInt(seedVal, 10)

	checkConsts()

	// Concurrent reads of the init-filled table plus explicit GC.
	var wg sync.WaitGroup
	var bad int32
	start := int(seedVal % int64(arrLen))
	for w := 0; w < 8; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < 200; i++ {
				for j := start; j < len(table); j += 4 {
					want := j*int(doubled) + int(mask%97) + int(u8max)%7
					if table[j] != want {
						atomic.AddInt32(&bad, 1)
						return
					}
				}
				_ = (w + i) % int(arrLen)
			}
		}(w)
	}
	for i := 0; i < 8; i++ {
		gcSink = append(gcSink, make([]byte, 8192))
		runtime.GC()
	}
	wg.Wait()
	ck(atomic.LoadInt32(&bad) == 0, "constant table stable under concurrent GC")
	gcSink = gcSink[:0]

	gcStress()
	checkConsts()
	ck(initSum == 2576, "init sum survives GC")

	finish("l12_const_init_expr")
}
