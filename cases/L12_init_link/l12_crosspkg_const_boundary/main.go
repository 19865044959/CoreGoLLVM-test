// L12: cross-package constant-chain boundaries (module). limits exports typed
// and untyped constants at the edges of uint8/int8/uint16/int64, string and
// complex constant expressions; derive uses them as array lengths and in
// further constant expressions; main builds a two-package const-length table.
// The case checks constant folding, typed-constant preservation, zero-length
// arrays, %T defaults and runtime truncation.
package main

import (
	"fmt"
	"os"
	"runtime"
	"strconv"
	"sync"
	"sync/atomic"

	"l12_crosspkg_const_boundary/derive"
	"l12_crosspkg_const_boundary/limits"
)

const (
	TableLen = limits.Total + derive.Slots // 1088 + 34
	SeedBase = int(limits.HighBit >> 10)   // 32, constant-derived
)

var table [TableLen]int64

func init() {
	for i := range table {
		table[i] = int64(i%int(limits.Slots)) + int64(SeedBase)
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

func gcStress(rounds int) {
	for r := 0; r < rounds; r++ {
		for i := 0; i < 16; i++ {
			b := make([]byte, 4096)
			b[0] = byte(i + r)
			gcSink = append(gcSink[:0], b)
		}
		runtime.GC()
	}
	gcSink = gcSink[:0]
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

func main() {
	seedVal = readSeed()
	seedStr = strconv.FormatInt(seedVal, 10)

	// Basic untyped/typed constant values and their const expressions.
	ck(limits.ZeroLen == 0 && limits.Slots == 17 && limits.Blocks == 64 && limits.Total == 1088, "limits arithmetic constants")
	ck(limits.MaxWidth == 255, "typed uint8 max")
	ck(limits.MinLevel == -128, "typed int8 min")
	ck(limits.HighBit == 32768, "untyped high bit")
	ck(limits.FlagsAll == 33023, "typed flags combination")
	ck(limits.Word == "go-l12" && limits.WordLen == 6, "const string concat/len")
	ck(limits.Name == "go-l12/derived" && limits.NameLen == 14, "const name concat/len")
	ck(limits.Pair == "abc", "const rune string")
	ck(limits.Unicode == "héllo" && limits.UnicodeLen == 6, "const string byte length")
	ck(limits.CReal == 3 && limits.CImag == 4 && limits.CAbs2 == 25, "const complex parts")
	ck(limits.Shifted == 9223372036854775808, "typed uint64 shift")
	ck(limits.Huge == 1099511627776, "untyped huge int")
	ck(limits.MinI64 == -9223372036854775808 && limits.MaxI64 == 9223372036854775807, "int64 boundaries")
	ck(limits.ExactFloat == 0.375, "exact float constant")

	// Typed constants keep their defined type; untyped constants take their
	// default type when boxed.
	ck(fmt.Sprintf("%T", limits.MaxWidth) == "limits.Width", "typed Width %T")
	ck(fmt.Sprintf("%T", limits.MinLevel) == "limits.Level", "typed Level %T")
	ck(fmt.Sprintf("%T", limits.FlagsAll) == "limits.Flags", "typed Flags %T")
	ck(fmt.Sprintf("%T", limits.HighBit) == "int", "untyped HighBit %T")
	ck(fmt.Sprintf("%T", limits.CVal) == "complex128", "untyped complex default %T")
	ck(fmt.Sprintf("%T", limits.Pair) == "string", "const string %T")
	ck(fmt.Sprintf("%T", limits.ExactFloat) == "float64", "untyped float default %T")
	ck(fmt.Sprintf("%T", limits.Shifted) == "uint64", "typed Shifted %T")

	// Cross-package constant propagation.
	ck(derive.Slots == 34 && derive.Span == 1071, "derive arithmetic chain")
	ck(derive.DerivedName == "go-l12/derived/d" && derive.DerivedNameLen == 16, "derive string chain")
	ck(derive.LowMask == 255, "typed mask via cross-package const")
	ck(fmt.Sprintf("%T", derive.LowMask) == "limits.Flags", "derive mask type preserved")
	ck(derive.LevelSpan == 383, "const conversions of typed boundaries")
	ck(derive.ShiftCheck == 128, "const shift chain")
	ck(TableLen == 1122, "two-package const array length")

	ck(len(derive.Zero) == 0, "zero-length array from imported const")
	ck(len(derive.Grid) == 64, "grid length from imported const")
	ck(len(derive.Rows) == 34 && len(derive.Labels) == 34, "derive lengths")
	ck(len(table) == 1122, "main table length")
	ck(derive.InitOK, "derive init saw wrong constants")

	for i := 0; i < 64; i++ {
		if derive.Grid[i] != uint16(i*3+32) {
			ck(false, fmt.Sprintf("grid[%d]=%d", i, derive.Grid[i]))
			break
		}
	}
	for i := 0; i < 34; i++ {
		if derive.Labels[i] != fmt.Sprintf("r%02d", i) ||
			derive.Rows[i][0] != uint8(i) || derive.Rows[i][1] != uint8(i*3+255) {
			ck(false, fmt.Sprintf("row/label %d mismatch", i))
			break
		}
	}
	for i := 0; i < len(table); i++ {
		if table[i] != int64(i%17+32) {
			ck(false, fmt.Sprintf("table[%d]=%d", i, table[i]))
			break
		}
	}

	// Typed constant keys and switches.
	names := map[limits.Width]string{0: "zero", limits.MaxWidth: "max"}
	ck(names[limits.MaxWidth] == "max" && names[limits.Width(0)] == "zero", "typed const map keys")
	flagName := "other"
	switch limits.FlagsAll & 0x00ff {
	case limits.Flags(255):
		flagName = "low"
	}
	ck(flagName == "low", "switch on typed const expression")

	// Constant conversions are compile-time; runtime conversions wrap.
	wv := 300
	lv := -300
	fv := 0x1ffff
	w := limits.Width(wv)
	l := limits.Level(lv)
	f := limits.Flags(fv)
	ck(w == 44, fmt.Sprintf("uint8 wrap %d", w))
	ck(l == -44, fmt.Sprintf("int8 wrap %d", l))
	ck(f == 65535, fmt.Sprintf("uint16 wrap %d", f))
	ck(uint8(limits.HighBit>>8) == 128, "constant shift conversion")

	// Read-only concurrent view of const-derived tables with explicit GC.
	var wg sync.WaitGroup
	var bad int32
	start := int(seedVal % 1122)
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < 400; i++ {
				idx := (start + g*13 + i) % 1122
				if table[idx] != int64(idx%17+32) {
					atomic.AddInt32(&bad, 1)
					return
				}
				j := idx % 34
				if derive.Labels[j] != fmt.Sprintf("r%02d", j) ||
					derive.Grid[idx%64] != uint16((idx%64)*3+32) {
					atomic.AddInt32(&bad, 1)
					return
				}
			}
		}(g)
	}
	gcStress(3)
	wg.Wait()
	ck(atomic.LoadInt32(&bad) == 0, "concurrent const-table read failed under GC")

	gcStress(2)
	ck(len(table) == 1122 && table[1121] == int64(1121%17+32), "table changed after GC")
	ck(derive.Labels[33] == "r33" && derive.Grid[63] == uint16(63*3+32), "derive tables changed after GC")
	ck(limits.FlagsAll == 33023 && limits.MaxWidth == 255, "constants changed after GC")
	ck(len(derive.Zero) == 0, "zero-length array changed after GC")

	finish("l12_crosspkg_const_boundary")
}
