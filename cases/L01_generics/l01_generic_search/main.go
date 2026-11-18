// L01: 泛型排序/查找处理指针元素：sort.Slice + sort.Search 手写 LowerBound，跨 GC 校验。
package main

import (
	"fmt"
	"os"
	"runtime"
	"sort"
	"strconv"
	"sync/atomic"
)

type Item struct {
	Key    int64
	Name   string
	Weight int64
}

type num struct {
	v int64
}

func SortBy[T any](xs []*T, less func(a, b *T) bool) {
	sort.Slice(xs, func(i, j int) bool { return less(xs[i], xs[j]) })
}

// LowerBound 返回首个不小于 target 的位置；found 表示该位置与 target 等价。
func LowerBound[T any](xs []*T, target *T, less func(a, b *T) bool) (int, bool) {
	i := sort.Search(len(xs), func(i int) bool { return !less(xs[i], target) })
	if i < len(xs) && !less(target, xs[i]) {
		return i, true
	}
	return i, false
}

func FindFirst[T any](xs []*T, pred func(*T) bool) int {
	return sort.Search(len(xs), func(i int) bool { return pred(xs[i]) })
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
	if n > 32 {
		n = 32
	}
	return n
}

func nextRand(state *uint64) uint64 {
	*state = *state*6364136223846793005 + 1442695040888963407
	return *state >> 33
}

func main() {
	uafSeed = envSeed()
	rounds := tortureRounds(1)
	n := 192 * rounds

	// 确定性 LCG 洗牌：UAF_SEED 只影响初始顺序，不影响期望结果。
	state := uint64(uafSeed)*0x9e3779b97f4a7c15 + 0x123456789
	order := make([]int, n)
	for i := range order {
		order[i] = i
	}
	for i := n - 1; i > 0; i-- {
		j := int(nextRand(&state) % uint64(i+1))
		order[i], order[j] = order[j], order[i]
	}
	items := make([]*Item, n)
	var wantSum int64
	for idx, o := range order {
		key := int64(o*3 + 7)
		items[idx] = &Item{Key: key, Name: fmt.Sprintf("k%d", key), Weight: key*2 + 1}
		wantSum += key*2 + 1
	}
	gcStress()
	for idx := range items {
		ck(items[idx] != nil, "item became nil before sort")
	}

	lessKey := func(a, b *Item) bool { return a.Key < b.Key }
	SortBy(items, lessKey)
	gcStress()

	// 排序后 key 必须是 7,10,13,... 的严格递增序列。
	var gotSum int64
	for i := 0; i < n; i++ {
		it := items[i]
		wantKey := int64(i*3 + 7)
		ck(it != nil, "item became nil after sort")
		if it == nil {
			continue
		}
		ck(it.Key == wantKey, fmt.Sprintf("sorted key[%d] got=%d want=%d", i, it.Key, wantKey))
		ck(it.Name == fmt.Sprintf("k%d", wantKey), "sorted item name corrupted")
		ck(it.Weight == wantKey*2+1, "sorted item weight corrupted")
		gotSum += it.Weight
	}
	ck(gotSum == wantSum, fmt.Sprintf("sorted sum got=%d want=%d", gotSum, wantSum))

	// 命中查找：首/中/尾三个元素。
	for _, wantIdx := range []int{0, n / 2, n - 1} {
		wantKey := int64(wantIdx*3 + 7)
		probe := &Item{Key: wantKey}
		idx, found := LowerBound(items, probe, lessKey)
		ck(found, "LowerBound missed present key")
		ck(idx == wantIdx, fmt.Sprintf("LowerBound index got=%d want=%d", idx, wantIdx))
		if idx >= 0 && idx < len(items) && items[idx] != nil {
			ck(items[idx].Key == wantKey && items[idx].Weight == wantKey*2+1, "LowerBound payload corrupted")
		}
	}

	// 未命中：区间内缺失 key、下界以下、上界以上。
	probe := &Item{Key: 8}
	idx, found := LowerBound(items, probe, lessKey)
	ck(!found && idx == 1, fmt.Sprintf("missing mid key idx got=%d want=1", idx))
	probe = &Item{Key: 1}
	idx, found = LowerBound(items, probe, lessKey)
	ck(!found && idx == 0, "missing key below min should insert at 0")
	probe = &Item{Key: int64(n*3 + 7)}
	idx, found = LowerBound(items, probe, lessKey)
	ck(!found && idx == n, "missing key above max should insert at n")

	// GC 后再次查找，确保排序结果与指针在收集中保持。
	gcStress()
	probe = &Item{Key: int64((n/2)*3 + 7)}
	idx, found = LowerBound(items, probe, lessKey)
	ck(found && idx == n/2, "post-GC LowerBound failed")
	if found && idx < len(items) && items[idx] != nil {
		ck(items[idx].Name == fmt.Sprintf("k%d", (n/2)*3+7), "post-GC found payload corrupted")
	}

	// 谓词首次匹配（亦基于 sort.Search）。
	threshold := int64(n * 3 / 2)
	wantIdx := -1
	for i, it := range items {
		if it != nil && it.Weight >= threshold {
			wantIdx = i
			break
		}
	}
	gotIdx := FindFirst(items, func(it *Item) bool { return it != nil && it.Weight >= threshold })
	ck(gotIdx == wantIdx, fmt.Sprintf("FindFirst got=%d want=%d", gotIdx, wantIdx))

	// 第二个实例化 SortBy[*num]，排序数值指针并 GC。
	nn := 16 + int(uafSeed%8)
	nums := make([]*num, nn)
	for i := 0; i < nn; i++ {
		nums[i] = &num{v: int64((i*7 + 3) % nn)}
	}
	SortBy(nums, func(a, b *num) bool { return a.v < b.v })
	gcStress()
	for i := 1; i < nn; i++ {
		ck(nums[i-1].v <= nums[i].v, "num slice not sorted after GC")
	}
	probeNum := &num{v: nums[nn/2].v}
	nidx, nfound := LowerBound(nums, probeNum, func(a, b *num) bool { return a.v < b.v })
	ck(nfound && nums[nidx].v >= probeNum.v, "num LowerBound failed after GC")

	fmt.Printf("rounds=%d items=%d sum=%d\n", rounds, n, gotSum)
	finish("l01_generic_search")
}
