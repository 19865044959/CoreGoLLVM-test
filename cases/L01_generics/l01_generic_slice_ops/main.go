// L01: 泛型切片工具集：Map/Filter/Reduce/Chunk/Distinct/GroupBy/FlatMap/Zip，
// 多类型参数函数实例化 + 指针元素跨 GC 校验，所有结果保持输入顺序。
package main

import (
	"fmt"
	"os"
	"runtime"
	"strconv"
	"sync/atomic"
)

type rec struct {
	id   int64
	name string
	blob []byte
}

func mapSlice[T, U any](in []*T, f func(*T) U) []U {
	out := make([]U, len(in))
	for i, p := range in {
		out[i] = f(p)
	}
	return out
}

func filterSlice[T any](in []*T, keep func(*T) bool) []*T {
	out := make([]*T, 0, len(in))
	for _, p := range in {
		if keep(p) {
			out = append(out, p)
		}
	}
	return out
}

func reduceSlice[T, A any](in []*T, init A, f func(A, *T) A) A {
	acc := init
	for _, p := range in {
		acc = f(acc, p)
	}
	return acc
}

func chunkSlice[T any](in []*T, size int) [][]*T {
	if size < 1 {
		size = 1
	}
	var out [][]*T
	for start := 0; start < len(in); start += size {
		end := start + size
		if end > len(in) {
			end = len(in)
		}
		chunk := make([]*T, end-start)
		copy(chunk, in[start:end])
		out = append(out, chunk)
	}
	return out
}

func distinctSlice[T any, K comparable](in []*T, key func(*T) K) []*T {
	seen := make(map[K]bool, len(in))
	out := make([]*T, 0, len(in))
	for _, p := range in {
		k := key(p)
		if seen[k] {
			continue
		}
		seen[k] = true
		out = append(out, p)
	}
	return out
}

type bucket[K comparable, T any] struct {
	key   K
	items []*T
}

// groupBySlice 按首次出现顺序返回分组，组内保持输入顺序。
func groupBySlice[T any, K comparable](in []*T, key func(*T) K) []bucket[K, T] {
	idx := make(map[K]int, len(in))
	var out []bucket[K, T]
	for _, p := range in {
		k := key(p)
		i, ok := idx[k]
		if !ok {
			idx[k] = len(out)
			out = append(out, bucket[K, T]{key: k})
			i = len(out) - 1
		}
		out[i].items = append(out[i].items, p)
	}
	return out
}

func flatMapSlice[T, U any](in []*T, f func(*T) []U) []U {
	var out []U
	for _, p := range in {
		out = append(out, f(p)...)
	}
	return out
}

type pairT[T, U any] struct {
	a *T
	b *U
}

