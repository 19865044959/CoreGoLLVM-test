// L01: 带数值约束的泛型栈：numeric 约束 + 泛型 cell[T] 负载，
// 维护并行单调栈以 O(1) 取 Min/Max/Sum，跨 GC 校验指针身份与聚合值。
package main

import (
	"fmt"
	"os"
	"runtime"
	"strconv"
	"sync/atomic"
)

type numeric interface {
	~int64 | ~int32 | ~float64
}

type cell[T numeric] struct {
	val  T
	blob []byte
}

type NumStack[T numeric] struct {
	items []*cell[T]
	mins  []T // 与 items 等长：mins[i] 是 items[0..i] 的运行最小值
	maxs  []T // 与 items 等长：maxs[i] 是 items[0..i] 的运行最大值
	sum   T
}

func (s *NumStack[T]) Push(c *cell[T]) {
	s.items = append(s.items, c)
	s.sum += c.val
	if n := len(s.mins); n == 0 || c.val < s.mins[n-1] {
		s.mins = append(s.mins, c.val)
	} else {
		s.mins = append(s.mins, s.mins[n-1])
	}
	if n := len(s.maxs); n == 0 || c.val > s.maxs[n-1] {
		s.maxs = append(s.maxs, c.val)
	} else {
		s.maxs = append(s.maxs, s.maxs[n-1])
	}
}

func (s *NumStack[T]) Pop() (*cell[T], bool) {
	if len(s.items) == 0 {
		return nil, false
	}
	c := s.items[len(s.items)-1]
	s.items = s.items[:len(s.items)-1]
	s.mins = s.mins[:len(s.mins)-1]
	s.maxs = s.maxs[:len(s.maxs)-1]
	s.sum -= c.val
	return c, true
}

func (s *NumStack[T]) Min() (T, bool) {
	var zero T
	if len(s.mins) == 0 {
		return zero, false
	}
	return s.mins[len(s.mins)-1], true
}

func (s *NumStack[T]) Max() (T, bool) {
	var zero T
	if len(s.maxs) == 0 {
		return zero, false
	}
	return s.maxs[len(s.maxs)-1], true
}

func (s *NumStack[T]) Sum() T   { return s.sum }
func (s *NumStack[T]) Len() int { return len(s.items) }

func (s *NumStack[T]) recompute() (T, T, T) {
	var sum, mn, mx T
	for i, c := range s.items {
		sum += c.val
		if i == 0 || c.val < mn {
			mn = c.val
		}
		if i == 0 || c.val > mx {
			mx = c.val
		}
	}
	return sum, mn, mx
}

var (
	failed   int32
	gcSink   [][]byte
	cellSink [][]*cell[int64]
	uafSeed  int64 = 1
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

func main() {
	uafSeed = envSeed()
	n := 180 + int(uafSeed%40)

	// int64 实例化：值域覆盖负数、重复最小值/最大值。
	st := &NumStack[int64]{}
	cells := make([]*cell[int64], n)
	for i := 0; i < n; i++ {
		v := int64((i*37+11)%97) - 48
		cells[i] = &cell[int64]{val: v, blob: make([]byte, 5+i%7)}
		cells[i].blob[0] = byte(i)
		cells[i].blob[len(cells[i].blob)-1] = byte(v + 64)
		st.Push(cells[i])
		if i == n/2 {
			gcStress()
		}
	}
	cellSink = append(cellSink, cells)
	gcStress()

	ck(st.Len() == n, fmt.Sprintf("len got=%d want=%d", st.Len(), n))
	sum, mn, mx := st.recompute()
	gotSum := st.Sum()
	gotMin, okMin := st.Min()
	gotMax, okMax := st.Max()
	ck(gotSum == sum, fmt.Sprintf("sum got=%d want=%d", gotSum, sum))
	ck(okMin && gotMin == mn, fmt.Sprintf("min got=%d want=%d", gotMin, mn))
	ck(okMax && gotMax == mx, fmt.Sprintf("max got=%d want=%d", gotMax, mx))
	for i := 0; i < n; i++ {
		ck(st.items[i] == cells[i], fmt.Sprintf("stack item %d pointer identity changed", i))
		ck(st.items[i].val == cells[i].val && st.items[i].blob[0] == byte(i), "stack payload corrupted after GC")
	}

	// 弹出后半段，每步校验返回指针与聚合值。
	for i := n - 1; i >= n/2; i-- {
		c, ok := st.Pop()
		ck(ok && c == cells[i], fmt.Sprintf("pop %d pointer mismatch", i))
		if c != nil {
			ck(c.val == cells[i].val && c.blob[len(c.blob)-1] == byte(c.val+64), "popped payload corrupted")
		}
		if i%32 == 0 {
			gcStress()
			sum, mn, mx = st.recompute()
			gotSum = st.Sum()
			gotMin, okMin = st.Min()
			gotMax, okMax = st.Max()
			ck(gotSum == sum, fmt.Sprintf("post-pop sum got=%d want=%d", gotSum, sum))
			ck(okMin && gotMin == mn, "post-pop min mismatch")
			ck(okMax && gotMax == mx, "post-pop max mismatch")
		}
	}
	ck(st.Len() == n/2, "len after half pops mismatch")
	gcStress()

	// 清空后 Min/Max 必须不可用，Sum 归零。
	for st.Len() > 0 {
		_, ok := st.Pop()
		ck(ok, "pop from non-empty stack failed")
	}
	_, okMin = st.Min()
	_, okMax = st.Max()
	ck(!okMin && !okMax, "empty stack exposed min/max")
	ck(st.Sum() == 0, "empty stack sum not zero")
	gcStress()

	// int32 实例化：同一套约束、不同底层类型。
	s32 := &NumStack[int32]{}
	vals32 := []int32{7, -3, 9, -3, 0, 9, 2}
	for i, v := range vals32 {
		s32.Push(&cell[int32]{val: v, blob: []byte{byte(i)}})
	}
	gcStress()
	min32, _ := s32.Min()
	max32, _ := s32.Max()
	var sum32 int32
	for _, v := range vals32 {
		sum32 += v
	}
	ck(s32.Sum() == sum32 && min32 == -3 && max32 == 9, "int32 constrained stack aggregate corrupted")
	p32, ok32 := s32.Pop()
	ck(ok32 && p32 != nil && p32.val == 2, "int32 constrained stack pop corrupted")

	// float64 实例化：整数值保证求和/比较精确。
	sf := &NumStack[float64]{}
	valsF := make([]float64, 25)
	for i := range valsF {
		valsF[i] = float64((i*11)%31) - 15
		sf.Push(&cell[float64]{val: valsF[i], blob: []byte{byte(i), 0x5a}})
	}
	gcStress()
	var sumF float64
	mnF, mxF := valsF[0], valsF[0]
	for _, v := range valsF {
		sumF += v
		if v < mnF {
			mnF = v
		}
		if v > mxF {
			mxF = v
		}
	}
	gotMnF, _ := sf.Min()
	gotMxF, _ := sf.Max()
	ck(sf.Sum() == sumF, "float64 stack sum corrupted")
	ck(gotMnF == mnF && gotMxF == mxF, "float64 stack min/max corrupted")
	for sf.Len() > 0 {
		c, ok := sf.Pop()
		ck(ok && c.blob[1] == 0x5a, "float64 stack payload corrupted")
	}
	ck(sf.Sum() == 0, "float64 stack sum not zero after drain")

	finish("l01_constrained_stack")
}
