// L11: 多维边界检查消除——二维/三维/锯齿切片的行优先、列优先、
// 扁平视图与子切片求和必须一致；越界索引/切片仍按规范 panic。
package main

import (
	"fmt"
	"os"
	"runtime"
	"strconv"
	"sync/atomic"
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

func tortureMultiplier() int {
	s := os.Getenv("TORTURE_ROUNDS")
	if s == "" {
		return 1
	}
	n, err := strconv.Atoi(s)
	if err != nil || n < 1 {
		return 1
	}
	if n > 50 {
		n = 50
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

func buildMatrix(rows, cols int, salt int64) [][]int64 {
	m := make([][]int64, rows)
	for i := 0; i < rows; i++ {
		m[i] = make([]int64, cols)
		for j := 0; j < cols; j++ {
			m[i][j] = int64((i*cols+j)%97) + salt
		}
	}
	return m
}

func sumNested(m [][]int64) int64 {
	var t int64
	for i := 0; i < len(m); i++ {
		row := m[i]
		for j := 0; j < len(row); j++ {
			t += row[j]
		}
	}
	return t
}

func sumRange(m [][]int64) int64 {
	var t int64
	for i := range m {
		for _, v := range m[i] {
			t += v
		}
	}
	return t
}

func sumColumns(m [][]int64) int64 {
	var t int64
	cols := len(m[0])
	for j := 0; j < cols; j++ {
		for i := 0; i < len(m); i++ {
			t += m[i][j]
		}
	}
	return t
}

func sumInteriorSubslice(m [][]int64) int64 {
	var t int64
	for i := 1; i < len(m)-1; i++ {
		row := m[i][1 : len(m[i])-1]
		for _, v := range row {
			t += v
		}
	}
	return t
}

func sumInteriorRef(m [][]int64) int64 {
	var t int64
	for i, row := range m {
		for j, v := range row {
			if i > 0 && i < len(m)-1 && j > 0 && j < len(row)-1 {
				t += v
			}
		}
	}
	return t
}

func buildJagged(rows int, salt int64) [][]int64 {
	m := make([][]int64, rows)
	for i := 0; i < rows; i++ {
		m[i] = make([]int64, i+4)
		for j := range m[i] {
			m[i][j] = int64(i*10+j) + salt
		}
	}
	return m
}

func sumJagged(m [][]int64) int64 {
	var t int64
	for i := 0; i < len(m); i++ {
		for j := 0; j < len(m[i]); j++ {
			t += m[i][j]
		}
	}
	return t
}

func jaggedFormula(rows int, salt int64) int64 {
	var t int64
	for i := 0; i < rows; i++ {
		l := int64(i + 4)
		t += l*salt + l*int64(i*10) + l*(l-1)/2
	}
	return t
}

func buildTensor(d, r, c int, salt int64) [][][]int64 {
	t := make([][][]int64, d)
	for i := 0; i < d; i++ {
		t[i] = make([][]int64, r)
		for j := 0; j < r; j++ {
			t[i][j] = make([]int64, c)
			for k := 0; k < c; k++ {
				t[i][j][k] = int64(i*100+j*10+k) + salt
			}
		}
	}
	return t
}

func sumTensorNested(t [][][]int64) int64 {
	var s int64
	for i := 0; i < len(t); i++ {
		for j := 0; j < len(t[i]); j++ {
			row := t[i][j]
			for k := 0; k < len(row); k++ {
				s += row[k]
			}
		}
	}
	return s
}

func sumTensorFlat(t [][][]int64) int64 {
	var s int64
	for _, plane := range t {
		for _, row := range plane {
			for _, v := range row {
				s += v
			}
		}
	}
	return s
}

func expectPanic(name string, fn func()) {
	defer func() {
		r := recover()
		if r == nil {
			ck(false, name+": expected panic")
			return
		}
		if !contains(fmt.Sprint(r), "out of range") {
			ck(false, name+": unexpected panic text: "+fmt.Sprint(r))
		}
	}()
	fn()
}

func contains(s, sub string) bool {
	if len(sub) == 0 {
		return true
	}
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

func main() {
	seed = loadSeed()
	if seed == 0 {
		seed = 1
	}
	rounds := 8 * tortureMultiplier()

	var kept [][]int64
	var keptSum int64
	for r := 0; r < rounds; r++ {
		rows := 24 + int((seed+uint64(r))%9)    // 24..32
		cols := 20 + int((seed*3+uint64(r))%11) // 20..30
		salt := int64(seed%13) + int64(r)
		m := buildMatrix(rows, cols, salt)

		total := sumNested(m)
		ck(total == sumRange(m), "row-major nested vs range")
		ck(total == sumColumns(m), "row-major vs column-major")
		ck(sumInteriorSubslice(m) == sumInteriorRef(m), "interior subslice vs reference")
		ck(m[0][0] == salt && m[rows-1][cols-1] == int64((rows*cols-1)%97)+salt,
			"corner elements")

		j := buildJagged(rows, salt)
		ck(sumJagged(j) == jaggedFormula(rows, salt), "jagged sum formula")
		ck(len(j[0]) == 4 && len(j[rows-1]) == rows+3, "jagged row lengths")

		t3 := buildTensor(4, 5, 6, salt)
		tn := sumTensorNested(t3)
		ck(tn == sumTensorFlat(t3), "tensor nested vs flat")
		if r == 0 {
			kept = m
			keptSum = total
		}

		gcStress()
		ck(sumNested(m) == total, "matrix sum after GC")
		ck(m[rows-1][cols-1] == int64((rows*cols-1)%97)+salt,
			"corner element after GC")
	}

	ck(kept != nil && sumNested(kept) == keptSum, "kept matrix after all rounds")
	ck(sumInteriorSubslice(kept) == sumInteriorRef(kept), "kept interior after GC")

	panicOK := func(name string, fn func()) { expectPanic(name, fn) }
	neg := -1
	lo, hi := 3, 2
	panicOK("row index", func() { _ = kept[len(kept)] })
	panicOK("column index", func() { _ = kept[0][len(kept[0])] })
	panicOK("negative row", func() { _ = kept[neg] })
	panicOK("subslice beyond", func() { _ = kept[0][1 : len(kept[0])+1] })
	panicOK("short slice", func() { _ = kept[0][lo:hi] })

	ck(sumNested(kept) == keptSum, "matrix intact after panic recovery")
	runtime.KeepAlive(kept)

	finish("l11_bce_multidim")
}
