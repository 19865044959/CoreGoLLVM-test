// L01: 泛型矩阵：+ 约束 union（~int64|~float64），转置/乘法/单位阵与代数恒等式，跨 GC 校验。
package main

import (
	"fmt"
	"os"
	"runtime"
	"strconv"
	"sync/atomic"
)

type number interface {
	~int64 | ~float64
}

type Matrix[T number] struct {
	r, c int
	d    []T
}

func NewMatrix[T number](r, c int) *Matrix[T] {
	if r < 0 || c < 0 {
		r, c = 0, 0
	}
	return &Matrix[T]{r: r, c: c, d: make([]T, r*c)}
}

func (m *Matrix[T]) Rows() int { return m.r }
func (m *Matrix[T]) Cols() int { return m.c }

func (m *Matrix[T]) At(i, j int) T { return m.d[i*m.c+j] }

func (m *Matrix[T]) Set(i, j int, v T) { m.d[i*m.c+j] = v }

func (m *Matrix[T]) Fill(fn func(i, j int) T) {
	for i := 0; i < m.r; i++ {
		for j := 0; j < m.c; j++ {
			m.d[i*m.c+j] = fn(i, j)
		}
	}
}

func (m *Matrix[T]) Transpose() *Matrix[T] {
	out := NewMatrix[T](m.c, m.r)
	for i := 0; i < m.r; i++ {
		for j := 0; j < m.c; j++ {
			out.d[j*out.c+i] = m.d[i*m.c+j]
		}
	}
	return out
}

func (m *Matrix[T]) Mul(o *Matrix[T]) *Matrix[T] {
	if m.c != o.r {
		return nil
	}
	out := NewMatrix[T](m.r, o.c)
	for i := 0; i < m.r; i++ {
		for j := 0; j < o.c; j++ {
			var acc T
			for k := 0; k < m.c; k++ {
				acc += m.d[i*m.c+k] * o.d[k*o.c+j]
			}
			out.d[i*out.c+j] = acc
		}
	}
	return out
}

func (m *Matrix[T]) Sum() T {
	var s T
	for _, v := range m.d {
		s += v
	}
	return s
}

func (m *Matrix[T]) Equal(o *Matrix[T]) bool {
	if m.r != o.r || m.c != o.c {
		return false
	}
	for i := range m.d {
		if m.d[i] != o.d[i] {
			return false
		}
	}
	return true
}

func Identity[T number](n int) *Matrix[T] {
	m := NewMatrix[T](n, n)
	for i := 0; i < n; i++ {
		m.Set(i, i, T(1))
	}
	return m
}

var (
	failed  int32
	gcSink  [][]byte
	uafSeed int64 = 1
)

func ck(ok bool, msg string) {
	if !ok {
		atomic.AddInt32(&failed, 1)
		fmt.Println("FAIL:", msg)
	}
}

func gcStress() {
	for i := 0; i < 8; i++ {
		b := make([]byte, 2048)
		b[0] = byte(i)
		gcSink = append(gcSink[:0], b)
	}
	gcSink = gcSink[:0]
	runtime.GC()
	runtime.GC()
}

func finish(name string) {
	if n := atomic.LoadInt32(&failed); n != 0 {
		fmt.Printf("FAILED %s seed=%d (%d failed checks)\n", name, uafSeed, n)
		os.Exit(1)
	}
	fmt.Println("ALL PASS " + name)
}

func envSeed() int64 {
	s := os.Getenv("UAF_SEED")
	if s == "" {
		return 1
	}
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil || n <= 0 {
		return 1
	}
	return n
}

func tortureRounds(def int) int {
	s := os.Getenv("TORTURE_ROUNDS")
	if s == "" {
		return def
	}
	n, err := strconv.Atoi(s)
	if err != nil || n <= 0 {
		return def
	}
	if n > 8 {
		n = 8
	}
	return n
}

