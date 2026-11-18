// L01: 泛型类型的方法集 + 泛型接口实现，GC 后通过接口/方法值派发。
package main

import (
	"fmt"
	"os"
	"runtime"
	"strconv"
	"sync/atomic"
)

type record struct {
	id   int64
	name string
	next *record
}

type Collection[T any] interface {
	Push(*T)
	Pop() *T
	Len() int
}

type Stack[T any] struct {
	items []*T
}

func (s *Stack[T]) Push(p *T) { s.items = append(s.items, p) }

func (s *Stack[T]) Pop() *T {
	n := len(s.items)
	if n == 0 {
		return nil
	}
	p := s.items[n-1]
	s.items[n-1] = nil
	s.items = s.items[:n-1]
	return p
}

func (s *Stack[T]) Len() int { return len(s.items) }

type Queue[T any] struct {
	items []*T
	head  int
}

func (q *Queue[T]) Push(p *T) { q.items = append(q.items, p) }

func (q *Queue[T]) Pop() *T {
	if q.head == len(q.items) {
		return nil
	}
	p := q.items[q.head]
	q.items[q.head] = nil
	q.head++
	return p
}

func (q *Queue[T]) Len() int { return len(q.items) - q.head }

// 嵌入泛型实例化，方法集通过提升满足泛型接口。
type Deque[T any] struct {
	*Stack[T]
}

var (
	_ Collection[int64]  = (*Stack[int64])(nil)
	_ Collection[int64]  = (*Queue[int64])(nil)
	_ Collection[record] = (*Deque[record])(nil)
)

func drain[T any](c Collection[T]) []*T {
	var out []*T
	for c.Len() > 0 {
		out = append(out, c.Pop())
	}
	return out
}

var (
	failed  int32
	gcSink  [][]byte
	recSink []*record
	uafSeed int64 = 1
)

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
	n := 64 + int(uafSeed%9)
	recs := make([]*record, n)
	for i := 0; i < n; i++ {
		recs[i] = &record{id: int64(i), name: fmt.Sprintf("r%d", i)}
		if i > 0 {
			recs[i].next = recs[i-1]
		}
	}
	recSink = recs

	// 通过泛型接口使用 Stack；GC 后在接口与方法值两种路径上派发。
	var st Collection[record] = &Stack[record]{}
	for i := 0; i < n; i++ {
		st.Push(recs[i])
	}
	gcStress()
	ck(st.Len() == n, "stack Len via interface mismatch after GC")

	pop := st.Pop // 方法值绑定具体实现
	p := pop()
	ck(p == recs[n-1], "method value dispatch returned wrong pointer")
	ck(p != nil && p.id == int64(n-1) && p.name == fmt.Sprintf("r%d", n-1), "popped payload corrupted after GC")
	ck(p.next == recs[n-2], "popped record link corrupted")

	rest := drain(st)
	ck(len(rest) == n-1, fmt.Sprintf("stack drain count got=%d want=%d", len(rest), n-1))
	for i, got := range rest {
		want := recs[n-2-i]
		ck(got == want, "stack LIFO pointer identity changed")
		ck(got.id == want.id && got.name == want.name, "stack LIFO payload corrupted")
	}
	ck(st.Len() == 0, "stack not empty after drain")
	ck(st.Pop() == nil, "empty stack Pop should return nil")

	// FIFO 队列，同一泛型接口的不同实现。
	var qu Collection[record] = &Queue[record]{}
	for i := 0; i < n; i++ {
		qu.Push(recs[i])
	}
	gcStress()
	out := drain(qu)
	ck(len(out) == n, "queue drain count mismatch")
	for i, got := range out {
		ck(got == recs[i], "queue FIFO pointer identity changed")
		ck(got.id == int64(i), "queue FIFO payload corrupted")
	}
	ck(qu.Pop() == nil, "empty queue Pop should return nil")

	// 嵌入泛型实例化后的方法提升。
	var dq Collection[record] = &Deque[record]{Stack: &Stack[record]{}}
	for i := 0; i < n/2; i++ {
		dq.Push(recs[i])
	}
	gcStress()
	dqOut := drain(dq)
	ck(len(dqOut) == n/2, "deque promoted-method drain count mismatch")
	for i, got := range dqOut {
		ck(got == recs[n/2-1-i], "deque promoted-method LIFO order corrupted")
	}

	// 不同实例化的接口方法在 GC 后仍可正确派发。
	var ints Collection[int64] = &Stack[int64]{}
	for i := 0; i < 16; i++ {
		v := int64(i * 11)
		ints.Push(&v)
	}
	gcStress()
	for i := 15; i >= 0; i-- {
		v := ints.Pop()
		ck(v != nil && *v == int64(i*11), "Collection[int64] dispatch corrupted")
	}
	ck(ints.Pop() == nil, "Collection[int64] empty Pop not nil")

	finish("l01_generic_methods")
}
