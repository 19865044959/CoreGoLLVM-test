// L10: 生成器产物中的大 const 表：1024 个类型常量 + 名称常量、编译期折叠数组、
// 反查/二分查找、GC 后全表校验，UAF_SEED 随机探测。
package main

import (
	"fmt"
	"math/rand"
	"os"
	"runtime"
	"sort"
	"strconv"
	"sync/atomic"
)

const codeCount = 1024
const codeBase int32 = 1000000

const (
	MaxI32 = int32(1<<31 - 1)
	MinI32 = int32(-1 << 31)
	MaxI64 = int64(1<<63 - 1)
	MinI64 = int64(-1 << 63)
	MaxU64 = uint64(1<<64 - 1)
)

// 编译期折叠：表跨度与名称总长在编译期即可确定。
const foldedSpan = int64(Code1023) - int64(Code0000) + 1
const foldedNameLen = len(StrCode0000) + len(StrCode1023)
const expectedSum = int64(codeBase)*codeCount + int64(codeCount)*(codeCount-1)/2 + int64(len("CODE_0000"))*codeCount

// 数组长度由常量表达式给出，编译不过即说明常量折叠/类型推导有问题。
var tableShape [foldedSpan]byte

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

func envRounds() int {
	n := 1
	if s := os.Getenv("TORTURE_ROUNDS"); s != "" {
		if v, err := strconv.Atoi(s); err == nil && v > 0 {
			n = v
		}
	}
	if n > 50 {
		n = 50
	}
	return n
}

func byArith(v int32) (int, bool) {
	if v < codeBase || int64(v) > int64(codeBase)+int64(codeCount)-1 {
		return 0, false
	}
	return int(v - codeBase), true
}

func bySearch(v int32) (int, bool) {
	i := sort.Search(len(codeValues), func(i int) bool { return codeValues[i] >= v })
	if i < len(codeValues) && codeValues[i] == v {
		return i, true
	}
	return 0, false
}

func byName(name string) (int, bool) {
	if len(name) != len("CODE_0000") || name[:5] != "CODE_" {
		return 0, false
	}
	n, err := strconv.Atoi(name[5:])
	if err != nil || n < 0 || n >= codeCount {
		return 0, false
	}
	if codeNames[n] != name {
		return 0, false
	}
	return n, true
}

func nameOf(i int) string {
	var buf [9]byte
	copy(buf[:], "CODE_")
	v := i
	for p := 8; p >= 5; p-- {
		buf[p] = byte('0' + v%10)
		v /= 10
	}
	return string(buf[:])
}

func sumTable() int64 {
	var sum int64
	for i := 0; i < codeCount; i++ {
		sum += int64(codeValues[i]) + int64(len(codeNames[i]))
	}
	return sum
}

func hashTable() uint64 {
	h := uint64(1469598103934665603)
	for i := 0; i < codeCount; i++ {
		h ^= uint64(uint32(codeValues[i]))
		h *= 1099511628211
		for j := 0; j < len(codeNames[i]); j++ {
			h ^= uint64(codeNames[i][j])
			h *= 1099511628211
		}
	}
	return h
}

