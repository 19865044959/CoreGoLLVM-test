// L17: defer 中的接口方法调用。defer 记录保存 interface data；GC 后 deferreturn 再通过 itab 调用方法。
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
	name string
	next *item
}

type closer interface {
	Close() int64
	Peek() *item
}

type res struct {
	head  *item
	list  []*item
	tag   string
	calls []int64
}

type vres struct {
	head *item
	list []*item
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

func gcStress() {
	var sink [][]byte
	for i := 0; i < 3; i++ {
		b := make([]byte, 1024)
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

//go:noinline
func (r *res) Close() int64 {
	gcStress()
	if r.head == nil || r.head.next == nil {
		ck(false, "res.Close: head corrupted")
		return 0
	}
	if r.head.name != "head" || r.head.next.name != "tail" {
		ck(false, "res.Close: head names corrupted")
	}
	for _, it := range r.list {
		if it == nil || it.name != "list" {
			ck(false, "res.Close: list corrupted")
			break
		}
	}
	r.calls = append(r.calls, 1)
	return r.head.v + r.head.next.v
}

//go:noinline
func (r *res) Peek() *item {
	gcStress()
	return r.head
}

//go:noinline
func (v vres) Close() int64 {
	gcStress()
	if v.head == nil || v.head.next == nil {
		ck(false, "vres.Close: head corrupted")
		return 0
	}
	for _, it := range v.list {
		if it == nil {
			ck(false, "vres.Close: list corrupted")
			break
		}
	}
	return v.head.v
}

//go:noinline
func (v vres) Peek() *item {
	gcStress()
	return v.head
}

// useDirect: defer 语句直接调用接口方法，interface data 存活到 deferreturn。
//
//go:noinline
func useDirect(c closer, where string) (sum int64) {
	defer c.Close()
	defer func() {
		gcStress()
		p := c.Peek()
		if p == nil {
			ck(false, where+": Peek nil")
			return
		}
		sum += p.v
	}()
	gcStress()
	p := c.Peek()
	if p == nil {
		ck(false, where+": initial Peek nil")
		return
	}
	sum = p.v * 2
	return
}

// useCaptured: defer 闭包在返回时调用接口方法并累加返回值。
//
//go:noinline
func useCaptured(c closer, where string) (sum int64) {
	defer func() {
		gcStress()
		sum += c.Close()
		gcStress()
		if c.Peek() == nil {
			ck(false, where+": Peek nil after Close")
		}
	}()
	gcStress()
	sum = 1
	return
}

// useArg: 接口值作为 defer 实参被求值并复制进 defer 记录。
//
//go:noinline
func useArg(c closer, where string) (sum int64) {
	defer func(x closer) {
		gcStress()
		sum += x.Close()
	}(c)
	gcStress()
	sum = 2
	return
}

// useMethodExpr: 接口方法表达式先取函数值，再在 defer 闭包中调用。
//
//go:noinline
func useMethodExpr(c closer, where string) (sum int64) {
	f := closer.Close
	defer func() {
		gcStress()
		sum += f(c)
	}()
	gcStress()
	sum = 3
	return
}

func main() {
	uafSeed = seedFromEnv()
	const rounds = 10

	for rnd := 0; rnd < rounds; rnd++ {
		base := int64(rnd*1000) + uafSeed%29
		n := 3 + int((uafSeed+int64(rnd))%4)
		r := &res{
			head: &item{v: base + 1, name: "head", next: &item{v: base + 2, name: "tail"}},
			tag:  "res",
		}
		for i := 0; i < n; i++ {
			r.list = append(r.list, &item{v: base + 10 + int64(i), name: "list"})
		}

		s1 := useDirect(r, fmt.Sprintf("round %d direct", rnd))
		gcStress()
		ck(s1 == 3*(base+1), fmt.Sprintf("round %d direct sum=%d want=%d seed=%d", rnd, s1, 3*(base+1), uafSeed))

		s2 := useCaptured(r, fmt.Sprintf("round %d captured", rnd))
		gcStress()
		want2 := int64(1) + (base + 1) + (base + 2)
		ck(s2 == want2, fmt.Sprintf("round %d captured sum=%d want=%d seed=%d", rnd, s2, want2, uafSeed))

		s3 := useArg(r, fmt.Sprintf("round %d arg", rnd))
		gcStress()
		ck(s3 == 2+(base+1)+(base+2), fmt.Sprintf("round %d arg sum=%d want=%d seed=%d", rnd, s3, 2+(base+1)+(base+2), uafSeed))

		s4 := useMethodExpr(r, fmt.Sprintf("round %d expr", rnd))
		gcStress()
		ck(s4 == 3+(base+1)+(base+2), fmt.Sprintf("round %d expr sum=%d want=%d seed=%d", rnd, s4, 3+(base+1)+(base+2), uafSeed))

		ck(len(r.calls) == 4, fmt.Sprintf("round %d close calls=%d want=4 seed=%d", rnd, len(r.calls), uafSeed))
		if r.head == nil || r.head.v != base+1 || r.head.next == nil || r.head.next.v != base+2 {
			ck(false, fmt.Sprintf("round %d res head corrupted after defers seed=%d", rnd, uafSeed))
		}
		if len(r.list) != n || r.list[n-1].v != base+10+int64(n-1) {
			ck(false, fmt.Sprintf("round %d res list corrupted after defers seed=%d", rnd, uafSeed))
		}

		vr := vres{head: &item{v: base + 40, name: "head", next: &item{v: base + 41, name: "tail"}},
			list: []*item{{v: base + 42, name: "list"}}}
		vs := useCaptured(vr, fmt.Sprintf("round %d value", rnd))
		gcStress()
		ck(vs == 1+base+40, fmt.Sprintf("round %d value receiver sum=%d want=%d seed=%d", rnd, vs, 1+base+40, uafSeed))
		ck(vr.head != nil && vr.head.v == base+40 && vr.head.next != nil && vr.head.next.v == base+41,
			fmt.Sprintf("round %d vres corrupted after GC seed=%d", rnd, uafSeed))
	}

	finish("l17_defer_iface_method")
}
