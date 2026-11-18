// L06: 值/指针接收者方法集：NumMethod、Implements、方法表达式/绑定方法、嵌入提升。
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

type node struct {
	V     int64
	Label string
}

type counter struct {
	n   int64
	tag *node
}

func (c *counter) Inc(d int64) int64 { return c.add(d) }
func (c *counter) PtrOnly() int64    { return c.n * 2 }
func (c counter) Get() int64         { return c.n + c.tag.V }
func (c counter) ValOnly() int64     { return c.tag.V - c.n }
func (c *counter) add(d int64) int64 { c.n += d; return c.n }

type incer interface{ Inc(int64) int64 }
type getter interface{ Get() int64 }
type ptrOnlyIface interface{ PtrOnly() int64 }
type valOnlyIface interface{ ValOnly() int64 }

var _ incer = (*counter)(nil)
var _ getter = counter{}
var _ getter = (*counter)(nil)
var _ ptrOnlyIface = (*counter)(nil)
var _ valOnlyIface = counter{}
var _ valOnlyIface = (*counter)(nil)

type embedPtr struct{ *counter }
type embedVal struct{ counter }

var (
	failed    int32
	gcSink    [][]byte
	anySink   any
	boundSink []reflect.Value
	uafSeed   int64 = 1
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

	tVal := reflect.TypeOf(counter{})
	tPtr := reflect.TypeOf(&counter{})
	tInc := reflect.TypeOf((*incer)(nil)).Elem()
	tGet := reflect.TypeOf((*getter)(nil)).Elem()
	tPtrOnly := reflect.TypeOf((*ptrOnlyIface)(nil)).Elem()
	tValOnly := reflect.TypeOf((*valOnlyIface)(nil)).Elem()
	tEmbedPtr := reflect.TypeOf(embedPtr{})
	tEmbedVal := reflect.TypeOf(embedVal{})
	tPtrEmbedVal := reflect.TypeOf(&embedVal{})

	ck(tVal.NumMethod() == 2, "value method set has Get/ValOnly only")
	ck(tPtr.NumMethod() == 4, "pointer method set has all four methods")
	names := make([]string, 0, tPtr.NumMethod())
	for i := 0; i < tPtr.NumMethod(); i++ {
		names = append(names, tPtr.Method(i).Name)
	}
	sort.Strings(names)
	ck(reflect.DeepEqual(names, []string{"Get", "Inc", "PtrOnly", "ValOnly"}), "pointer method set names")
	if _, ok := tVal.MethodByName("Inc"); ok {
		ck(false, "value type has no pointer-receiver method")
	}
	if _, ok := tVal.MethodByName("PtrOnly"); ok {
		ck(false, "value type has no PtrOnly")
	}
	if m, ok := tPtr.MethodByName("Inc"); !ok {
		ck(false, "pointer type has Inc")
	} else if _, ok := tPtr.MethodByName("Get"); !ok {
		ck(false, "pointer type has Get")
	} else {
		ck(m.Name == "Inc", "pointer type sees both receivers")
	}

	ck(!tVal.Implements(tInc), "counter value does not implement incer")
	ck(tPtr.Implements(tInc), "counter pointer implements incer")
	ck(tVal.Implements(tGet) && tPtr.Implements(tGet), "both implement getter")
	ck(!tVal.Implements(tPtrOnly) && tPtr.Implements(tPtrOnly), "PtrOnly is pointer-only")
	ck(tVal.Implements(tValOnly) && tPtr.Implements(tValOnly), "ValOnly promoted to both")

	ck(tEmbedPtr.NumMethod() == 4, "embed *counter promotes all methods to value")
	ck(tEmbedVal.NumMethod() == 2, "embed counter value promotes value methods only")
	ck(tPtrEmbedVal.NumMethod() == 4, "pointer to embed value sees all methods")
	ck(tEmbedPtr.Implements(tInc), "embed *counter value implements incer")
	ck(!tEmbedVal.Implements(tInc), "embed counter value does not implement incer")
	ck(tPtrEmbedVal.Implements(tInc), "pointer to embed counter implements incer")
	ck(tEmbedVal.Implements(tGet) && tPtrEmbedVal.Implements(tGet), "embed value implements getter")

	const rounds = 12
	for r := 0; r < rounds; r++ {
		rnd := rand.New(rand.NewSource(uafSeed + int64(r)*211))
		base := int64(r * 50)
		delta := int64(1 + rnd.Intn(9))
		c := &counter{n: base, tag: &node{V: 100 + int64(rnd.Intn(30)), Label: "tag"}}
		anySink = c

		// 方法表达式：reflect.Type.MethodByName(i).Func 的第一个参数是接收者。
		m, mok := tPtr.MethodByName("Inc")
		ck(mok, "pointer method Inc exists")
		out := m.Func.Call([]reflect.Value{reflect.ValueOf(c), reflect.ValueOf(delta)})
		ck(out[0].Int() == base+delta, "method expression call mutates receiver")

		// 绑定方法值捕获当前值接收者状态，GC 后仍可调用。
		bound := reflect.ValueOf(c).MethodByName("Get")
		boundSink = append(boundSink[:0], bound)
		boundWant := c.n + c.tag.V
		gcStress()
		ck(boundSink[0].Call(nil)[0].Int() == boundWant, "bound method value survives GC")
		ck(c.n == base+delta, "pointer receiver mutation survives GC")

		// 接口 Value 上的方法调用（动态派发）。
		var g getter = c
		gv := reflect.ValueOf(&g).Elem()
		ck(gv.MethodByName("Get").Call(nil)[0].Int() == c.n+c.tag.V, "interface method call via reflect")
		ck(gv.Elem().Type() == tPtr, "interface holds pointer dynamic type")

		// 嵌入 *counter：值与指针都提升全部方法，共享底层计数器。
		ep := embedPtr{counter: c}
		var ei incer = ep
		ei.Inc(3)
		ck(c.n == base+delta+3, "embed *counter value promotes pointer method")
		var eg getter = ep
		ck(eg.Get() == c.n+c.tag.V, "embed *counter promotes value method")

		// 嵌入 counter 值：值只提升值方法，指针才提升指针方法。
		ev := embedVal{counter: *c}
		var evg getter = ev
		evCopyN := ev.n
		c.n += 1000
		ck(evg.Get() == evCopyN+c.tag.V, "embed value method captures copy of n")
		ck(evg.Get() != c.n+c.tag.V, "embed value copy independent of source n")
		c.n -= 1000
		var epi incer = &ev
		epi.Inc(2)
		ck(ev.n == evCopyN+2 && c.n == base+delta+3, "pointer to embed value mutates its own copy")
		ck(!tEmbedVal.Implements(tInc) && tPtrEmbedVal.Implements(tInc), "value/pointer embed method set table")

		// 值接收者绑定方法捕获副本：修改原对象不影响已绑定方法值。
		val := counter{n: 7, tag: &node{V: 5}}
		vg := reflect.ValueOf(val).MethodByName("ValOnly")
		val.n = 100
		ck(vg.Call(nil)[0].Int() == 5-7, "value-receiver bound method keeps copy")
		anySink = []any{ep, ev, val}

		gcStress()
		ck(boundSink[0].Call(nil)[0].Int() == c.n+c.tag.V, "pointer-bound method follows receiver after GC")
		ck(c.tag.V >= 100 && c.tag.Label == "tag", "receiver pointer field survives GC")
	}
	finish("l06_methodset_value_ptr")
}
