// L01: 泛型 Box[T] 持有 *T，跨多轮 GC 后指针身份与 payload 内容必须完好。
package main

import (
	"fmt"
	"os"
	"runtime"
	"strconv"
	"sync/atomic"
)

type payload struct {
	v     int64
	name  string
	child *payload
}

type Box[T any] struct {
	v   *T
	tag string
}

var (
	failed  int32
	gcSink  [][]byte
	boxSink []*Box[payload]
	uafSeed int64 = 1
)

func NewBox[T any](v *T, tag string) *Box[T] { return &Box[T]{v: v, tag: tag} }

func (b *Box[T]) Get() *T { return b.v }

func (b *Box[T]) Valid() bool { return b.v != nil }

func (b *Box[T]) Replace(v *T) *T {
	old := b.v
	b.v = v
	return old
}

func ck(ok bool, msg string) {
	if !ok {
		atomic.AddInt32(&failed, 1)
		fmt.Println("FAIL:", msg)
	}
}

func gcStress() {
	for i := 0; i < 8; i++ {
		b := make([]byte, 2048)
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
	n := 96 + int(uafSeed%17)

	orig := make([]*payload, n)
	boxes := make([]*Box[payload], n)
	seen := make(map[*payload]bool, n)
	for i := 0; i < n; i++ {
		p := &payload{v: int64(i*3 + 1), name: fmt.Sprintf("p%d", i)}
		if i > 0 {
			p.child = orig[i-1]
		}
		orig[i] = p
		boxes[i] = NewBox(p, fmt.Sprintf("t%d", i))
		ck(!seen[p], "duplicate payload pointer before GC")
		seen[p] = true
	}
	boxSink = boxes
	gcStress()

	for i := 0; i < n; i++ {
		b := boxes[i]
		ck(b.Valid(), "box lost pointer field")
		ck(b.Get() == orig[i], "box pointer identity changed across GC")
		ck(b.Get().v == int64(i*3+1), "box payload value corrupted")
		ck(b.Get().name == fmt.Sprintf("p%d", i), "box payload name corrupted")
		ck(b.tag == fmt.Sprintf("t%d", i), "box tag corrupted")
		if i > 0 {
			ck(b.Get().child == orig[i-1], "payload child link broken")
		}
	}

	// nil 指针字段跨 GC 仍是 nil。
	nilBox := NewBox[payload](nil, "nil")
	gcStress()
	ck(!nilBox.Valid(), "nil box became valid after GC")
	ck(nilBox.Get() == nil, "nil box produced non-nil pointer after GC")

	// 替换指针后旧指针身份、新 payload 内容都必须在 GC 后保持。
	repl := &payload{v: 777, name: "repl"}
	old := boxes[0].Replace(repl)
	ck(old == orig[0], "Replace returned wrong old pointer")
	ck(boxes[0].Get() == repl && repl.v == 777, "Replace new pointer not visible")
	gcStress()
	ck(boxes[0].Get() == repl && boxes[0].Get().v == 777, "Replace pointer lost after GC")
	ck(boxes[0].Get().name == "repl", "Replace payload name corrupted after GC")

	// 标量实例 Box[int64] 与深链尾节点。
	x := int64(42)
	ib := NewBox[int64](&x, "int")
	gcStress()
	ck(*ib.Get() == 42, "int64 box value corrupted")
	*ib.Get() = 43
	ck(*ib.Get() == 43, "int64 box write-through failed")

	depth := 0
	for p := boxes[n-1].Get(); p != nil; p = p.child {
		depth++
	}
	ck(depth == n, fmt.Sprintf("child chain depth got=%d want=%d", depth, n))

	finish("l01_box_pointer_gc")
}
