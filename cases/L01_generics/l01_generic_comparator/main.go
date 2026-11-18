// L01: 泛型排序/比较器：多键比较器组合、稳定排序、泛型堆 top-k，跨 GC 校验指针与顺序。
package main

import (
	"container/heap"
	"fmt"
	"os"
	"runtime"
	"sort"
	"strconv"
	"sync/atomic"
)

type rec struct {
	dept  int64
	score int64
	name  string
	seq   int
	blob  []byte
}

type cmpFn[T any] func(a, b T) int

func then[T any](cs ...cmpFn[T]) cmpFn[T] {
	return func(a, b T) int {
		for _, c := range cs {
			if v := c(a, b); v != 0 {
				return v
			}
		}
		return 0
	}
}

func reversed[T any](c cmpFn[T]) cmpFn[T] {
	return func(a, b T) int { return -c(a, b) }
}

func stableSort[T any](xs []T, c cmpFn[T]) {
	sort.SliceStable(xs, func(i, j int) bool { return c(xs[i], xs[j]) < 0 })
}

// refStable 是独立插入排序，用于给出稳定排序的参照顺序。
func refStable[T any](xs []T, c cmpFn[T]) []T {
	out := append([]T(nil), xs...)
	for i := 1; i < len(out); i++ {
		v := out[i]
		j := i - 1
		for j >= 0 && c(v, out[j]) < 0 {
			out[j+1] = out[j]
			j--
		}
		out[j+1] = v
	}
	return out
}

// binHeap 是泛型二叉堆，谓词由比较器提供（container/heap 接口）。
type binHeap[T any] struct {
	data []T
	cmp  cmpFn[T]
}

func (h *binHeap[T]) Len() int           { return len(h.data) }
func (h *binHeap[T]) Less(i, j int) bool { return h.cmp(h.data[i], h.data[j]) < 0 }
func (h *binHeap[T]) Swap(i, j int)      { h.data[i], h.data[j] = h.data[j], h.data[i] }
func (h *binHeap[T]) Push(x any)         { h.data = append(h.data, x.(T)) }
func (h *binHeap[T]) Pop() any {
	old := h.data
	n := len(old)
	x := old[n-1]
	h.data = old[:n-1]
	return x
}

func intCmp(a, b int64) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	default:
		return 0
	}
}

var (
	failed  int32
	gcSink  [][]byte
	recSink []*rec
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

func nextRand(state *uint64) uint64 {
	*state = *state*6364136223846793005 + 1442695040888963407
	return *state >> 33
}

func main() {
	uafSeed = envSeed()
	n := 240 + int(uafSeed%24)

	// 组合比较器：dept 升序、score 降序、name 升序；seq 不参与比较以检验稳定性。
	byDept := func(a, b *rec) int { return intCmp(a.dept, b.dept) }
	byScoreDesc := reversed(func(a, b *rec) int { return intCmp(a.score, b.score) })
	byName := func(a, b *rec) int {
		switch {
		case a.name < b.name:
			return -1
		case a.name > b.name:
			return 1
		default:
			return 0
		}
	}
	cmp := then(byDept, byScoreDesc, byName)

	items := make([]*rec, n)
	for i := 0; i < n; i++ {
		items[i] = &rec{
			dept:  int64(i % 3),
			score: int64((i * 7) % 5),
			name:  fmt.Sprintf("g%d", i%4),
			seq:   i,
			blob:  []byte{byte(i), byte(i * 3)},
		}
	}
	recSink = items

	state := uint64(uafSeed)*0x9e3779b97f4a7c15 + 11
	input := append([]*rec(nil), items...)
	for i := len(input) - 1; i > 0; i-- {
		j := int(nextRand(&state) % uint64(i+1))
		input[i], input[j] = input[j], input[i]
	}
	inputPos := make(map[*rec]int, len(input))
	for i, r := range input {
		inputPos[r] = i
	}

	want := refStable(input, cmp)
	got := append([]*rec(nil), input...)
	stableSort(got, cmp)
	gcStress()

	for i := 0; i < n; i++ {
		ck(got[i] == want[i], fmt.Sprintf("stable sort pointer mismatch at %d", i))
		if i > 0 {
			ck(cmp(got[i-1], got[i]) <= 0, fmt.Sprintf("sorted order violated at %d", i))
			if cmp(got[i-1], got[i]) == 0 {
				ck(inputPos[got[i-1]] < inputPos[got[i]], "stable sort reordered equal elements")
			}
		}
		ck(got[i].blob[0] == byte(got[i].seq) && got[i].blob[1] == byte(got[i].seq*3), "sorted payload corrupted after GC")
	}

	// 泛型堆：按同一比较器弹出全序；中途 GC。
	h := &binHeap[*rec]{cmp: cmp}
	for _, r := range got {
		heap.Push(h, r)
		if h.Len() == n/2 {
			gcStress()
		}
	}
	var prev *rec
	seen := make(map[*rec]bool, n)
	for h.Len() > 0 {
		cur := heap.Pop(h).(*rec)
		ck(!seen[cur], "heap returned a duplicate element")
		seen[cur] = true
		ck(cur.blob[0] == byte(cur.seq), "heap element payload corrupted")
		if prev != nil {
			ck(cmp(prev, cur) <= 0, "heap pop order violated")
		}
		prev = cur
		if h.Len() == n/4 {
			gcStress()
		}
	}
	ck(len(seen) == n, fmt.Sprintf("heap popped got=%d want=%d", len(seen), n))

	// 第二次实例化 binHeap[int64] + 组合器语义表。
	nums := make([]int64, 64)
	for i := range nums {
		nums[i] = int64((i*37 + 9) % 61)
	}
	ih := &binHeap[int64]{cmp: intCmp}
	for _, v := range nums {
		heap.Push(ih, v)
	}
	gcStress()
	var last int64 = -1 << 62
	for ih.Len() > 0 {
		v := heap.Pop(ih).(int64)
		ck(v >= last, "int heap pop order violated")
		last = v
	}

	asc := intCmp
	desc := reversed(asc)
	ck(desc(1, 2) > 0 && desc(2, 1) < 0 && desc(3, 3) == 0, "reversed comparator semantics broken")
	pair := then(func(a, b [2]int64) int { return intCmp(a[0], b[0]) }, func(a, b [2]int64) int { return intCmp(a[1], b[1]) })
	ck(pair([2]int64{1, 9}, [2]int64{1, 2}) > 0, "then comparator second key not used")
	ck(pair([2]int64{1, 2}, [2]int64{2, 0}) < 0, "then comparator first key not used")
	ck(pair([2]int64{3, 4}, [2]int64{3, 4}) == 0, "then comparator equality broken")

	finish("l01_generic_comparator")
}
