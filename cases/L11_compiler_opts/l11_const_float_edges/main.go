// L11: 浮点常量边界——2^53/2^24 附近的 ties-to-even、高精度十进制常量
// 单次舍入、float32 双重舍入边界、次正规数的运行期算术、±Inf/NaN 比较
// 与常量/运行期类型差异；保留的位模式跨显式 GC 不变。
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
	cfTie1    = float64(1<<53 + 1)
	cfTie2    = float64(1<<53 + 2)
	cfTie3    = float64(1<<53 + 3)
	cfExact01 = 0.1000000000000000055511151231257827021181583404541015625
	cfHalf32A = float32(1 + 1.0/16777216)
	cfHalf32B = float32(1 + 3.0/16777216)
	cf24Tie1  = float32(1<<24 + 1)
	cf24Tie3  = float32(1<<24 + 3)
	cfMinSub  = math.SmallestNonzeroFloat64
	cfNegMin  = -math.SmallestNonzeroFloat64
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

func cfHalf(x float64) float64 { return x / 2 }

//go:noinline
func cfHalfNI(x float64) float64 { return x / 2 }

func main() {
	seed = loadSeed()
	if seed == 0 {
		seed = 1
	}

	ck(math.Float64bits(cfTie1) == 0x4340000000000000, "2^53+1 ties to even")
	ck(math.Float64bits(cfTie2) == 0x4340000000000001, "2^53+2 exact")
	ck(math.Float64bits(cfTie3) == 0x4340000000000002, "2^53+3 ties to even")
	ck(cfTie1 == cfTie2-2, "2^53+1 equals 2^53")
	ck(cfTie3 == cfTie2+2, "2^53+3 rounds to 2^53+4")

	ck(math.Float64bits(float64(cfExact01)) == 0x3fb999999999999a, "exact 0.1 decimal constant")
	ck(math.Float64bits(0.1) == 0x3fb999999999999a, "literal 0.1 rounding")

	ck(math.Float32bits(cfHalf32A) == 0x3f800000, "float32 halfway ties to even down")
	ck(math.Float32bits(cfHalf32B) == 0x3f800002, "float32 halfway ties to even up")
	ck(math.Float32bits(cf24Tie1) == 0x4b800000, "float32 2^24+1 ties to even")
	ck(math.Float32bits(cf24Tie3) == 0x4b800002, "float32 2^24+3 ties to even")

	var f32 float32 = cfHalf32A
	ck(math.Float32bits(f32) == 0x3f800000, "float32 constant assignment")
	var f32b float64 = float64(cfHalf32B)
	ck(math.Float64bits(f32b) == 0x3ff0000040000000, "float32 to float64 widening")

	ck(math.Float64bits(float64(cfMinSub)) == 0x1, "constant smallest subnormal")
	ck(math.Signbit(float64(cfNegMin)), "constant negative smallest subnormal sign")
	tiny := math.SmallestNonzeroFloat64
	half := tiny / 2
	halfNI := cfHalfNI(tiny)
	ck(half == 0 && halfNI == 0, "runtime smallest subnormal half underflows")
	ck(cfHalf(tiny) == 0, "helper subnormal half underflows")
	ck(tiny*2 == math.Float64frombits(2), "runtime smallest subnormal double")
	ck(tiny > 0 && -tiny < 0, "subnormal signs")
	f32tiny := float32(math.SmallestNonzeroFloat32)
	ck(f32tiny/2 == 0, "float32 smallest subnormal half underflows")
	ck(f32tiny*2 == math.Float32frombits(2), "float32 smallest subnormal double")

	inf := math.Inf(1)
	negInf := math.Inf(-1)
	ck(inf > math.MaxFloat64, "positive infinity compare")
	ck(negInf < -math.MaxFloat64, "negative infinity compare")
	ck(inf == inf && negInf == negInf, "infinity equality")
	ck(inf > negInf, "infinity ordering")
	ck(math.IsInf(inf, 1) && math.IsInf(negInf, -1), "IsInf checks")

	nan := math.NaN()
	ck(nan != nan, "NaN not equal to itself")
	ck(math.IsNaN(nan), "IsNaN")
	ck(!(nan > 0) && !(nan < 0), "NaN unordered")
	ck(math.Float64bits(nan) != 0, "NaN bits nonzero")

	small32 := float32(1.5)
	ck(math.Float32bits(small32) == 0x3fc00000, "float32 wide value")
	ck(math.Float64bits(float64(small32)) == 0x3ff8000000000000, "runtime float32 widening")

	ck(math.Float64bits(1e308) == math.Float64bits(float64(1e308)), "large literal constants")
	ck(1e308 < inf && -1e308 > negInf, "large finite constant bounded by infinities")

	var iv any = 1 / 2
	var fv any = 1 / 2.0
	_, iok := iv.(int)
	_, fok := fv.(float64)
	ck(iok && fok, "untyped constant result types")
	ck(iv.(int) == 0, "integer constant division")
	ck(fv.(float64) == 0.5, "floating constant division")

	runtimeInt := 2.9
	ck(int64(runtimeInt) == 2 && int64(-runtimeInt) == -2, "runtime float to int truncation")

	m := map[float64]string{
		float64(cfTie1): "tie1",
		math.Inf(1):     "inf",
	}
	ck(m[float64(cfTie1)] == "tie1", "float constant map key")
	ck(m[inf] == "inf", "infinity map key")
	ck(m[nan] == "", "NaN map key never matches")

	keep := []float64{float64(cfTie1), float64(cfTie3), float64(cfExact01),
		float64(cfHalf32B), tiny, math.Copysign(0, -1), inf, negInf}
	bits := make([]uint64, len(keep))
	for i, v := range keep {
		bits[i] = math.Float64bits(v)
	}
	gcStress()
	for i, v := range keep {
		ck(math.Float64bits(v) == bits[i], "retained float bits after GC")
	}
	ck(math.Float32bits(float32(keep[3])) == 0x3f800002, "float32 value after GC")
	ck(keep[4] == math.SmallestNonzeroFloat64 && keep[4] > 0, "subnormal after GC")

	nanKeep := math.NaN()
	gcStress()
	ck(math.IsNaN(nanKeep) && nanKeep != nanKeep, "NaN value after GC")
	ck(math.Signbit(math.Copysign(0, -1)), "negative zero after GC")

	seedF := float64(seed%97+1) / 7
	var acc float64
	for i := 0; i < 100; i++ {
		acc += seedF / float64(i+3)
	}
	acc2 := seedF / 3
	ck(acc > acc2, "accumulated float division")
	ck(!math.IsNaN(acc) && !math.IsInf(acc, 0), "accumulated float finite")

	finish("l11_const_float_edges")
}
