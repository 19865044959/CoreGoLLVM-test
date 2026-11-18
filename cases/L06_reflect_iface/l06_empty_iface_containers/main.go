// L06: []interface{} / map[string]interface{} 嵌套指针 + type switch + GC。
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

type node struct {
	V     int64
	Label string
}

type stats struct {
	nodes      int
	containers int
	bad        int
	sum        int64
	labelLen   int64
	steps      int
}

var (
	failed   int32
	gcSink   [][]byte
	rootSink any
	uafSeed  int64 = 1
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

func envInt(name string, def int) int {
	s := os.Getenv(name)
	if s == "" {
		return def
	}
	n, err := strconv.Atoi(s)
	if err != nil || n <= 0 {
		return def
	}
	if n > 50000 {
		n = 50000
	}
	return n
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

// build 递归构造 []interface{} / map[string]interface{} 混合树，返回根与节点值之和。
func build(depth int, r *rand.Rand, base int64) (any, int64) {
	if depth <= 0 {
		v := base + r.Int63n(7)
		return &node{V: v, Label: "leaf"}, v
	}
	switch r.Intn(4) {
	case 0:
		arr := make([]any, 0, 3)
		var sum int64
		for i := 0; i < 3; i++ {
			c, s := build(depth-1, r, base+int64(i)*100)
			arr = append(arr, c)
			sum += s
		}
		return arr, sum
	case 1:
		m := map[string]any{}
		var sum int64
		for i := 0; i < 3; i++ {
			c, s := build(depth-1, r, base+int64(i)*1000)
			m[fmt.Sprintf("k%d", i)] = c
			sum += s
		}
		m["ptr"] = &node{V: 1, Label: "m"}
		sum++
		return m, sum
	case 2:
		p := &node{V: base % 17, Label: "n"}
		arr := []any{p, []any{&node{V: 2, Label: "deep"}, map[string]any{"z": &node{V: 3, Label: "z"}}}}
		return arr, p.V + 2 + 3
	default:
		p := &node{V: base % 11, Label: "solo"}
		return []any{p}, p.V
	}
}

func walk(v any, st *stats) {
	st.steps++
	if st.steps%128 == 0 {
		gcStress()
	}
	switch x := v.(type) {
	case nil:
		return
	case *node:
		st.nodes++
		st.sum += x.V
		st.labelLen += int64(len(x.Label))
	case []any:
		st.containers++
		for _, e := range x {
			walk(e, st)
		}
	case map[string]any:
		st.containers++
		keys := make([]string, 0, len(x))
		for k := range x {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			walk(x[k], st)
		}
	default:
		st.bad++
	}
}

func main() {
	uafSeed = envSeed()
	rounds := envInt("TORTURE_ROUNDS", 1000)
	const depth = 6

	for r := 0; r < rounds; r++ {
		rnd := rand.New(rand.NewSource(uafSeed + int64(r)*7919))
		root, wantSum := build(depth, rnd, int64(r*13))
		rootSink = root
		gcStress()

		var st stats
		walk(root, &st)
		ck(st.bad == 0, "unexpected dynamic type in nested containers")
		ck(st.sum == wantSum, "nested interface tree sum")
		ck(st.nodes > 0, "nested interface tree has nodes")
		ck(st.containers > 0, "nested interface tree has containers")
		ck(st.labelLen > 0, "nested interface tree labels")

		gcStress()
		var st2 stats
		walk(rootSink, &st2)
		ck(st2.sum == st.sum && st2.nodes == st.nodes && st2.containers == st.containers,
			"nested interface tree survives repeated GC")

		// 丢弃局部根后再从全局槽取值，验证接口容器整体可扫描。
		root = nil
		gcStress()
		var st3 stats
		walk(rootSink, &st3)
		ck(st3.sum == wantSum && st3.bad == 0, "nested interface tree after root drop")
	}
	finish("l06_empty_iface_containers")
}
