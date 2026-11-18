// L16: reflect.Value 迭代器与值操作边界：Seq/Seq2 覆盖 slice/array/string/int/func/map，
// SetIterKey/SetIterValue 免分配写入，非法 Kind panic；结果跨 GC 与重复执行稳定。
package main

import (
	"fmt"
	"math/rand"
	"os"
	"reflect"
	"runtime"
	"sort"
	"strconv"
	"sync/atomic"
)

type pair struct {
	k string
	v int64
}

var (
	failed  int32
	gcSink  [][]byte
	live    []any
	uafSeed int64 = 1
)

func ck(ok bool, msg string) {
	if !ok {
		atomic.AddInt32(&failed, 1)
		fmt.Println("FAIL:", msg)
	}
}

func gcStress() {
	for i := 0; i < 16; i++ {
		b := make([]byte, 8192)
		b[0] = byte(i)
		gcSink = append(gcSink[:0], b)
	}
	for i := 0; i < 64; i++ {
		_ = make([]reflect.Value, 32)
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

func panics(f func()) (p bool) {
	defer func() {
		if recover() != nil {
			p = true
		}
	}()
	f()
	return false
}

func seqInts(v reflect.Value) []int64 {
	var out []int64
	for x := range v.Seq() {
		out = append(out, x.Int())
	}
	return out
}

func seqPairs(v reflect.Value) []pair {
	var out []pair
	for k, val := range v.Seq2() {
		out = append(out, pair{k: k.String(), v: val.Int()})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].k < out[j].k })
	return out
}

func seqFunc(v reflect.Value) []int64 {
	var out []int64
	for x := range v.Seq() {
		out = append(out, x.Int())
		if len(out) == 2 {
			break
		}
	}
	return out
}

