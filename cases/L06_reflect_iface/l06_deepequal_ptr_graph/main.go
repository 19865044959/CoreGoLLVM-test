// L06: reflect.DeepEqual 指针图：环、共享节点、map/slice、func/NaN 语义，跨 GC 稳定。
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

type gnode struct {
	V     int64
	Label string
	Next  *gnode
	Kids  []*gnode
	Attr  map[string]*gnode
	Ref   *gnode
	Fn    func()
	F     float64
}

var (
	failed  int32
	gcSink  [][]byte
	anySink any
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

func envInt(name string, def, max int) int {
	s := os.Getenv(name)
	if s == "" {
		return def
	}
	n, err := strconv.Atoi(s)
	if err != nil || n <= 0 {
		return def
	}
	if n > max {
		n = max
	}
	return n
}

// buildGraph 构造带环/共享引用/map/slice 的节点图，两次调用产生结构相等但地址不同的图。
func buildGraph(width int, base int64) (*gnode, []*gnode) {
	nodes := make([]*gnode, width)
	for i := 0; i < width; i++ {
		nodes[i] = &gnode{V: base + int64(i), Label: fmt.Sprintf("n%02d", i)}
	}
	for i := 0; i < width; i++ {
		if i+1 < width {
			nodes[i].Next = nodes[i+1]
		} else {
			nodes[i].Next = nodes[0]
		}
		if i%3 == 0 {
			nodes[i].Kids = []*gnode{nodes[(i+1)%width]}
			if i%6 == 0 {
				nodes[i].Kids = append(nodes[i].Kids, nodes[(i+2)%width])
			}
		}
		if i%4 == 0 {
			nodes[i].Attr = map[string]*gnode{
				"self":                nodes[i],
				fmt.Sprintf("k%d", i): nodes[(i*5+1)%width],
			}
		}
		if i > 0 && i%5 == 0 {
			nodes[i].Ref = nodes[i/2]
		}
	}
	return nodes[0], nodes
}

func main() {
	uafSeed = envSeed()
	rounds := envInt("TORTURE_ROUNDS", 20, 2000)
	width := envInt("TORTURE_WIDTH", 40, 2000)
	if width < 6 {
		width = 6
	}

	// 固定语义矩阵：自环 vs 双节点环、共享 vs 独立但等值、nil vs 空 slice、func/NaN。
	self := &gnode{V: 1, Label: "x"}
	self.Next = self
	other := &gnode{V: 1, Label: "x"}
	other.Next = other
	ck(reflect.DeepEqual(self, other), "DeepEqual self-cycle vs self-cycle")
	twoA := &gnode{V: 1, Label: "x"}
	twoB := &gnode{V: 1, Label: "x"}
	twoA.Next = twoB
	twoB.Next = twoA
	ck(reflect.DeepEqual(self, twoA), "DeepEqual self-cycle vs two-node cycle")
	shared := &gnode{V: 5, Label: "s"}
	s1 := &gnode{V: 1, Kids: []*gnode{shared, shared}}
	t1 := &gnode{V: 1, Kids: []*gnode{{V: 5, Label: "s"}, {V: 5, Label: "s"}}}
	ck(reflect.DeepEqual(s1, t1), "DeepEqual shared vs distinct equal nodes")
	ck(!reflect.DeepEqual(&gnode{Kids: nil}, &gnode{Kids: []*gnode{}}), "DeepEqual nil vs empty slice")
	ck(reflect.DeepEqual(&gnode{Next: &gnode{V: 2}}, &gnode{Next: &gnode{V: 2}}), "DeepEqual pointer identity ignored")
	fn := func() {}
	ck(!reflect.DeepEqual(&gnode{Fn: fn}, &gnode{Fn: fn}), "DeepEqual non-nil funcs are never equal")
	ck(reflect.DeepEqual(&gnode{}, &gnode{}), "DeepEqual nil funcs are equal")
	ck(!reflect.DeepEqual(&gnode{F: math.NaN()}, &gnode{F: math.NaN()}), "DeepEqual NaN is not equal")
	ck(reflect.DeepEqual(&gnode{F: 1}, &gnode{F: 1}), "DeepEqual equal floats")
	ck(reflect.DeepEqual(math.NaN(), math.NaN()) == false, "DeepEqual float NaN direct")

	for r := 0; r < rounds; r++ {
		rnd := rand.New(rand.NewSource(uafSeed + int64(r)*307))
		base := int64(r * 1000)
		g1, n1 := buildGraph(width, base)
		g2, n2 := buildGraph(width, base)
		anySink = []any{g1, g2}
		gcStress()

		ck(reflect.DeepEqual(g1, g2), "structurally equal cyclic graphs")
		ck(reflect.DeepEqual(g1, g1), "DeepEqual graph with itself")
		ck(!reflect.DeepEqual(g1, n1[1]), "root vs inner node differ")
		ck(len(n1) == width && len(n2) == width, "graph width")

		pos := rnd.Intn(width)
		oldV := n1[pos].V
		n1[pos].V = oldV + 1
		ck(!reflect.DeepEqual(g1, g2), "value mutation breaks equality")
		n1[pos].V = oldV
		ck(reflect.DeepEqual(g1, g2), "value restore re-establishes equality")

		oldLabel := n1[pos].Label
		n1[pos].Label = oldLabel + "x"
		ck(!reflect.DeepEqual(g1, g2), "label mutation breaks equality")
		n1[pos].Label = oldLabel

		ck(reflect.DeepEqual(g1.Kids, g2.Kids), "kids slices deep equal")

		// 删除 map 键，再恢复同一指针。
		var mapOwner int
		for i := 0; i < width; i++ {
			if n1[i].Attr != nil {
				mapOwner = i
				break
			}
		}
		var key string
		var keep *gnode
		for k, v := range n1[mapOwner].Attr {
			key, keep = k, v
			break
		}
		delete(n1[mapOwner].Attr, key)
		ck(!reflect.DeepEqual(g1, g2), "map key deletion breaks equality")
		n1[mapOwner].Attr[key] = keep
		ck(reflect.DeepEqual(g1, g2), "map key restoration re-establishes equality")

		// 追加 Kid 再回退。
		kidsLen := len(n1[mapOwner].Kids)
		n1[mapOwner].Kids = append(n1[mapOwner].Kids, n1[(mapOwner+3)%width])
		ck(!reflect.DeepEqual(g1, g2), "kids append breaks equality")
		n1[mapOwner].Kids = n1[mapOwner].Kids[:kidsLen]

		// NaN 双端都设置 -> 不相等；恢复数值后又相等。
		n1[0].F = math.NaN()
		n2[0].F = math.NaN()
		ck(!reflect.DeepEqual(g1, g2), "NaN in both graphs is unequal")
		n1[0].F, n2[0].F = 0, 0
		ck(reflect.DeepEqual(g1, g2), "reset NaN restores equality")

		// func 字段：双方 nil 相等；非 nil（即使同一函数）不相等。
		ck(reflect.DeepEqual(g1, g2), "nil funcs keep graphs equal")
		n1[1].Fn = func() {}
		n2[1].Fn = n1[1].Fn
		ck(!reflect.DeepEqual(g1, g2), "non-nil func breaks equality")
		n1[1].Fn, n2[1].Fn = nil, nil
		ck(reflect.DeepEqual(g1, g2), "nil func restore")

		// 自环 vs 双节点环在完整图上仍相等。
		loop1 := &gnode{V: 9, Label: "loop"}
		loop1.Next = loop1
		loop2a := &gnode{V: 9, Label: "loop"}
		loop2b := &gnode{V: 9, Label: "loop"}
		loop2a.Next = loop2b
		loop2b.Next = loop2a
		ck(reflect.DeepEqual(loop1, loop2a), "cycle shape independent equality")

		// GC 后再次比较，并核对共享指针仍指向同一对象。
		gcStress()
		ck(reflect.DeepEqual(g1, g2), "graphs still deep equal after GC")
		ck(n1[0].Next == n1[1] && n1[width-1].Next == n1[0], "Next chain survives GC")
		var sum1, sum2 int64
		for i := 0; i < width; i++ {
			sum1 += n1[i].V
			sum2 += n2[i].V
		}
		ck(sum1 == sum2 && sum1 == base*int64(width)+int64(width-1)*int64(width)/2, "graph sums after GC")
	}
	finish("l06_deepequal_ptr_graph")
}
