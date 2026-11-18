// L06: 多种动态类型装箱 interface{}，type switch 分类，跨 GC 后结果不变。
package main

import (
	"fmt"
	"math/rand"
	"os"
	"runtime"
	"strconv"
	"sync/atomic"
)

type node struct {
	V     int64
	Label string
}

type Celsius float64
type ID int64
type named string
type flag bool

type point struct {
	X, Y int64
	Tag  *node
}

type pair struct{ A, B int64 }

type descValue struct{ S string }

type descy interface{ Desc() string }

func (p *point) Desc() string    { return p.Tag.Label }
func (n named) Desc() string     { return string(n) }
func (c Celsius) Desc() string   { return "temp" }
func (d descValue) Desc() string { return d.S }

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

func boxValues() []any {
	return []any{
		int(1), int8(2), int16(3), int32(4), int64(5),
		uint(6), uint8(7), uint16(8), uint32(9), uint64(10),
		"str", true, 1.5, float32(2.5),
		Celsius(36.5), ID(11), named("n"), flag(true),
		pair{3, 4}, &point{X: 5, Y: 6, Tag: &node{V: 7, Label: "p"}},
		[]byte{1, 2, 3}, map[string]int{"a": 1},
		(*node)(nil), nil, descValue{S: "abc"},
	}
}

func expected() ([]int64, []int64) {
	kinds := []int64{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16, 17, 18, 19, 20, 21, 22, 23, 0, 25}
	sums := []int64{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 3, 1, 15, 25, 365, 11, 1, 1, 34, 567, 3, 1, 0, 0, 3}
	return kinds, sums
}

func classify(v any) (int64, int64) {
	switch x := v.(type) {
	case nil:
		return 0, 0
	case int:
		return 1, int64(x)
	case int8:
		return 2, int64(x)
	case int16:
		return 3, int64(x)
	case int32:
		return 4, int64(x)
	case int64:
		return 5, x
	case uint:
		return 6, int64(x)
	case uint8:
		return 7, int64(x)
	case uint16:
		return 8, int64(x)
	case uint32:
		return 9, int64(x)
	case uint64:
		return 10, int64(x)
	case string:
		return 11, int64(len(x))
	case bool:
		if x {
			return 12, 1
		}
		return 12, 0
	case float64:
		return 13, int64(x * 10)
	case float32:
		return 14, int64(x * 10)
	case Celsius:
		return 15, int64(x * 10)
	case ID:
		return 16, int64(x)
	case named:
		return 17, int64(len(x))
	case flag:
		if bool(x) {
			return 18, 1
		}
		return 18, 0
	case pair:
		return 19, x.A*10 + x.B
	case *point:
		if x == nil {
			return 20, 0
		}
		return 20, x.X*100 + x.Y*10 + x.Tag.V
	case []byte:
		return 21, int64(len(x))
	case map[string]int:
		return 22, int64(x["a"])
	case *node:
		if x == nil {
			return 23, 0
		}
		return 24, x.V
	case descy:
		return 25, int64(len(x.Desc()))
	default:
		return 99, 0
	}
}

func main() {
	uafSeed = envSeed()
	kinds, sums := expected()
	var baseK, baseS int64
	for i := range kinds {
		baseK += kinds[i]
		baseS += sums[i]
	}

	const rounds = 120
	var gotK, gotS int64
	for r := 0; r < rounds; r++ {
		vals := boxValues()
		gcStress()

		for i, v := range vals {
			k, s := classify(v)
			if k != kinds[i] || s != sums[i] {
				ck(false, "type switch classification mismatch")
			}
			gotK += k
			gotS += s
			if i%8 == 0 {
				gcStress()
			}
		}

		// 保持一个装箱指针，跨 shuffle + GC 后继续解引用。
		pt := vals[19].(*point)

		rnd := rand.New(rand.NewSource(uafSeed + int64(r)))
		rnd.Shuffle(len(vals), func(i, j int) { vals[i], vals[j] = vals[j], vals[i] })
		gcStress()

		var shufK, shufS int64
		for _, v := range vals {
			k, s := classify(v)
			shufK += k
			shufS += s
		}
		ck(shufK == baseK && shufS == baseS, "shuffled type switch aggregate mismatch")
		ck(pt.X == 5 && pt.Y == 6 && pt.Tag.V == 7 && pt.Tag.Label == "p", "boxed pointer survives GC")
		ck(len(vals) == len(kinds), "interface slice length stable")
	}

	ck(gotK == int64(rounds)*baseK, "fixed-order type switch aggregate mismatch")
	ck(gotS == int64(rounds)*baseS, "fixed-order type switch sum mismatch")

	finish("l06_type_switch_many")
}