func main() {
	curSeed = 1
	if s := os.Getenv("UAF_SEED"); s != "" {
		if v, err := strconv.ParseInt(s, 10, 64); err == nil {
			curSeed = v
		}
	}
	tRounds := envRounds()

	// 1. 表规模与编译期常量。
	ck(len(tableShape) == codeCount, "compile-time folded array size")
	ck(foldedSpan == codeCount, "folded span")
	ck(foldedNameLen == 18, "folded name length")
	ck(expectedSum == 1024532992, "folded sum literal")
	ck(len(codeValues) == codeCount, "value table size")
	ck(len(codeNames) == codeCount, "name table size")
	ck(MaxI32 == 2147483647 && MinI32 == -2147483648, "int32 constant bounds")
	ck(MaxI64 == 9223372036854775807 && MinI64 == -9223372036854775808, "int64 constant bounds")
	ck(MaxU64 == ^uint64(0), "uint64 constant bounds")

	// 2. 全表值/名称一致，且与运行期生成公式一致。
	badVal := 0
	badName := 0
	for i := 0; i < codeCount; i++ {
		if codeValues[i] != codeBase+int32(i) {
			badVal++
		}
		if codeNames[i] != nameOf(i) {
			badName++
		}
	}
	ck(badVal == 0, "const values follow base+index")
	ck(badName == 0, "const names follow CODE_%04d")
	ck(sumTable() == expectedSum, "table sum equals folded constant")
	h0 := hashTable()

	// 3. 三种查找路径一致；重复值/名称检测。
	byVal := make(map[int32]int, codeCount)
	byStr := make(map[string]int, codeCount)
	for i := 0; i < codeCount; i++ {
		if _, dup := byVal[codeValues[i]]; dup {
			ck(false, "duplicate const value")
		}
		if _, dup := byStr[codeNames[i]]; dup {
			ck(false, "duplicate const name")
		}
		byVal[codeValues[i]] = i
		byStr[codeNames[i]] = i
	}
	for i := 0; i < codeCount; i++ {
		v := codeValues[i]
		a, ok1 := byArith(v)
		b, ok2 := bySearch(v)
		n, ok3 := byName(codeNames[i])
		if !ok1 || !ok2 || !ok3 || a != i || b != i || n != i || byVal[v] != i || byStr[codeNames[i]] != i {
			ck(false, "lookup paths disagree")
			break
		}
	}

	// 4. GC 后全表、映射与哈希不变。
	retainedNames := make([]string, codeCount)
	copy(retainedNames, codeNames[:])
	gcSink = append(gcSink, []byte(codeNames[0]), []byte(codeNames[codeCount-1]))
	gcStress()
	ck(hashTable() == h0, "table hash stable across GC")
	ck(sumTable() == expectedSum, "table sum stable across GC")
	for i := 0; i < codeCount; i++ {
		if byStr[retainedNames[i]] != i || byVal[codeValues[i]] != i {
			ck(false, "lookups stable across GC")
			break
		}
		if retainedNames[i] != nameOf(i) {
			ck(false, "retained name stable across GC")
			break
		}
	}

	// 5. 越界输入全部拒绝。
	invalid := []int32{MinI32, MinI32 + 1, -1, 0, 1, codeBase - 1, codeBase + codeCount, MaxI32, MaxI32 - 1}
	for _, v := range invalid {
		if _, ok := byArith(v); ok {
			ck(false, "byArith accepted out-of-range value")
		}
		if _, ok := bySearch(v); ok {
			ck(false, "bySearch accepted out-of-range value")
		}
		if _, ok := byVal[v]; ok {
			ck(false, "map accepted out-of-range value")
		}
	}
	for _, s := range []string{"", "CODE_", "CODE__000", "CODE_00000", "code_0000", "CODE_1024", "CODE_-001", "CODE_00x0", "CODE_0000 "} {
		if _, ok := byName(s); ok {
			ck(false, "byName accepted malformed name")
		}
	}

	// 6. UAF_SEED 随机探测（有效/无效混合），GC 周期性插入。
	rng := rand.New(rand.NewSource(curSeed))
	probes := 150000 * tRounds
	for p := 0; p < probes; p++ {
		if p%3 == 0 {
			idx := rng.Intn(codeCount)
			v := codeValues[idx]
			a, ok1 := byArith(v)
			b, ok2 := bySearch(v)
			n, ok3 := byName(codeNames[idx])
			ckSeed(ok1 && ok2 && ok3 && a == idx && b == idx && n == idx, "random valid probe")
			ckSeed(byVal[v] == idx, "random valid map probe")
			ckSeed(codeNames[idx] == nameOf(idx), "random valid name probe")
		} else {
			v := int32(rng.Uint32())
			if v >= codeBase && int64(v) <= int64(codeBase)+int64(codeCount)-1 {
				continue
			}
			if _, ok := byArith(v); ok {
				ckSeed(false, "random invalid accepted by arith")
			}
			if _, ok := bySearch(v); ok {
				ckSeed(false, "random invalid accepted by search")
			}
		}
		if p%50000 == 0 {
			gcStress()
			ckSeed(hashTable() == h0, "table hash stable in random loop")
		}
	}
	gcSink = append(gcSink, []byte(retainedNames[codeCount-1]))

	finish("l10_large_const_table")
}
