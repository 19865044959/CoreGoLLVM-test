// L17: spill interface 矩阵。16 个 any 局部/形参 + 含非空接口字段的聚合 +
// 固定数组，总量超过寄存器后落到溢出槽；深度递归 morestack 后 GC 再全量重扫。
package main

import (
	"fmt"
	"os"
	"runtime"
	"strconv"
	"sync/atomic"
)

type item struct {
	v    int64
	tag  string
	next *item
}

type pair struct {
	p *item
	s string
}

type scorerIface interface {
	score() int64
}

type scorer struct {
	p *item
}

func (s *scorer) score() int64 {
	if s == nil || s.p == nil {
		ck(false, fmt.Sprintf("scorer: nil receiver/field seed=%d", uafSeed))
		return 0
	}
	return itemVal(s.p, "scorer") + 7
}

type holder struct {
	f scorerIface
}

var (
	failed  int32
	uafSeed int64 = 1
)

func ck(ok bool, msg string) {
	if !ok {
		atomic.AddInt32(&failed, 1)
		fmt.Println("FAIL:", msg)
	}
}

//go:noinline
func gcStress() {
	var sink [][]byte
	for i := 0; i < 6; i++ {
		b := make([]byte, 1536)
		b[0] = byte(i)
		sink = append(sink, b)
	}
	runtime.GC()
	runtime.GC()
	runtime.KeepAlive(sink)
}

func seedFromEnv() int64 {
	if s := os.Getenv("UAF_SEED"); s != "" {
		if v, err := strconv.ParseInt(s, 10, 64); err == nil && v != 0 {
			return v
		}
	}
	return 1
}

func envRounds(def, max int) int {
	if s := os.Getenv("TORTURE_ROUNDS"); s != "" {
		if v, err := strconv.Atoi(s); err == nil && v > 0 {
			if v > max {
				return max
			}
			return v
		}
	}
	return def
}

func finish(name string) {
	if n := atomic.LoadInt32(&failed); n != 0 {
		fmt.Printf("FAILED %s (%d failed checks) seed=%d\n", name, n, uafSeed)
		os.Exit(1)
	}
	fmt.Println("ALL PASS " + name)
}

func mkItem(base int64) *item {
	return &item{v: base, tag: "mx", next: &item{v: base + 9, tag: "mx-next"}}
}

func itemVal(x *item, where string) int64 {
	if x == nil {
		ck(false, fmt.Sprintf("%s: nil item seed=%d", where, uafSeed))
		return 0
	}
	if x.tag != "mx" || x.next == nil || x.next.tag != "mx-next" || x.next.v != x.v+9 {
		ck(false, fmt.Sprintf("%s: item chain corrupted seed=%d", where, uafSeed))
	}
	return x.v + x.next.v
}

