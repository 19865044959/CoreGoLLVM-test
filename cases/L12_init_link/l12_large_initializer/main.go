// L12: 包级大数组/指针矩阵/大 map 初始化 + 反复 GC（P2，TORTURE_ROUNDS 放大）。
package main

import (
	"fmt"
	"os"
	"runtime"
	"strconv"
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

func staticVal(i int) uint64 { return uint64(i*i*3 + i + 1) }

func checkStatic() {
	ck(len(staticTable) == 128, "static table length")
	for i := 0; i < len(staticTable); i++ {
		if staticTable[i] != staticVal(i) {
			ck(false, "static table value at index "+strconv.Itoa(i))
			return
		}
	}
	ck(lv0 == 100 && lv7 == 107, "package-level scalar vars")
	for i := 0; i < len(ptrMatrix); i++ {
		for j, p := range ptrMatrix[i] {
			if p == nil {
				ck(false, "nil pointer matrix cell")
				return
			}
			if *p != int64(100+j) {
				ck(false, "pointer matrix value")
				return
			}
			if p != ptrMatrix[0][j] {
				ck(false, "pointer matrix identity across rows")
				return
			}
		}
	}
	ck(len(bigBytes) == 16384, "bigBytes length")
	if len(bigBytes) == 16384 {
		ck(bigBytes[0] == 3 && bigBytes[16383] == byte((16383*7+3)%256), "bigBytes content edges")
		var sum uint64
		for _, b := range bigBytes {
			sum += uint64(b)
		}
		var wantSum uint64
		for i := 0; i < 16384; i++ {
			wantSum += uint64(byte(i*7 + 3))
		}
		ck(sum == wantSum, "bigBytes checksum")
	}
}

func checkBigMap(rounds int) {
	n := bigMapCount
	ck(len(bigMap) == n, "big map length")
	wantSum := int64(n) * int64(n-1) / 2
	ck(bigMapIDSum == wantSum, "big map id sum at init")
	ck(!bigInitBad, "big map validated during init")
	ck(bigMapHead != nil && bigMapHead.id == n-1, "big map head")

	seen := 0
	wantID := n - 1
	for it := bigMapHead; it != nil; it = it.next {
		if it.id != wantID || it.label != "item" {
			ck(false, "big map chain node")
			return
		}
		seen++
		wantID--
		if seen > n {
			ck(false, "big map chain cycle")
			return
		}
	}
	ck(seen == n, "big map chain length")

	step := n / 64
	if step < 1 {
		step = 1
	}
	start := int((seedVal + int64(rounds)) % int64(n))
	for k := 0; k < 64; k++ {
		i := (start + k*step) % n
		it := bigMap[i]
		if it == nil {
			ck(false, "missing big map entry")
			return
		}
		if it.id != i || it.label != "item" || len(it.payload) != 24 {
			ck(false, "big map entry metadata")
			return
		}
		if it.payload[0] != byte(i*7) || it.payload[23] != byte(i*7+23) {
			ck(false, "big map payload")
			return
		}
	}
}

func main() {
	seedVal = readSeed()
	seedStr = strconv.FormatInt(seedVal, 10)
	rounds := envRounds()

	checkStatic()
	ck(bigMapCount >= 12000, "default big map size")
	checkBigMap(rounds)

	// 大对象初始化后先做一轮 GC 垃圾压力。
	for pass := 0; pass < 2+rounds; pass++ {
		chunks := make([][]byte, 8)
		for i := range chunks {
			chunks[i] = make([]byte, 262144)
			chunks[i][0] = byte(pass + i)
		}
		gcSink = append(gcSink[:0], chunks...)
		runtime.GC()
	}
	gcSink = gcSink[:0]
	checkStatic()
	checkBigMap(rounds)

	gcStress()
	checkStatic()
	checkBigMap(rounds)

	// 指针矩阵与包级标量变量的别名关系跨 GC 保持。
	lv0 = 777
	ck(*ptrMatrix[5][0] == 777 && *ptrMatrix[15][0] == 777, "pointer matrix aliases package scalar")
	gcStress()
	ck(*ptrMatrix[0][0] == 777 && *ptrMatrix[15][0] == 777, "alias after GC")
	lv0 = 100
	ck(*ptrMatrix[0][0] == 100, "alias restored")

	ck(bigBytes[1234] == byte((1234*7+3)%256), "bigBytes survives repeated GC")
	finish("l12_large_initializer")
}
