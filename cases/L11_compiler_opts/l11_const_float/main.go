// L11: 浮点常量折叠——无类型常量按任意精度计算、单次舍入，运行期
// float64 运算按 IEEE 逐步舍入；float32/整数转换、复数常量、±0 与
// GC 后保留值的位模式必须与 Go 规范一致。
package main

import (
	"fmt"
	"math"
	"os"
	"runtime"
	"strconv"
	"sync/atomic"
)

const (
	cfSum         = 0.1 + 0.2
	cfCancelConst = 0.1 + 0.2 - 0.3
	cfThird       = 1.0 / 3.0
	cf32          = float32(0.1)
	cfBig         = float64(1<<53 + 1)
	cfC           = (1 + 2i) * (3 - 4i)
)

var (
	failed int32
	gcSink [][]byte
	seed   uint64 = 1
)

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

//go:noinline
func cfAdd(a, b float64) float64 { return a + b }

//go:noinline
func cfCancelRT(a, b, c float64) float64 { return (a + b) - c }

//go:noinline
func cfDiv(a, b float64) float64 { return a / b }

//go:noinline
func cfToFloat32(a float64) float32 { return float32(a) }

func main() {
	seed = loadSeed()
	if seed == 0 {
		seed = 1
	}

	rSum := cfAdd(0.1, 0.2)
	rZero := cfCancelRT(0.1, 0.2, 0.3)
	rThird := cfDiv(1, 3)

	ck(math.Float64bits(float64(cfSum)) == 0x3FD3333333333333, "constant 0.1+0.2 exact decimal rounding")
	ck(math.Float64bits(rSum) == 0x3FD3333333333334, "runtime 0.1+0.2 rounds operands first")
	ck(float64(cfSum) != rSum, "constant and runtime sums differ")
	ck(float64(cfCancelConst) == 0, "constant decimal cancellation is zero")
	ck(rZero != 0, "runtime decimal cancellation is non-zero")
	ck(math.Float64bits(float64(cfThird)) == 0x3FD5555555555555, "constant one third rounding")
	ck(float64(cfThird) == rThird, "one third constant equals runtime division")

	var oneThird float32 = float32(cfThird)
	ck(math.Float32bits(oneThird) == 0x3EAAAAAB, "float32 constant rounding")

	ck(math.Float32bits(cf32) == 0x3DCCCCCD, "float32 of 0.1")
	ck(math.Float32bits(cf32) == math.Float32bits(cfToFloat32(0.1)), "constant float32 conversion")

	var bigInt int64 = 1<<53 + 1
	rfBig := float64(bigInt)
	ck(math.Float64bits(cfBig) == math.Float64bits(rfBig), "float64 of 2^53+1")
	ck(cfBig == float64(1<<53), "2^53+1 rounds to even")
	ck(float64(cfBig) == 9007199254740992.0, "2^53+1 numeric value")

	fl := 3.9
	ck(int(fl) == 3 && int(-fl) == -3, "runtime float to int truncates toward zero")
	var arr [3]int64
	arr[0] = 1
	arr[2] = 3
	ck(len(arr) == 3, "constant-sized array")

	rc := complex(1, 2) * complex(3, -4)
	ck(real(cfC) == 11 && imag(cfC) == 2, "complex constant multiplication")
	ck(complex128(cfC) == rc, "complex constant equals runtime")

	ck(math.Float64bits(math.MaxFloat64) == 0x7FEFFFFFFFFFFFFF, "MaxFloat64 constant")
	ck(math.Float32bits(math.MaxFloat32) == 0x7F7FFFFF, "MaxFloat32 constant")
	ck(float64(math.MaxFloat32) == 340282346638528859811704183484516925440.0, "MaxFloat32 exact value")
	half := math.MaxFloat64 / 2
	ck(math.Float64bits(half) == 0x7FDFFFFFFFFFFFFF, "MaxFloat64 half constant")

	const cfNegZero = -0.0
	negz := math.Copysign(0, -1)
	ck(cfNegZero == 0, "constant negative zero is zero")
	ck(!math.Signbit(float64(cfNegZero)), "constant -0.0 converts to +0")
	ck(math.Signbit(negz), "runtime negative zero keeps sign")

	m := map[float64]string{float64(cfSum): "const"}
	ck(m[float64(cfSum)] == "const", "constant float map key")
	ck(m[rSum] == "", "runtime sum is a different float key")

	vals := []float64{float64(cfSum), rSum, float64(cfThird), rThird, cfBig, float64(cf32), math.Copysign(0, -1)}
	gcStress()
	bitsOK := math.Float64bits(vals[0]) == 0x3FD3333333333333 &&
		math.Float64bits(vals[1]) == 0x3FD3333333333334 &&
		math.Float64bits(vals[2]) == 0x3FD5555555555555 &&
		vals[3] == rThird &&
		math.Float64bits(vals[4]) == 0x4340000000000000 &&
		math.Float64bits(vals[5]) == 0x3FB99999A0000000 &&
		math.Signbit(vals[6])
	ck(bitsOK, "retained float bits after GC")
	ck(arr[0]+arr[2] == 4 && len(arr) == 3, "constant-sized array after GC")

	finish("l11_const_float")
}