func scanAny(a any, where string, idx int) int64 {
	switch x := a.(type) {
	case nil:
		return 0
	case *item:
		if x == nil {
			return 0
		}
		return itemVal(x, fmt.Sprintf("%s[%d]", where, idx))
	case pair:
		return itemVal(x.p, fmt.Sprintf("%s[%d].p", where, idx)) + int64(len(x.s))
	case []*item:
		var sum int64
		for i, p := range x {
			sum += itemVal(p, fmt.Sprintf("%s[%d].list[%d]", where, idx, i))
		}
		return sum
	case string:
		return int64(len(x))
	case int64:
		return x
	case map[string]*item:
		var sum int64
		for k, p := range x {
			sum += itemVal(p, fmt.Sprintf("%s[%d].map[%s]", where, idx, k)) + int64(len(k))
		}
		return sum
	case [2]*item:
		return itemVal(x[0], where+".arr2[0]") + itemVal(x[1], where+".arr2[1]")
	case [4]*item:
		var sum int64
		for i, p := range x {
			sum += itemVal(p, fmt.Sprintf("%s[%d].arr4[%d]", where, idx, i))
		}
		return sum
	case [4]any:
		var sum int64
		for i, v := range x {
			sum += scanAny(v, fmt.Sprintf("%s[%d].any4", where, idx), i)
		}
		return sum
	case func() int64:
		return x()
	case chan *item:
		select {
		case p := <-x:
			sum := itemVal(p, fmt.Sprintf("%s[%d].chan", where, idx))
			x <- p
			return sum
		default:
			ck(false, fmt.Sprintf("%s: empty channel at %d seed=%d", where, idx, uafSeed))
			return 0
		}
	case holder:
		if x.f == nil {
			ck(false, fmt.Sprintf("%s: nil holder iface at %d seed=%d", where, idx, uafSeed))
			return 0
		}
		return x.f.score()
	case *scorer:
		if x == nil {
			return 0
		}
		return x.score()
	case []any:
		var sum int64
		for i, v := range x {
			sum += scanAny(v, fmt.Sprintf("%s[%d].nested", where, idx), i)
		}
		return sum
	case *[]*item:
		var sum int64
		for i, p := range *x {
			sum += itemVal(p, fmt.Sprintf("%s[%d].ptrlist[%d]", where, idx, i))
		}
		return sum
	default:
		ck(false, fmt.Sprintf("%s: unexpected type %T at %d seed=%d", where, a, idx, uafSeed))
		return 0
	}
}

func scanMatrix(u0, u1, u2, u3, u4, u5, u6, u7, u8, u9, u10, u11, u12, u13, u14, u15 any,
	p0, p1, p2 pair, arr [4]*item, anys [4]any, where string) int64 {
	vals := [...]any{u0, u1, u2, u3, u4, u5, u6, u7, u8, u9, u10, u11, u12, u13, u14, u15}
	var sum int64
	for i, v := range vals {
		sum += scanAny(v, where+"."+strconv.Itoa(i), i)
	}
	sum += scanAny(p0, where+".p0", 0) + scanAny(p1, where+".p1", 1) + scanAny(p2, where+".p2", 2)
	sum += scanAny(arr, where+".arr", 0)
	sum += scanAny(anys, where+".anys", 0)
	return sum
}

//go:noinline
func deep(n int, p *item, base int64) int64 {
	var buf [192]byte
	buf[0] = byte(n)
	buf[len(buf)-1] = byte(base)
	if n <= 0 {
		gcStress()
		return itemVal(p, "deep") + int64(buf[0]) + int64(buf[len(buf)-1])
	}
	return deep(n-1, p, base+1) + int64(buf[0])
}

//go:noinline
func observe(u0, u1, u2, u3, u4, u5, u6, u7, u8, u9, u10, u11, u12, u13, u14, u15 any,
	p0, p1, p2 pair, arr [4]*item, anys [4]any) int64 {
	pre := scanMatrix(u0, u1, u2, u3, u4, u5, u6, u7, u8, u9, u10, u11, u12, u13, u14, u15, p0, p1, p2, arr, anys, "observe-pre")
	gcStress()
	post := scanMatrix(u0, u1, u2, u3, u4, u5, u6, u7, u8, u9, u10, u11, u12, u13, u14, u15, p0, p1, p2, arr, anys, "observe-post")
	if pre != post {
		ck(false, fmt.Sprintf("observe: matrix changed across GC pre=%d post=%d seed=%d", pre, post, uafSeed))
	}
	return post
}

