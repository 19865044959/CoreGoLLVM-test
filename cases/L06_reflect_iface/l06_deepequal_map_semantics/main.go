// L06: reflect.DeepEqual 的 map 语义：插入顺序、nil/空、NaN、func、接口键、环与指针值，跨 GC。
package main

import (
	"fmt"
	"math"
	"math/rand"
	"os"
	"reflect"
	"runtime"
	"strconv"
	"sync/atomic"
)

type leaf struct {
	V   int64
	Tag *string
}

type key struct {
	A int64
	B string
}

var (
	failed  int32
	gcSink  [][]byte
	mapSink []any
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

func envInt(name string, def int) int {
	s := os.Getenv(name)
	if s == "" {
		return def
	}
	n, err := strconv.Atoi(s)
	if err != nil || n <= 0 {
		return def
	}
	return n
}

func perm(n int, rnd *rand.Rand) []int {
	ord := make([]int, n)
	for i := range ord {
		ord[i] = i
	}
	rnd.Shuffle(n, func(i, j int) { ord[i], ord[j] = ord[j], ord[i] })
	return ord
}

func buildMap(base int64, ord []int, width int) map[string]any {
	m := make(map[string]any, width)
	for _, i := range ord {
		k := fmt.Sprintf("k%03d", i)
		switch i % 5 {
		case 0:
			t := fmt.Sprintf("t%03d-%d", i, base)
			m[k] = &leaf{V: base + int64(i), Tag: &t}
		case 1:
			m[k] = []int64{base, int64(i), base + int64(i)}
		case 2:
			m[k] = map[string]int64{"a": base, "b": int64(i)}
		case 3:
			m[k] = nil
		case 4:
			m[k] = fmt.Sprintf("s%03d", i)
		}
	}
	return m
}

func main() {
	uafSeed = envSeed()
	rounds := envInt("TORTURE_ROUNDS", 12)
	width := envInt("TORTURE_WIDTH", 40)
	if width < 10 {
		width = 10
	}

	// 固定语义：nil、空、NaN、func、接口键、typed nil、环与结构体键。
	var nilMap, nilMap2 map[string]any
	emptyMap := map[string]any{}
	ck(!reflect.DeepEqual(nilMap, emptyMap), "nil map differs from empty map")
	ck(reflect.DeepEqual(nilMap, nilMap2), "two nil maps equal")
	ck(reflect.DeepEqual(emptyMap, map[string]any{}), "two empty maps equal")

	nan := math.NaN()
	na1 := map[string]float64{"x": nan}
	na2 := map[string]float64{"x": nan}
	ck(!reflect.DeepEqual(na1, na2), "NaN map values never equal across maps")
	ck(reflect.DeepEqual(na1, na1), "same NaN map object equal to itself")
	nk1 := map[float64]int64{nan: 1}
	nk2 := map[float64]int64{nan: 1}
	ck(!reflect.DeepEqual(nk1, nk2), "NaN keys never match across maps")
	ck(reflect.DeepEqual(nk1, nk1), "same NaN-key map object equal to itself")

	f := func() {}
	fm1 := map[string]any{"f": f}
	fm2 := map[string]any{"f": f}
	ck(!reflect.DeepEqual(fm1, fm2), "non-nil func values never equal")
	fm1["f"] = nil
	fm2["f"] = nil
	ck(reflect.DeepEqual(fm1, fm2), "both nil func values equal")

	ik1 := map[any]int64{"a": 1, int64(2): 2}
	ik2 := map[any]int64{int64(2): 2, "a": 1}
	ik3 := map[any]int64{"a": 1, int(2): 2}
	ck(reflect.DeepEqual(ik1, ik2), "interface-key map order independent")
	ck(!reflect.DeepEqual(ik1, ik3), "int and int64 keys are distinct")

	tn := map[string]any{"p": (*leaf)(nil)}
	un := map[string]any{"p": nil}
	ck(!reflect.DeepEqual(tn, un), "typed nil map value differs from nil")

	cm1 := map[string]any{}
	cm1["self"] = cm1
	cm2 := map[string]any{}
	cm2["self"] = cm2
	ck(reflect.DeepEqual(cm1, cm2), "cyclic maps deeply equal")
	cm2["self"] = map[string]any{}
	ck(!reflect.DeepEqual(cm1, cm2), "broken map cycle differs")

	sk1 := map[key]*leaf{{A: 1, B: "x"}: {V: 1, Tag: nil}}
	sk2 := map[key]*leaf{{A: 1, B: "x"}: {V: 1, Tag: nil}}
	ck(reflect.DeepEqual(sk1, sk2), "struct-key maps with equal pointer values")
	sk2[key{A: 1, B: "x"}].V = 2
	ck(!reflect.DeepEqual(sk1, sk2), "struct-key map value difference")
	sk2[key{A: 1, B: "x"}].V = 1
	ck(reflect.DeepEqual(sk1, sk2), "struct-key map value restored")

	// 每轮：不同插入顺序构造两份等值 map，做增删改、指针/嵌套比较与 GC 回读。
	for r := 0; r < rounds; r++ {
		base := uafSeed + int64(r*100)
		rnd := rand.New(rand.NewSource(uafSeed + int64(r)*17))
		ord1 := perm(width, rnd)
		ord2 := append([]int(nil), ord1...)
		for i, j := 0, len(ord2)-1; i < j; i, j = i+1, j-1 {
			ord2[i], ord2[j] = ord2[j], ord2[i]
		}
		m1 := buildMap(base, ord1, width)
		m2 := buildMap(base, ord2, width)
		ck(len(m1) == width && len(m2) == width, "map width")
		ck(reflect.DeepEqual(m1, m2), "equal maps in different insertion order")
		ck(reflect.DeepEqual(m1, m1), "map self equal")
		ck(!reflect.DeepEqual(m1, map[string]any{}), "map differs from empty map")

		victim := fmt.Sprintf("k%03d", (r*7)%width)
		old := m2[victim]
		m2[victim] = "changed-by-test"
		ck(!reflect.DeepEqual(m1, m2), "mutated map value detected")
		m2[victim] = old
		ck(reflect.DeepEqual(m1, m2), "map value restored")
		delete(m2, victim)
		ck(!reflect.DeepEqual(m1, m2), "deleted map key detected")
		m2[victim] = old
		ck(reflect.DeepEqual(m1, m2), "map key restored")

		// 独立指针值按指向内容比较；改变指向内容立即不等。
		p := &leaf{V: base + 1, Tag: nil}
		q := &leaf{V: base + 1, Tag: nil}
		pm1 := map[string]*leaf{"p": p}
		pm2 := map[string]*leaf{"p": q}
		ck(reflect.DeepEqual(pm1, pm2), "pointer identity ignored in map values")
		q.V++
		ck(!reflect.DeepEqual(pm1, pm2), "pointed value difference detected")
		q.V--
		ck(reflect.DeepEqual(pm1, pm2), "pointed value restored")

		// 只由全局 map 槽保活，GC 后重复比较并解引用指针元素。
		mapSink = append(mapSink[:0], m1, m2)
		m1 = nil
		m2 = nil
		gcStress()
		r1 := mapSink[0].(map[string]any)
		r2 := mapSink[1].(map[string]any)
		ck(reflect.DeepEqual(r1, r2), "maps survive GC and stay equal")
		lf, ok := r1["k000"].(*leaf)
		ck(ok && lf != nil && lf.V == base, "pointer map value after GC")
		ck(lf.Tag != nil && *lf.Tag == fmt.Sprintf("t000-%d", base), "pointer map value tag after GC")
		nm, ok := r1["k002"].(map[string]int64)
		ck(ok && nm["a"] == base && nm["b"] == 2, "nested map value after GC")
		sv, ok := r1["k001"].([]int64)
		ck(ok && len(sv) == 3 && sv[2] == base+1, "slice map value after GC")
	}

	finish("l06_deepequal_map_semantics")
}
