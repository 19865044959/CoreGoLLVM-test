// L13: 排序稳定性：sort.SliceStable/sort.Stable 对重复 key 保持输入次序，并与 sort.Search 边界交叉校验。
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

type row struct {
	key  int
	sub  int
	seq  int
	name string
	pad  []byte
}

type byKey []*row

func (b byKey) Len() int           { return len(b) }
func (b byKey) Less(i, j int) bool { return b[i].key < b[j].key }
func (b byKey) Swap(i, j int)      { b[i], b[j] = b[j], b[i] }

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

func envSeed() int64 {
	s := os.Getenv("UAF_SEED")
	if s == "" {
		return 1
	}
	v, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return 1
	}
	return v
}

func newRow(i int, salt int64) *row {
	r := &row{key: i % 9, sub: (i / 9) % 3, seq: i, name: fmt.Sprintf("row-%05d", i), pad: make([]byte, 10)}
	for k := range r.pad {
		r.pad[k] = byte(i*13 + k*7 + int(salt))
	}
	return r
}

func validRow(r *row, salt int64) bool {
	if r == nil || r.name != fmt.Sprintf("row-%05d", r.seq) || len(r.pad) != 10 {
		return false
	}
	for i := range r.pad {
		if r.pad[i] != byte(r.seq*13+i*7+int(salt)) {
			return false
		}
	}
	return true
}

func keysSorted(rows []*row) bool {
	for i := 1; i < len(rows); i++ {
		if rows[i-1].key > rows[i].key {
			return false
		}
	}
	return true
}

func stableByKey(rows []*row, rank map[*row]int) bool {
	for i := 1; i < len(rows); i++ {
		if rows[i-1].key == rows[i].key && rank[rows[i-1]] > rank[rows[i]] {
			return false
		}
	}
	return true
}

func main() {
	curSeed = envSeed()
	rnd := rand.New(rand.NewSource(curSeed))

	const n = 900
	for round := 0; round < 12; round++ {
		base := make([]*row, n)
		for i := range base {
			base[i] = newRow(i, curSeed)
		}
		input := append([]*row(nil), base...)
		rnd.Shuffle(len(input), func(i, j int) { input[i], input[j] = input[j], input[i] })
		rank := make(map[*row]int, n)
		for i, row := range input {
			rank[row] = i
		}

		// sort.SliceStable：key 有序且同 key 保持输入相对次序。
		sorted := append([]*row(nil), input...)
		sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].key < sorted[j].key })
		ckSeed(keysSorted(sorted), fmt.Sprintf("stable: slice keys unsorted at round %d", round))
		ckSeed(stableByKey(sorted, rank), fmt.Sprintf("stable: slice order broke at round %d", round))
		for i := 0; i < n; i++ {
			if !validRow(sorted[i], curSeed) {
				ckSeed(false, fmt.Sprintf("stable: slice payload corrupt at round %d idx %d", round, i))
				break
			}
		}

		// 已有序切片再稳定排序必须保持完全相同指针序列。
		before := append([]*row(nil), sorted...)
		sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].key < sorted[j].key })
		for i := 0; i < n; i++ {
			if sorted[i] != before[i] {
				ckSeed(false, fmt.Sprintf("stable: resort changed order at round %d idx %d", round, i))
				break
			}
		}

		// sort.Stable + 自定义 sort.Interface。
		custom := make(byKey, n)
		copy(custom, input)
		sort.Stable(custom)
		ckSeed(keysSorted(custom), fmt.Sprintf("stable: interface keys unsorted at round %d", round))
		ckSeed(stableByKey(custom, rank), fmt.Sprintf("stable: interface order broke at round %d", round))

		// 不稳定 sort.Sort 只要求 key 有序、元素集合不变。
		unstable := make(byKey, n)
		copy(unstable, input)
		sort.Sort(unstable)
		ckSeed(keysSorted(unstable), fmt.Sprintf("stable: unstable sort keys at round %d", round))
		cnt := [9]int{}
		for _, row := range unstable {
			cnt[row.key]++
		}
		for k := 0; k < 9; k++ {
			want := 0
			for _, row := range input {
				if row.key == k {
					want++
				}
			}
			if cnt[k] != want {
				ckSeed(false, fmt.Sprintf("stable: unstable multiset key %d at round %d", k, round))
				break
			}
		}

		// 多键稳定排序：(key, sub) 均相同的元素保持输入次序。
		multi := append([]*row(nil), input...)
		sort.SliceStable(multi, func(i, j int) bool {
			if multi[i].key != multi[j].key {
				return multi[i].key < multi[j].key
			}
			return multi[i].sub < multi[j].sub
		})
		for i := 0; i < n; i++ {
			if i > 0 {
				a, b := multi[i-1], multi[i]
				if a.key > b.key || (a.key == b.key && a.sub > b.sub) {
					ckSeed(false, fmt.Sprintf("stable: multi key unsorted at round %d idx %d", round, i))
					break
				}
				if a.key == b.key && a.sub == b.sub && rank[a] > rank[b] {
					ckSeed(false, fmt.Sprintf("stable: multi order broke at round %d idx %d", round, i))
					break
				}
			}
		}

		// sort.Search 在重复 key 上求下界/上界。
		for k := 0; k < 9; k++ {
			lo := sort.Search(len(sorted), func(i int) bool { return sorted[i].key >= k })
			hi := sort.Search(len(sorted), func(i int) bool { return sorted[i].key > k })
			want := 0
			for _, row := range input {
				if row.key == k {
					want++
				}
			}
			if hi-lo != want {
				ckSeed(false, fmt.Sprintf("stable: search bounds key %d at round %d", k, round))
				break
			}
			for i := lo; i < hi; i++ {
				if sorted[i].key != k {
					ckSeed(false, fmt.Sprintf("stable: search range key %d at round %d", k, round))
					break
				}
			}
		}

		gcStress()
		for i := 0; i < n; i++ {
			if sorted[i] != before[i] || !validRow(sorted[i], curSeed) {
				ckSeed(false, fmt.Sprintf("stable: pointers corrupt after GC at round %d idx %d", round, i))
				break
			}
		}
		runtime.KeepAlive(input)
		runtime.KeepAlive(base)
	}

	finish("l13_sort_stable_dups")
}
