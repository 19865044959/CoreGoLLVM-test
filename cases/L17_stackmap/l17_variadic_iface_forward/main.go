// L17: 变参 interface 跨接口层转发。caller 的 []any、forward 的 ...any 形参切片
// 与 sink 的 ...any 形参切片交替存活；GC 落在每一层，接口 data word 必须可扫。
package main

import (
	"fmt"
	"os"
	"runtime"
	"strconv"
	"sync/atomic"
)

type token struct {
	id   int64
	tag  string
	next *token
}

type box struct {
	t   *token
	tag string
}

type forwarder interface {
	forward(prefix string, vals ...any) (int64, *token)
}

type layer struct {
	base int64
	fn   func(string, ...any) (int64, *token)
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
	for i := 0; i < 8; i++ {
		b := make([]byte, 2048)
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

func finish(name string) {
	if n := atomic.LoadInt32(&failed); n != 0 {
		fmt.Printf("FAILED %s (%d failed checks) seed=%d\n", name, n, uafSeed)
		os.Exit(1)
	}
	fmt.Println("ALL PASS " + name)
}

type lcg struct{ s uint64 }

func newLCG(seed int64) *lcg {
	return &lcg{s: uint64(seed)*0x9e3779b97f4a7c15 + 0x2545f4914f6cdd1d}
}

func (l *lcg) next() uint64 {
	l.s = l.s*6364136223846793005 + 1442695040888963407
	return l.s >> 33
}

func (l *lcg) intn(n int) int {
	if n <= 0 {
		return 0
	}
	return int(l.next() % uint64(n))
}

func mkToken(base int64) *token {
	return &token{id: base, tag: "tok", next: &token{id: base + 1000, tag: "tail"}}
}

func checkToken(t *token, where string, idx int) {
	if t == nil || t.tag != "tok" || t.next == nil || t.next.id != t.id+1000 || t.next.tag != "tail" {
		ck(false, fmt.Sprintf("%s: token at arg[%d] corrupted seed=%d", where, idx, uafSeed))
	}
}

// scan 遍历一遍变参切片并校验其中的接口值，返回 id 和最后一个 token。
func scan(vals []any, where string) (int64, *token) {
	sum := int64(0)
	var tail *token
	for i, a := range vals {
		switch x := a.(type) {
		case nil:
			// nil interface：无 data word 可校验。
		case *token:
			checkToken(x, where, i)
			if x != nil {
				sum += x.id
				tail = x
			}
		case box:
			checkToken(x.t, where, i)
			if x.tag != "box" {
				ck(false, fmt.Sprintf("%s: box tag=%q at arg[%d] seed=%d", where, x.tag, i, uafSeed))
			}
			if x.t != nil {
				sum += x.t.id
				tail = x.t
			}
		case []*token:
			if len(x) != 2 {
				ck(false, fmt.Sprintf("%s: seq len=%d at arg[%d] seed=%d", where, len(x), i, uafSeed))
			}
			for j, p := range x {
				if p == nil || p.id != int64(j+1) || p.tag != "seq" {
					ck(false, fmt.Sprintf("%s: seq[%d] corrupted at arg[%d] seed=%d", where, j, i, uafSeed))
					continue
				}
				sum += p.id
				tail = p
			}
		case string:
			sum += int64(len(x))
		default:
			ck(false, fmt.Sprintf("%s: unexpected type %T at arg[%d] seed=%d", where, a, i, uafSeed))
		}
	}
	return sum, tail
}

//go:noinline
func sink(prefix string, vals ...any) (int64, *token) {
	gcStress()
	s1, t1 := scan(vals, "sink-before")
	gcStress()
	s2, t2 := scan(vals, "sink-after")
	ck(s1 == s2 && t1 == t2,
		fmt.Sprintf("sink %q: variadic slice changed across GC seed=%d", prefix, uafSeed))
	runtime.KeepAlive(vals)
	return s2, t2
}

//go:noinline
func (l *layer) forward(prefix string, vals ...any) (int64, *token) {
	gcStress()
	b1, bt1 := scan(vals, "forward-before")
	sum, tail := l.fn(prefix+"|layer", vals...)
	gcStress()
	b2, bt2 := scan(vals, "forward-after")
	ck(b1 == b2 && bt1 == bt2,
		fmt.Sprintf("forward: caller slice changed across forwarding GC seed=%d", uafSeed))
	ck(sum == b2 && tail == bt2,
		fmt.Sprintf("forward: callee view differs from caller view seed=%d", uafSeed))
	runtime.KeepAlive(vals)
	return b2 + l.base, bt2
}

func makeArgs(base int64) []any {
	return []any{
		mkToken(base),
		box{t: mkToken(base + 10), tag: "box"},
		[]*token{{id: 1, tag: "seq"}, {id: 2, tag: "seq"}},
		"alpha",
		nil,
		box{t: mkToken(base + 20), tag: "box"},
		"beta",
	}
}

func directSum() int64 {
	// mkToken(5000)=5000, mkToken(6000)=6000, seq=1+2, "gamma"=5, layer.base=7。
	return 5000 + 6000 + 1 + 2 + 5 + 7
}

func main() {
	uafSeed = seedFromEnv()
	rnd := newLCG(uafSeed)
	f := &layer{base: 7, fn: sink}

	for r := 0; r < 12; r++ {
		args := makeArgs(int64(r) * 100)
		for i := len(args) - 1; i > 0; i-- {
			j := rnd.intn(i + 1)
			args[i], args[j] = args[j], args[i]
		}
		wantSum, wantTail := scan(args, "caller-before")
		sum, tail := f.forward("round", args...)
		gcStress()
		gotSum, gotTail := scan(args, "caller-after")
		ck(gotSum == wantSum && gotTail == wantTail,
			fmt.Sprintf("round %d: args corrupted after forward seed=%d", r, uafSeed))
		ck(sum == wantSum+7 && tail == wantTail,
			fmt.Sprintf("round %d: forward sum/tail mismatch seed=%d", r, uafSeed))
		runtime.KeepAlive(args)
	}

	// 调用点直接构造变参片段，不经过 []any 局部变量。
	sum, tail := f.forward("direct", mkToken(5000), box{t: mkToken(6000), tag: "box"},
		[]*token{{id: 1, tag: "seq"}, {id: 2, tag: "seq"}}, "gamma", nil)
	gcStress()
	ck(sum == directSum(), fmt.Sprintf("direct call sum=%d want=%d seed=%d", sum, directSum(), uafSeed))
	ck(tail != nil && tail.id == 2 && tail.tag == "seq",
		fmt.Sprintf("direct call tail corrupted seed=%d", uafSeed))

	finish("l17_variadic_iface_forward")
}
