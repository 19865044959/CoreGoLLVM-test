// L11: 多实现接口去虚拟化——12 种具体实现经接口分派、方法值、type switch、
// 类型断言与泛型静态调用，结果必须与直接调用/参考公式一致，并跨 GC 复核。
package main

import (
	"fmt"
	"os"
	"runtime"
	"strconv"
	"sync/atomic"
)

type codec interface {
	Encode(x int64) int64
	Kind() string
}

type codecBase struct {
	kind string
	mul  int64
	add  int64
}

func (b codecBase) Encode(x int64) int64 { return x*b.mul + b.add }
func (b codecBase) Kind() string         { return b.kind }

type implA struct{ codecBase }
type implB struct{ codecBase }
type implC struct{ codecBase }
type implD struct{ codecBase }
type implE struct{ codecBase }
type implF struct{ codecBase }
type implG struct{ codecBase }
type implH struct{ codecBase }
type implI struct{ codecBase }
type implJ struct{ codecBase }

type implK struct {
	mul, add int64
}

func (c *implK) Encode(x int64) int64 { return x*c.mul - c.add }
func (c *implK) Kind() string         { return "K" }

type implL struct {
	mul, add int64
}

func (c implL) Encode(x int64) int64 { return x*c.mul ^ c.add }
func (c implL) Kind() string         { return "L" }

type codecSpec struct {
	kind string
	mul  int64
	add  int64
	ptr  bool
	xor  bool
}

var codecSpecs = []codecSpec{
	{"A", 1, 0, false, false},
	{"B", 2, 1, false, false},
	{"C", 3, 2, false, false},
	{"D", 4, 3, false, false},
	{"E", 5, 4, false, false},
	{"F", 6, 5, false, false},
	{"G", 7, 6, false, false},
	{"H", 8, 7, false, false},
	{"I", 9, 8, false, false},
	{"J", 10, 9, false, false},
	{"K", 3, 4, true, false},
	{"L", 5, 6, false, true},
}

func expectedEncode(s codecSpec, x int64) int64 {
	if s.ptr {
		return x*s.mul - s.add
	}
	if s.xor {
		return x*s.mul ^ s.add
	}
	return x*s.mul + s.add
}

func buildCodecs() []codec {
	return []codec{
		implA{codecBase{"A", 1, 0}},
		implB{codecBase{"B", 2, 1}},
		implC{codecBase{"C", 3, 2}},
		implD{codecBase{"D", 4, 3}},
		implE{codecBase{"E", 5, 4}},
		implF{codecBase{"F", 6, 5}},
		implG{codecBase{"G", 7, 6}},
		implH{codecBase{"H", 8, 7}},
		implI{codecBase{"I", 9, 8}},
		implJ{codecBase{"J", 10, 9}},
		&implK{3, 4},
		implL{5, 6},
	}
}

func kindSwitch(c codec) string {
	switch c.(type) {
	case implA:
		return "A"
	case implB:
		return "B"
	case implC:
		return "C"
	case implD:
		return "D"
	case implE:
		return "E"
	case implF:
		return "F"
	case implG:
		return "G"
	case implH:
		return "H"
	case implI:
		return "I"
	case implJ:
		return "J"
	case *implK:
		return "K"
	case implL:
		return "L"
	default:
		return "?"
	}
}

func encodeStatic[T codec](v T, x int64) int64 { return v.Encode(x) }

