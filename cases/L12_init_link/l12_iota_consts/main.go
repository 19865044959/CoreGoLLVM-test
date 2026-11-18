// L12: typed/untyped 常量、iota、位移与常量表达式组合。
package main

import (
	"fmt"
	"os"
	"reflect"
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
	// typed iota 枚举。
	ck(Sunday == 0 && Monday == 1 && Tuesday == 2, "weekday iota head")
	ck(Wednesday == 3 && Thursday == 4 && Friday == 5 && Saturday == 6, "weekday iota tail")
	var w weekday = Tuesday
	switch w {
	case Sunday:
		ck(false, "switch picked Sunday")
	case Tuesday:
		ck(true, "typed weekday switch")
	default:
		ck(false, "switch fell through")
	}

	// typed 位标志 + 位移。
	var all perm = PermRead | PermWrite | PermExec
	ck(PermRead == 1 && PermWrite == 2 && PermExec == 4, "perm iota shifts")
	ck(all == 7, "perm OR combination")
	ck(PermRead&PermWrite == 0 && PermRead&PermExec == 0, "perm bits disjoint")
	ck(all&PermWrite != 0, "perm bit test")
	ck(PermRead < PermWrite && PermWrite < PermExec, "perm ordering")

	// iota 配合移位与空白占位。
	ck(KB == 1<<10 && MB == 1<<20 && GB == 1<<30, "KB/MB/GB shift iota")
	ck(P == 0 && Q == 0 && R == 1 && S == 10, "multi-const per line iota")
	ck(first == 1 && third == 3, "blank iota slot")

	// untyped/typed 常量转换与截断。
	ck(Greeting == "go1.24" && len(Greeting) == 6, "string constant concatenation")
	ck(Int62 == 1<<62, "typed int64 constant")
	ck(ExactInt == 4, "exact constant float to int conversion")
	frac := 1.9
	ck(int(frac) == 1, "runtime float to int truncation")
	ck(U64Shift == uint64(1)<<63, "typed uint64 shift")
	ck(untIntDiv == 0, "untyped integer division")
	ck(untFloatDiv == 0.5, "untyped float division")
	ck(smallPerm == 3, "typed constant conversion")
	ck(ShiftA == 4 && ShiftB == 8 && ShiftC == 16, "shift expression with iota")

	// 常量可作为数组长度。
	var buf [ArrLen]int
	ck(len(buf) == 6, "constant array length")
	buf[ArrLen-1] = 9
	ck(buf[5] == 9, "array indexed by constant expression")

	// 无类型常量的默认类型规则。
	var (
		iv interface{} = 7
		fv interface{} = 1.5
		sv interface{} = "s"
		bv interface{} = true
		rv interface{} = 'x'
	)
	ck(reflect.TypeOf(iv).Kind() == reflect.Int, "untyped int defaults to int")
	ck(reflect.TypeOf(fv).Kind() == reflect.Float64, "untyped float defaults to float64")
	ck(reflect.TypeOf(sv).Kind() == reflect.String, "untyped string defaults to string")
	ck(reflect.TypeOf(bv).Kind() == reflect.Bool, "untyped bool defaults to bool")
	ck(reflect.TypeOf(rv).Kind() == reflect.Int32, "untyped rune defaults to int32")

	gcStress()
	ck(Saturday == 6 && all == 7 && GB == 1<<30, "constants intact after GC")
	ck(len(buf) == 6 && buf[5] == 9, "array intact after GC")
	ck(Int62 == 1<<62 && U64Shift == uint64(1)<<63, "wide constants intact after GC")

	finish("l12_iota_consts")
}