func main() {
	uafSeed = envSeed()
	rounds := tortureRounds(1)
	n := (18 + int(uafSeed%4)) * rounds
	mDim := n - 2
	pDim := n + 3

	// A: n×mDim，B: mDim×pDim；填充值由 seed 平移，恒等式与期望值均现场推导。
	shift := int64(uafSeed % 5)
	a := NewMatrix[int64](n, mDim)
	a.Fill(func(i, j int) int64 { return int64((i*7+j*3)%11) - 5 + shift })
	b := NewMatrix[int64](mDim, pDim)
	b.Fill(func(i, j int) int64 { return int64((i*5+j*2)%13) - 6 + shift })
	gcStress()

	sumA, sumB := a.Sum(), b.Sum()
	c := a.Mul(b)
	ck(c != nil && c.Rows() == n && c.Cols() == pDim, "A*B dimension mismatch")
	gcStress()

	// 独立点积校验抽样单元。
	for _, rc := range [][2]int{{0, 0}, {0, pDim / 2}, {n / 2, pDim / 2}, {n - 1, pDim - 1}, {1, pDim - 2}} {
		var want int64
		for k := 0; k < mDim; k++ {
			want += a.At(rc[0], k) * b.At(k, rc[1])
		}
		ck(c.At(rc[0], rc[1]) == want, fmt.Sprintf("C[%d,%d] dot product mismatch", rc[0], rc[1]))
	}
	// 精确恒等式：sum(A*B) = Σ_k colSum(A,k) * rowSum(B,k)。
	var wantSumC int64
	for k := 0; k < mDim; k++ {
		var colA, rowB int64
		for i := 0; i < n; i++ {
			colA += a.At(i, k)
		}
		for j := 0; j < pDim; j++ {
			rowB += b.At(k, j)
		}
		wantSumC += colA * rowB
	}
	ck(c.Sum() == wantSumC, fmt.Sprintf("sum(C) got=%d want=%d (sumA=%d sumB=%d)", c.Sum(), wantSumC, sumA, sumB))

	// (A*B)^T = B^T * A^T，且转置两次还原。
	ct := c.Transpose()
	ck(ct.Rows() == pDim && ct.Cols() == n, "C^T dimension mismatch")
	bta := b.Transpose().Mul(a.Transpose())
	ck(bta != nil && ct.Equal(bta), "(A*B)^T != B^T*A^T")
	ck(ct.Transpose().Equal(c), "transpose is not involutive")
	gcStress()

	// 单位阵恒等：A*I = A，I*A = A。
	ia := a.Mul(Identity[int64](mDim))
	ai := Identity[int64](n).Mul(a)
	ck(ia != nil && ia.Equal(a), "A*I != A")
	ck(ai != nil && ai.Equal(a), "I*A != A")
	ck(a.Mul(NewMatrix[int64](mDim+1, pDim)) == nil, "dimension mismatch should yield nil")

	// 转置后再 GC，抽样回读。
	at := a.Transpose()
	gcStress()
	for i := 0; i < n; i += 5 {
		ck(at.At(0, i) == a.At(i, 0), "A^T payload corrupted after GC")
	}

	// 第二次实例化 Matrix[float64]：小整数运算应精确。
	fa := NewMatrix[float64](6, 5)
	fb := NewMatrix[float64](5, 7)
	fa.Fill(func(i, j int) float64 { return float64((i*3 + j) % 7) })
	fb.Fill(func(i, j int) float64 { return float64((i + j*2) % 5) })
	gcStress()
	fc := fa.Mul(fb)
	ck(fc != nil && fc.Rows() == 6 && fc.Cols() == 7, "float matrix dimension mismatch")
	// 用 int64 版本独立计算同值矩阵，逐元素比较（小整数在 float64 中精确）。
	ia2 := NewMatrix[int64](6, 5)
	ib2 := NewMatrix[int64](5, 7)
	ia2.Fill(func(i, j int) int64 { return int64((i*3 + j) % 7) })
	ib2.Fill(func(i, j int) int64 { return int64((i + j*2) % 5) })
	ic2 := ia2.Mul(ib2)
	gcStress()
	for i := 0; i < 6; i++ {
		for j := 0; j < 7; j++ {
			ck(fc.At(i, j) == float64(ic2.At(i, j)), fmt.Sprintf("float C[%d,%d] mismatch", i, j))
		}
	}
	ck(fc.Sum() == float64(ic2.Sum()), "float matrix sum mismatch")
	ck(fa.Mul(NewMatrix[float64](3, 3)) == nil, "float dimension mismatch should yield nil")

	finish("l01_generic_matrix")
}
