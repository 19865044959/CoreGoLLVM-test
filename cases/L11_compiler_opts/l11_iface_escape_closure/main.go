// L11: 接口逃逸 × 闭包捕获——闭包捕获接口值与堆对象后存入 any/slice/map，
// 显式 GC 之后调用必须看到创建时绑定的动态类型与后续修改；typed nil 与
// 函数计数器跨 GC 保持语义。
package main

import (
	"fmt"
	"os"
	"runtime"
	"strconv"
	"sync/atomic"
)

type icNode struct {
	id   int64
	tag  string
	data []int64
}

type icSink interface {
	Value() int64
	Label() string
}

type icAdder struct{ base int64 }

func (a *icAdder) Value() int64  { return a.base*2 + 1 }
func (a *icAdder) Label() string { return "addr" }

type icMul struct{ base int64 }

func (m icMul) Value() int64  { return m.base*3 - 1 }
func (m icMul) Label() string { return "mul" }

type icHolder struct {
	fn   any
	tag  string
	want int64
}

type icObs struct {
	fn   func() int64
	want int64
}

var (
	failed     int32
	gcSink     [][]byte
	seed       uint64 = 1
	icClosures []func() (int64, string)
	icBoxed    []any
	icWant     []int64
	icWantLab  []string
	icNodes    []*icNode
	icNodeIDs  []int64
	icNodeTags []string
	icCounters []*int64
	icCountFns []func()
	icCountWnt []int64
	icNilFns   []func() bool
	icMutFns   []icObs
	icMutNodes []*icNode
	icMapFns   []map[int]func() int64
	icMapWant  []map[int]int64
	icHolders  []*icHolder
)

func ck(ok bool, msg string) {
	if !ok {
		atomic.AddInt32(&failed, 1)
		fmt.Printf("FAIL: %s (UAF_SEED=%d)\n", msg, seed)
	}
}

func gcStress() {
	for i := 0; i < 16; i++ {
		b := make([]byte, 4096)
		b[0] = byte(i)
		b[len(b)-1] = byte(255 - i)
		gcSink = append(gcSink[:0], b)
	}
	gcSink = gcSink[:0]
	runtime.GC()
	runtime.GC()
}

func loadSeed() uint64 {
	s := os.Getenv("UAF_SEED")
	if s == "" {
		return 1
	}
	n, err := strconv.ParseUint(s, 10, 64)
	if err != nil {
		return 1
	}
	return n
}

func tortureMultiplier() int {
	s := os.Getenv("TORTURE_ROUNDS")
	if s == "" {
		return 1
	}
	n, err := strconv.Atoi(s)
	if err != nil || n < 1 {
		return 1
	}
	if n > 50 {
		n = 50
	}
	return n
}

func finish(name string) {
	if n := atomic.LoadInt32(&failed); n != 0 {
		fmt.Printf("FAILED %s (%d failed checks, seed=%d)\n", name, n, seed)
		os.Exit(1)
	}
	fmt.Println("ALL PASS " + name)
}

func main() {
	seed = loadSeed()
	if seed == 0 {
		seed = 1
	}
	rounds := 3 * tortureMultiplier()

	for r := 0; r < rounds; r++ {
		n := 10 + int((seed+uint64(r)*5)%15)
		mfns := make(map[int]func() int64, n)
		mwant := make(map[int]int64, n)

		for i := 0; i < n; i++ {
			tag := "n" + strconv.Itoa(i) + "#" + strconv.Itoa(r)
			node := &icNode{
				id:   int64(r*1000 + i),
				tag:  tag,
				data: []int64{int64(i), int64(i + 1)},
			}
			var impl icSink
			if i%2 == 0 {
				impl = &icAdder{base: int64(i)}
			} else {
				impl = icMul{base: int64(i)}
			}

			nc, sc := node, impl
			fn := func() (int64, string) {
				return nc.id + sc.Value(), nc.tag + "/" + sc.Label()
			}
			icClosures = append(icClosures, fn)
			icBoxed = append(icBoxed, fn)
			icNodes = append(icNodes, node)
			icNodeIDs = append(icNodeIDs, node.id)
			icNodeTags = append(icNodeTags, tag)
			icWant = append(icWant, node.id+impl.Value())
			icWantLab = append(icWantLab, node.tag+"/"+impl.Label())
			icHolders = append(icHolders, &icHolder{fn: fn, tag: tag, want: node.id + impl.Value()})

			counter := new(int64)
			*counter = int64(i)
			icCounters = append(icCounters, counter)
			icCountWnt = append(icCountWnt, int64(i+5))
			icCountFns = append(icCountFns, func() { *counter += 5 })

			var np *icNode
			var ni any = np
			icNilFns = append(icNilFns, func() bool {
				q, ok := ni.(*icNode)
				return ni != nil && ok && q == nil
			})

			mut := &icNode{id: int64(r*100 + i), tag: "mut"}
			icMutNodes = append(icMutNodes, mut)
			icMutFns = append(icMutFns, icObs{fn: func() int64 { return mut.id }, want: mut.id})

			nodeID := node.id
			tagLen := int64(len(node.tag))
			mfns[i] = func() int64 { return nodeID*2 + tagLen }
			mwant[i] = node.id*2 + tagLen
		}

		icMapFns = append(icMapFns, mfns)
		icMapWant = append(icMapWant, mwant)

		gcStress()

		for i := range mfns {
			if mfns[i]() != mwant[i] {
				ck(false, "map closure value after GC")
				break
			}
		}
		for i := range icNilFns {
			if !icNilFns[i]() {
				ck(false, "typed nil captured by closure after GC")
				break
			}
		}
	}

	gcStress()

	ck(len(icClosures) == len(icWant) && len(icBoxed) == len(icClosures), "closure slice lengths")
	for i, fn := range icClosures {
		v, lab := fn()
		ck(v == icWant[i] && lab == icWantLab[i], "closure captured interface after GC")
	}

	for i, b := range icBoxed {
		fn, ok := b.(func() (int64, string))
		ck(ok, "boxed closure dynamic type")
		if ok {
			v, lab := fn()
			ck(v == icWant[i] && lab == icWantLab[i], "boxed closure after GC")
		}
	}

	for i, fn := range icCountFns {
		fn()
		if *icCounters[i] != icCountWnt[i] {
			ck(false, "counter closure increment after GC")
			break
		}
	}

	for i := range icMutNodes {
		icMutNodes[i].id += 100
		icMutFns[i].want += 100
	}
	gcStress()
	for i := range icMutFns {
		if icMutFns[i].fn() != icMutFns[i].want {
			ck(false, "closure sees mutation after GC")
			break
		}
	}

	for i := range icMapFns {
		bad := false
		for k, want := range icMapWant[i] {
			if icMapFns[i][k]() != want {
				bad = true
				break
			}
		}
		if bad {
			ck(false, "retained map closures after GC")
			break
		}
	}

	for _, h := range icHolders {
		fn, ok := h.fn.(func() (int64, string))
		ck(ok && h.tag != "", "holder closure dynamic type")
		if ok {
			v, lab := fn()
			ck(v == h.want && lab != "", "holder closure after GC")
		}
	}

	nodeOK := true
	for i, n := range icNodes {
		if n == nil || n.id != icNodeIDs[i] || n.tag != icNodeTags[i] || len(n.data) != 2 {
			nodeOK = false
			break
		}
	}
	ck(nodeOK, "interface-escaped nodes after GC")

	finish("l11_iface_escape_closure")
}
