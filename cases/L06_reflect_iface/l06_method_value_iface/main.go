// L06: 方法值/方法表达式/接口方法值经接口调用，跨 GC 后派发正确。
package main

import (
	"fmt"
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

type counter struct {
	n   int64
	tag *node
}

func (c *counter) Inc(d int64) int64 { gcStress(); c.n += d; return c.n + c.tag.V }
func (c counter) Get() int64         { gcStress(); return c.n + c.tag.V }

type adder interface{ Inc(int64) int64 }
type getter interface{ Get() int64 }
type both interface {
	adder
	getter
}

type embed struct{ *counter }

var _ both = (*counter)(nil)
var _ getter = counter{}
var _ both = (*embed)(nil)

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

func main() {
	uafSeed = envSeed()
	const rounds = 60
	for r := 0; r < rounds; r++ {
		c := &counter{n: int64(r), tag: &node{V: 3, Label: "c"}}
		var a adder = c
		var b both = c
		var g getter = counter{n: 100, tag: &node{V: 4, Label: "g"}}

		inc := c.Inc
		expr := (*counter).Inc
		incIface := a.Inc
		exprIface := adder.Inc
		vget := g.Get
		gcStress()

		ck(inc(2) == int64(r)+5, "method value after GC")
		ck(expr(c, 3) == int64(r)+8, "method expression after GC")
		ck(incIface(4) == int64(r)+12, "interface method value after GC")
		ck(exprIface(c, 1) == int64(r)+13, "interface method expression after GC")
		ck(b.Inc(0) == int64(r)+13, "embedded interface dispatch after GC")
		ck(b.Get() == int64(r)+13, "value receiver via interface after GC")
		ck(vget() == 104, "value receiver method value snapshot")

		// 方法值装入 map，GC 后按固定 key 顺序调用。
		fns := map[string]func(int64) int64{
			"a": inc,
			"b": incIface,
			"c": b.Inc,
			"d": func(d int64) int64 { return expr(c, d) },
		}
		keys := make([]string, 0, len(fns))
		for k := range fns {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		gcStress()
		var sum int64
		for _, k := range keys {
			sum += fns[k](1)
		}
		ck(len(keys) == 4 && sum == 4*int64(r)+62, "method value map after GC")
		ck(c.n == int64(r)+14, "method value map updated receiver") // +4 calls of 1 after +10

		// 匿名嵌入提升的方法值与接口派发。
		e := &embed{counter: c}
		emv := e.Inc
		var eb both = e
		gcStress()
		want := c.n + c.tag.V + 5
		ck(emv(5) == want, "promoted method value after GC")
		ck(eb.Get() == c.n+c.tag.V, "promoted getter via interface after GC")

		// 值接收者方法值捕获副本，但共享指针字段。
		vt := &node{V: 50, Label: "v"}
		vv := counter{n: 100, tag: vt}
		f := vv.Get
		vt.V = 60
		gcStress()
		ck(f() == 160, "value method value copies receiver, shares pointer")
		vv.n = 500
		ck(f() == 160, "value method value keeps receiver snapshot")
	}
	finish("l06_method_value_iface")
}