func zipSlice[T, U any](a []*T, b []*U) []pairT[T, U] {
	n := len(a)
	if len(b) < n {
		n = len(b)
	}
	out := make([]pairT[T, U], n)
	for i := 0; i < n; i++ {
		out[i] = pairT[T, U]{a: a[i], b: b[i]}
	}
	return out
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
	n := 260 + int(uafSeed%61)
	state := uint64(uafSeed)*0x9e3779b97f4a7c15 + 0x1234abcd

	recs := make([]*rec, n)
	in := make([]*rec, n)
	for i := range recs {
		recs[i] = &rec{id: int64(i), name: fmt.Sprintf("s%d", i), blob: make([]byte, 7+i%5)}
		recs[i].blob[0] = byte(i)
		recs[i].blob[len(recs[i].blob)-1] = byte(i >> 1)
		in[i] = recs[i]
	}
	recSink = recs
	for i := n - 1; i > 0; i-- {
		j := int(nextRand(&state) % uint64(i+1))
		in[i], in[j] = in[j], in[i]
	}
	gcStress()

	// Filter：奇数 id，保持乱序输入中的相对顺序。
	odd := filterSlice(in, func(p *rec) bool { return p.id%2 == 1 })
	wantOdd := 0
	for _, p := range in {
		if p.id%2 == 1 {
			ck(p == odd[wantOdd], "Filter order/pointer mismatch")
			wantOdd++
		}
	}
	ck(len(odd) == wantOdd, "Filter length mismatch")

	// Map：两种目标类型，验证多类型参数实例化。
	ids := mapSlice(in, func(p *rec) int64 { return p.id * 2 })
	names := mapSlice(in, func(p *rec) string { return p.name + "!" })
	ck(len(ids) == n && len(names) == n, "Map length mismatch")
	for i := 0; i < n; i++ {
		ck(ids[i] == in[i].id*2, "Map int64 result corrupted")
		ck(names[i] == in[i].name+"!", "Map string result corrupted")
	}

	// Reduce：求和与拼接。
	sum := reduceSlice(in, int64(0), func(acc int64, p *rec) int64 { return acc + p.id })
	var wantSum int64
	for _, p := range in {
		wantSum += p.id
	}
	ck(sum == wantSum, fmt.Sprintf("Reduce sum got=%d want=%d", sum, wantSum))
	label := reduceSlice(in, "", func(acc string, p *rec) string {
		if p.id%97 == 0 {
			return acc + p.name + ";"
		}
		return acc
	})
	wantLabel := ""
	for _, p := range in {
		if p.id%97 == 0 {
			wantLabel += p.name + ";"
		}
	}
	ck(label == wantLabel, "Reduce string result corrupted")

	// Chunk：最后一块可短，所有元素指针按序保留。
	chunks := chunkSlice(in, 7)
	ck(len(chunks) == (n+6)/7, fmt.Sprintf("Chunk count got=%d want=%d", len(chunks), (n+6)/7))
	k := 0
	for ci, ch := range chunks {
		wantLen := 7
		if ci == len(chunks)-1 {
			wantLen = n - 7*(len(chunks)-1)
		}
		ck(len(ch) == wantLen, "Chunk length mismatch")
		for _, p := range ch {
			ck(p == in[k], "Chunk element pointer mismatch")
			k++
		}
	}
	ck(k == n, "Chunk lost elements")

	// Distinct：按 id%23 去重，保留首次出现。
	uniq := distinctSlice(in, func(p *rec) int64 { return p.id % 23 })
	seen := make(map[int64]bool)
	wantUniq := 0
	for _, p := range in {
		key := p.id % 23
		if seen[key] {
			continue
		}
		seen[key] = true
		ck(p == uniq[wantUniq], "Distinct order/pointer mismatch")
		wantUniq++
	}
	ck(len(uniq) == wantUniq, "Distinct length mismatch")

	// GroupBy：首次出现顺序确定，不依赖 map 遍历。
	groups := groupBySlice(in, func(p *rec) int64 { return p.id % 5 })
	order := make([]int64, 0, 5)
	manual := make(map[int64][]*rec)
	for _, p := range in {
		key := p.id % 5
		if _, ok := manual[key]; !ok {
			order = append(order, key)
		}
		manual[key] = append(manual[key], p)
	}
	ck(len(groups) == len(order), fmt.Sprintf("GroupBy groups got=%d want=%d", len(groups), len(order)))
	for gi, b := range groups {
		ck(b.key == order[gi], "GroupBy key order mismatch")
		want := manual[b.key]
		ck(len(b.items) == len(want), "GroupBy group size mismatch")
		for i := range want {
			ck(b.items[i] == want[i], "GroupBy item pointer mismatch")
		}
	}

	// FlatMap：每个元素展开为两个新值，nil 展开为空。
	flat := flatMapSlice(in, func(p *rec) []int64 {
		if p.id%13 == 0 {
			return nil
		}
		v := p.id
		return []int64{v, -v - 1}
	})
	wantFlat := 0
	for _, p := range in {
		if p.id%13 == 0 {
			continue
		}
		ck(flat[wantFlat] == p.id && flat[wantFlat+1] == -p.id-1, "FlatMap value mismatch")
		wantFlat += 2
	}
	ck(len(flat) == wantFlat, "FlatMap length mismatch")

	// Zip：长度取短边，配对指针正确。
	nums := make([]*int64, n+5)
	for i := range nums {
		v := int64(i * 11)
		nums[i] = &v
	}
	zipped := zipSlice(in, nums)
	ck(len(zipped) == n, "Zip length mismatch")
	for i, pr := range zipped {
		ck(pr.a == in[i] && pr.b == nums[i], "Zip pair pointer mismatch")
		ck(*pr.b == int64(i*11), "Zip value corrupted")
	}

	gcStress()
	for i := 0; i < n; i++ {
		ck(in[i].id == ids[i]/2, "Map result id corrupted after GC")
		ck(len(in[i].blob) == 7+int(in[i].id)%5, "input blob corrupted after GC")
	}
	for i, pr := range zipped {
		ck(pr.a == in[i] && pr.b == nums[i], "Zip pointer changed after GC")
	}
	for gi, b := range groups {
		for _, p := range b.items {
			ck(p.id >= 0 && p.id < int64(n), "GroupBy payload id corrupted after GC")
			ck(p.blob[0] == byte(p.id), "GroupBy payload blob corrupted after GC")
		}
		ck(b.key == order[gi], "GroupBy key order changed after GC")
	}

	// 第二次实例化：[]*string 上的 Map/Filter。
	strs := make([]*string, 24)
	for i := range strs {
		s := fmt.Sprintf("v%02d", i)
		strs[i] = &s
	}
	upper := mapSlice(strs, func(s *string) string { return "u" + *s })
	even := filterSlice(strs, func(s *string) bool {
		v, _ := strconv.Atoi((*s)[1:])
		return v%2 == 0
	})
	ck(len(even) == 12, fmt.Sprintf("string filter got=%d want=12", len(even)))
	for i := 0; i < 24; i++ {
		ck(upper[i] == "uv"+fmt.Sprintf("%02d", i), "string map result corrupted")
	}
	gcStress()
	for _, p := range even {
		ck(len(*p) == 3 && (*p)[0] == 'v', "string filter pointer corrupted after GC")
	}

	finish("l01_generic_slice_ops")
}