func matrix(base int64, u0, u1, u2, u3, u4, u5, u6, u7, u8, u9, u10, u11, u12, u13, u14, u15 any,
	p0, p1, p2 pair, arr [4]*item, anys [4]any) int64 {
	pre := scanMatrix(u0, u1, u2, u3, u4, u5, u6, u7, u8, u9, u10, u11, u12, u13, u14, u15, p0, p1, p2, arr, anys, "matrix-pre")
	dn := deep(24, arr[0], base)
	gcStress()
	post := scanMatrix(u0, u1, u2, u3, u4, u5, u6, u7, u8, u9, u10, u11, u12, u13, u14, u15, p0, p1, p2, arr, anys, "matrix-post")
	if pre != post {
		ck(false, fmt.Sprintf("matrix: args changed across GC/deep pre=%d post=%d seed=%d", pre, post, uafSeed))
	}
	obs := observe(u0, u1, u2, u3, u4, u5, u6, u7, u8, u9, u10, u11, u12, u13, u14, u15, p0, p1, p2, arr, anys)
	if obs != pre {
		ck(false, fmt.Sprintf("matrix: observe sum=%d want=%d seed=%d", obs, pre, uafSeed))
	}
	return post + dn%7
}

func main() {
	uafSeed = seedFromEnv()
	rounds := envRounds(8, 1024)

	for r := 0; r < rounds; r++ {
		base := int64(r*1000) + uafSeed%29
		ch := make(chan *item, 1)
		ch <- mkItem(base + 40)
		pl := []*item{mkItem(base + 30), mkItem(base + 31)}
		u0 := any(mkItem(base))
		u1 := any(pair{p: mkItem(base + 1), s: "p1"})
		u2 := any([]*item{mkItem(base + 2), mkItem(base + 3)})
		u3 := any("matrix")
		u4 := any(nil)
		u5 := any((*item)(nil))
		u6 := any(map[string]*item{"a": mkItem(base + 6), "b": mkItem(base + 7)})
		u7 := any([2]*item{mkItem(base + 8), mkItem(base + 9)})
		capItem := mkItem(base + 10)
		u8 := any(func() int64 { return capItem.v + 11 })
		u9 := any(ch)
		u10 := any(holder{f: &scorer{p: mkItem(base + 12)}})
		u11 := any(&pl)
		u12 := any([]any{mkItem(base + 13), "nested", int64(5)})
		u13 := any((*scorer)(nil))
		u14 := any(pair{p: mkItem(base + 14), s: "p14"})
		u15 := any(int64(15) + base)
		arr := [4]*item{mkItem(base + 20), mkItem(base + 21), mkItem(base + 22), mkItem(base + 23)}
		anys := [4]any{mkItem(base + 24), "any4", nil, pair{p: mkItem(base + 25), s: "a4"}}
		p0 := pair{p: mkItem(base + 26), s: "p0"}
		p1 := pair{p: mkItem(base + 27), s: "p1"}
		p2 := pair{p: mkItem(base + 28), s: "p2"}

		args := []any{u0, u1, u2, u3, u4, u5, u6, u7, u8, u9, u10, u11, u12, u13, u14, u15, p0, p1, p2, arr, anys}
		before := scanMatrix(u0, u1, u2, u3, u4, u5, u6, u7, u8, u9, u10, u11, u12, u13, u14, u15, p0, p1, p2, arr, anys, "caller-before")
		got := matrix(base, u0, u1, u2, u3, u4, u5, u6, u7, u8, u9, u10, u11, u12, u13, u14, u15, p0, p1, p2, arr, anys)
		gcStress()
		after := scanMatrix(u0, u1, u2, u3, u4, u5, u6, u7, u8, u9, u10, u11, u12, u13, u14, u15, p0, p1, p2, arr, anys, "caller-after")
		if before != after {
			ck(false, fmt.Sprintf("round %d: caller matrix changed across call/GC before=%d after=%d seed=%d", r, before, after, uafSeed))
		}
		if got < after {
			ck(false, fmt.Sprintf("round %d: matrix return too small got=%d after=%d seed=%d", r, got, after, uafSeed))
		}
		if capItem.v != base+10 || capItem.tag != "mx" {
			ck(false, fmt.Sprintf("round %d: closure captured item corrupted seed=%d", r, uafSeed))
		}
		if len(args) != 21 {
			ck(false, fmt.Sprintf("round %d: args slice length=%d seed=%d", r, len(args), uafSeed))
		}
	}

	finish("l17_spill_iface_matrix")
}