var (
	failed int32
	gcSink [][]byte
	seed   uint64 = 1
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
	x := int64(seed%37) - 18

	cs := buildCodecs()
	ck(len(cs) == len(codecSpecs), "implementation count")
	for i, c := range cs {
		ck(c.Kind() == codecSpecs[i].kind, "interface Kind dispatch")
		ck(c.Encode(x) == expectedEncode(codecSpecs[i], x), "interface Encode dispatch")
		ck(kindSwitch(c) == codecSpecs[i].kind, "type switch kind")
	}

	// 直接具体调用 vs 接口调用（去虚拟化等价）。
	ck(implA{codecBase{"A", 1, 0}}.Encode(x) == cs[0].Encode(x), "direct vs iface A")
	ck(implB{codecBase{"B", 2, 1}}.Encode(x) == cs[1].Encode(x), "direct vs iface B")
	ck(implC{codecBase{"C", 3, 2}}.Encode(x) == cs[2].Encode(x), "direct vs iface C")
	ck(implD{codecBase{"D", 4, 3}}.Encode(x) == cs[3].Encode(x), "direct vs iface D")
	ck(implE{codecBase{"E", 5, 4}}.Encode(x) == cs[4].Encode(x), "direct vs iface E")
	ck(implF{codecBase{"F", 6, 5}}.Encode(x) == cs[5].Encode(x), "direct vs iface F")
	ck(implG{codecBase{"G", 7, 6}}.Encode(x) == cs[6].Encode(x), "direct vs iface G")
	ck(implH{codecBase{"H", 8, 7}}.Encode(x) == cs[7].Encode(x), "direct vs iface H")
	ck(implI{codecBase{"I", 9, 8}}.Encode(x) == cs[8].Encode(x), "direct vs iface I")
	ck(implJ{codecBase{"J", 10, 9}}.Encode(x) == cs[9].Encode(x), "direct vs iface J")
	ck((&implK{3, 4}).Encode(x) == cs[10].Encode(x), "direct vs iface K")
	ck(implL{5, 6}.Encode(x) == cs[11].Encode(x), "direct vs iface L")

	// 泛型静态调用（编译期已知具体类型）。
	ck(encodeStatic(implB{codecBase{"B", 2, 1}}, x) == expectedEncode(codecSpecs[1], x),
		"generic static call B")
	ck(encodeStatic(&implK{3, 4}, x) == expectedEncode(codecSpecs[10], x),
		"generic static call K")
	ck(encodeStatic(implL{5, 6}, x) == expectedEncode(codecSpecs[11], x),
		"generic static call L")

	// 混合接口切片：120 次分派。
	const total = 120
	var wantSum, gotSum int64
	for i := 0; i < total; i++ {
		wantSum += expectedEncode(codecSpecs[i%len(codecSpecs)], int64(i))
		gotSum += cs[i%len(cs)].Encode(int64(i))
	}
	ck(gotSum == wantSum, "mixed interface dispatch sum")

	// 方法值。
	mvs := make([]func(int64) int64, len(cs))
	for i := range cs {
		mvs[i] = cs[i].Encode
	}
	kinds := make([]func() string, len(cs))
	for i := range cs {
		kinds[i] = cs[i].Kind
	}

	gcStress()

	var methodSum int64
	for i := 0; i < total; i++ {
		mv := mvs[i%len(mvs)]
		methodSum += mv(int64(i))
		ck(kinds[i%len(kinds)]() == codecSpecs[i%len(codecSpecs)].kind,
			"method value kind after GC")
	}
	ck(methodSum == wantSum, "method value sum after GC")

	// 类型断言：成功与失败路径。
	if _, ok := cs[0].(implA); !ok {
		ck(false, "type assertion implA")
	}
	if _, ok := cs[0].(*implK); ok {
		ck(false, "wrong type assertion must fail")
	}
	if k, ok := cs[10].(*implK); !ok || k.mul != 3 {
		ck(false, "pointer implementation assertion")
	}

	// 接口值相等性。
	ia1 := codec(implA{codecBase{"A", 1, 0}})
	ia2 := codec(implA{codecBase{"A", 1, 0}})
	ib := codec(implB{codecBase{"B", 2, 1}})
	ck(ia1 == ia2, "equal interface values")
	ck(ia1 != ib, "different dynamic interface values")

	// GC 之后再次逐项校验。
	gcStress()
	for i, c := range cs {
		ck(c.Encode(x) == expectedEncode(codecSpecs[i], x), "dispatch after GC")
		ck(kindSwitch(c) == codecSpecs[i].kind, "type switch after GC")
	}

	finish("l11_itab_multi_impl")
}
