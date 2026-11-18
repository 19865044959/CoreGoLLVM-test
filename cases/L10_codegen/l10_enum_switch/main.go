// L10: 500 个生成风格枚举值：String/合法值/整数映射三张 switch 表 + 名称反查 map。
// 验证常量表、switch 分派、越界值与 GC 后的确定性。
package main

import (
	"fmt"
	"math"
	"math/rand"
	"os"
	"runtime"
	"strconv"
	"sync/atomic"
)

var (
	failed  int32
	gcSink  [][]byte
	curSeed int64
)

func ck(ok bool, msg string) {
	if !ok {
		atomic.AddInt32(&failed, 1)
		fmt.Println("FAIL:", msg)
	}
}

func ckSeed(ok bool, msg string) {
	if !ok {
		atomic.AddInt32(&failed, 1)
		fmt.Printf("FAIL: %s (seed=%d)\n", msg, curSeed)
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

func expectedName(i int) string { return fmt.Sprintf("COLOR_%04d", i) }

func main() {
	curSeed = 1
	if s := os.Getenv("UAF_SEED"); s != "" {
		if v, err := strconv.ParseInt(s, 10, 64); err == nil {
			curSeed = v
		}
	}

	ck(colorCount == 500, "enum declares 500 values")
	ck(len(colorNames) == colorCount, "name table length")
	ck(len(colorValues) == colorCount, "reverse map length")

	// 1. 全量双向映射：值 → String → 名称反查 → 整数映射回值。
	for i := 0; i < colorCount; i++ {
		c := Color(i)
		want := expectedName(i)
		if c.String() != want {
			ckSeed(false, fmt.Sprintf("String(%d)=%q want %q", i, c.String(), want))
			break
		}
		got, ok := colorFromName(want)
		if !ok || got != c {
			ckSeed(false, fmt.Sprintf("FromName(%q) mismatch", want))
			break
		}
		vc, ok := colorFromCode(int32(i))
		if !ok || vc != c {
			ckSeed(false, fmt.Sprintf("FromCode(%d) mismatch", i))
			break
		}
		if !colorValid(c) {
			ckSeed(false, fmt.Sprintf("Valid(%d)=false", i))
			break
		}
		if colorGroup(c) != i/50 {
			ckSeed(false, fmt.Sprintf("Group(%d)=%d", i, colorGroup(c)))
			break
		}
	}

	// 2. 越界值：String 走 default，Valid=false，映射失败。
	for _, bad := range []int32{-1, -7, colorCount, colorCount + 1, 1000, math.MinInt32, math.MaxInt32} {
		c := Color(bad)
		want := fmt.Sprintf("Color(%d)", bad)
		ckSeed(c.String() == want, fmt.Sprintf("invalid String(%d)", bad))
		ckSeed(!colorValid(c), fmt.Sprintf("invalid Valid(%d)", bad))
		_, ok := colorFromCode(bad)
		ckSeed(!ok, fmt.Sprintf("invalid FromCode(%d)", bad))
		_, ok = colorFromName(want)
		ckSeed(!ok, fmt.Sprintf("invalid FromName(%q)", want))
		ckSeed(colorGroup(c) == -1, fmt.Sprintf("invalid Group(%d)", bad))
	}

	// 3. 名称唯一且无重复注册。
	uniq := make(map[string]bool, colorCount)
	dup := false
	for _, n := range colorNames {
		if uniq[n] {
			dup = true
		}
		uniq[n] = true
	}
	ck(!dup && len(uniq) == colorCount, "enum names unique")

	// 4. 大小写/畸形名称反查必须失败。
	for _, s := range []string{"", "COLOR_", "COLOR_0500", "color_0001", "COLOR_00001", "COLOR_abcd"} {
		if _, ok := colorFromName(s); ok {
			ckSeed(false, fmt.Sprintf("FromName(%q) unexpectedly ok", s))
		}
	}

	// 5. GC 后重新全量查询，结果确定。
	gcStress()
	var checksum int64
	for i := 0; i < colorCount; i++ {
		c := Color(i)
		checksum += int64(len(c.String()))*int64(i+1) + int64(colorGroup(c)) + int64(c)
	}
	wantSum := int64(0)
	for i := 0; i < colorCount; i++ {
		wantSum += int64(len(expectedName(i)))*int64(i+1) + int64(i/50) + int64(i)
	}
	ck(checksum == wantSum, "post-GC checksum deterministic")
	ck(Color(123).String() == "COLOR_0123" && colorGroup(Color(123)) == 2, "post-GC spot check")

	// 6. UAF_SEED 随机访问（含越界），GC 周期性穿插。
	r := rand.New(rand.NewSource(curSeed))
	var kept []string
	for iter := 0; iter < 2000; iter++ {
		if r.Intn(5) == 0 {
			var bad int32
			if r.Intn(2) == 0 {
				bad = int32(-1 - r.Intn(2000))
			} else {
				bad = int32(colorCount + r.Intn(2000))
			}
			c := Color(bad)
			ckSeed(c.String() == fmt.Sprintf("Color(%d)", bad), "random invalid string")
			ckSeed(!colorValid(c), "random invalid valid")
		} else {
			i := r.Intn(colorCount)
			c := Color(i)
			ckSeed(c.String() == expectedName(i), "random valid string")
			ckSeed(colorValid(c), "random valid flag")
			ckSeed(colorGroup(c) == i/50, "random group")
			vc, ok := colorFromCode(int32(i))
			ckSeed(ok && vc == c, "random from code")
			if iter%97 == 0 {
				kept = append(kept, c.String())
			}
		}
		if iter%113 == 0 {
			gcStress()
		}
	}
	gcStress()
	for i, s := range kept {
		c, ok := colorFromName(s)
		if !ok || c.String() != s {
			ckSeed(false, fmt.Sprintf("kept enum %d", i))
		}
	}

	finish("l10_enum_switch")
}