func runSuite(tag string, m map[string]int64) ([]any, bool) {
	var held []any
	okAll := true
	note := func(ok bool, msg string) {
		ck(ok, tag+" "+msg)
		if !ok {
			okAll = false
		}
	}

	// slice：Seq 产出下标，用 Index 取元素。
	s := []int{3, 1, 2}
	sv := reflect.ValueOf(s)
	idx := seqInts(sv)
	note(fmt.Sprint(idx) == "[0 1 2]", fmt.Sprintf("slice indices=%v", idx))
	var vals []int
	for _, i := range idx {
		vals = append(vals, int(sv.Index(int(i)).Int()))
	}
	note(fmt.Sprint(vals) == "[3 1 2]", fmt.Sprintf("slice values=%v", vals))

	// 数组指针：Seq 同样产出下标。
	arr := [4]string{"a", "b", "c", "d"}
	av := reflect.ValueOf(&arr).Elem()
	aidx := seqInts(av)
	note(fmt.Sprint(aidx) == "[0 1 2 3]", fmt.Sprintf("array indices=%v", aidx))
	note(av.Index(3).String() == "d", "array index value")

	// string：Seq 产出字节下标（多字节字符只占一个起点）。
	sidx := seqInts(reflect.ValueOf("hé!"))
	note(fmt.Sprint(sidx) == "[0 1 3]", fmt.Sprintf("string indices=%v", sidx))

	// int：0..n-1；0 与负数无迭代。
	note(fmt.Sprint(seqInts(reflect.ValueOf(int64(4)))) == "[0 1 2 3]", "int seq")
	note(len(seqInts(reflect.ValueOf(int(-2)))) == 0, "negative int seq must be empty")

	// nil 容器无迭代。
	note(len(seqInts(reflect.ValueOf([]int(nil)))) == 0, "nil slice seq")
	note(len(seqPairs(reflect.ValueOf(map[string]int64(nil)))) == 0, "nil map seq2")

	// func：Seq 执行 range-over-func，提前 break 生效。
	f := func(yield func(int) bool) {
		for i := 10; i < 15; i++ {
			if !yield(i) {
				return
			}
		}
	}
	fs := seqFunc(reflect.ValueOf(f))
	note(fmt.Sprint(fs) == "[10 11]", fmt.Sprintf("func seq=%v", fs))

	// map：Seq2 产出全部键值，排序后确定。
	got := seqPairs(reflect.ValueOf(m))
	want := make([]pair, 0, len(m))
	for k, v := range m {
		want = append(want, pair{k, v})
	}
	sort.Slice(want, func(i, j int) bool { return want[i].k < want[j].k })
	note(fmt.Sprint(got) == fmt.Sprint(want), fmt.Sprintf("map seq2=%v want=%v", got, want))
	for _, p := range got {
		note(m[p.k] == p.v, fmt.Sprintf("map pair %s=%d", p.k, p.v))
	}

	// SetIterKey/SetIterValue：单条目 map，避免依赖遍历顺序。
	one := map[string]int64{"only": 42}
	it := reflect.ValueOf(one).MapRange()
	var dk string
	var dv int64
	k0 := reflect.ValueOf(&dk).Elem()
	v0 := reflect.ValueOf(&dv).Elem()
	note(it.Next(), "single map must have one entry")
	k0.SetIterKey(it)
	v0.SetIterValue(it)
	note(dk == "only" && dv == 42, fmt.Sprintf("SetIterKey/Value=%s/%d", dk, dv))
	note(it.Key().String() == dk && it.Value().Int() == dv, "MapIter Key/Value consistency")
	note(!it.Next(), "single map iteration must end")

	// 非法 Kind：Seq/Seq2 与类型不匹配的 SetIterKey 均 panic。
	note(panics(func() {
		for range reflect.ValueOf(struct{ X int }{}).Seq() {
		}
	}), "Seq(struct) must panic")
	note(panics(func() {
		for range reflect.ValueOf(1.5).Seq() {
		}
	}), "Seq(float) must panic")
	note(panics(func() {
		for range (reflect.Value{}).Seq() {
		}
	}), "Seq(invalid) must panic")
	note(panics(func() {
		for range reflect.ValueOf(1).Seq2() {
		}
	}), "Seq2(non-func/map) must panic")
	badIt := reflect.ValueOf(one).MapRange()
	badIt.Next()
	var wrong int
	note(panics(func() { reflect.ValueOf(&wrong).Elem().SetIterKey(badIt) }),
		"SetIterKey type mismatch must panic")

	held = append(held, s, arr, m, fs, got, dk, dv)
	return held, okAll
}

func main() {
	uafSeed = envSeed()
	rng := rand.New(rand.NewSource(uafSeed))

	m := map[string]int64{"b": 2, "a": 1, "c": 3}
	held, ok := runSuite("initial", m)
	ck(ok, "initial suite failed")
	live = append(live, held...)

	// 重复执行与 GC：输出稳定，held 中的值/容器仍可读。
	before := fmt.Sprint(held)
	for round := 0; round < 3; round++ {
		gcStress()
		held2, ok2 := runSuite(fmt.Sprintf("round %d", round), m)
		ck(ok2, fmt.Sprintf("round %d suite failed", round))
		ck(fmt.Sprint(held2) == before, fmt.Sprintf("round %d suite output changed", round))
		// 种子打乱轮次内探针顺序。
		probes := []string{"a", "b", "c"}
		for _, i := range rng.Perm(len(probes)) {
			key := probes[i]
			ck(m[key] > 0, fmt.Sprintf("round %d map probe %s", round, key))
		}
		// 遍历仍有效，键集合确定。
		keys := make([]string, 0, len(m))
		it := reflect.ValueOf(m).MapRange()
		for it.Next() {
			keys = append(keys, it.Key().String())
		}
		sort.Strings(keys)
		ck(fmt.Sprint(keys) == "[a b c]", fmt.Sprintf("round %d map keys=%v", round, keys))
		ck(len(held2) == len(held), fmt.Sprintf("round %d held size changed", round))
		live = append(live, held2...)
	}
	m["d"] = 4
	ck(len(seqPairs(reflect.ValueOf(m))) == 4, "map mutation after GC")

	finish("l16_reflect_iter_value")
}
