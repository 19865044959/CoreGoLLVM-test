// L01: 泛型环形队列 + 双端栈：扩容回绕、Pop/PopBottom、RangeDelete 回调，跨 GC 校验指针身份。
package main

import (
	"fmt"
	"os"
	"runtime"
	"strconv"
	"sync/atomic"
)

type payload struct {
	id   int64
	name string
	blob []byte
}

// Queue 是 kitex NewQueue[T] 的环形缓冲实现：Add/Get FIFO，扩容时保持顺序。
type Queue[T any] struct {
	buf  []*T
	head int
	size int
}

func NewQueue[T any](capHint int) *Queue[T] {
	if capHint < 1 {
		capHint = 1
	}
	return &Queue[T]{buf: make([]*T, capHint)}
}

func (q *Queue[T]) grow() {
	nb := make([]*T, len(q.buf)*2)
	for i := 0; i < q.size; i++ {
		nb[i] = q.buf[(q.head+i)%len(q.buf)]
	}
	q.buf = nb
	q.head = 0
}

func (q *Queue[T]) Add(p *T) {
	if q.size == len(q.buf) {
		q.grow()
	}
	q.buf[(q.head+q.size)%len(q.buf)] = p
	q.size++
}

func (q *Queue[T]) Get() (*T, bool) {
	if q.size == 0 {
		return nil, false
	}
	p := q.buf[q.head]
	q.buf[q.head] = nil
	q.head = (q.head + 1) % len(q.buf)
	q.size--
	return p, true
}

func (q *Queue[T]) Size() int { return q.size }

// Stack 是 kitex NewStack[T] 的双端栈：Pop 取栈顶，PopBottom 取栈底。
type Stack[T any] struct {
	items []*T
}

func (s *Stack[T]) Push(p *T) { s.items = append(s.items, p) }

func (s *Stack[T]) Pop() (*T, bool) {
	if len(s.items) == 0 {
		return nil, false
	}
	p := s.items[len(s.items)-1]
	s.items = s.items[:len(s.items)-1]
	return p, true
}

func (s *Stack[T]) PopBottom() (*T, bool) {
	if len(s.items) == 0 {
		return nil, false
	}
	p := s.items[0]
	copy(s.items, s.items[1:])
	s.items = s.items[:len(s.items)-1]
	return p, true
}

func (s *Stack[T]) Size() int { return len(s.items) }

// RangeDelete 模拟 kitex stack RangeDelete：回调返回 (删除当前, 继续遍历)。
func (s *Stack[T]) RangeDelete(fn func(*T) (del bool, cont bool)) int {
	kept := s.items[:0]
	removed := 0
	for i, p := range s.items {
		del, cont := fn(p)
		if del {
			removed++
		} else {
			kept = append(kept, p)
		}
		if !cont {
			kept = append(kept, s.items[i+1:]...)
			break
		}
	}
	s.items = kept
	return removed
}

var (
	failed      int32
	gcSink      [][]byte
	payloadSink []*payload
	uafSeed     int64 = 1
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
	n := 240 + int(uafSeed%16)
	n &^= 1 // 保证偶数

	orig := make([]*payload, n)
	for i := 0; i < n; i++ {
		p := &payload{id: int64(i), name: fmt.Sprintf("p%d", i), blob: make([]byte, 8+i%5)}
		p.blob[0] = byte(i)
		p.blob[len(p.blob)-1] = byte(i >> 1)
		orig[i] = p
	}
	payloadSink = orig

	// ---- 队列：增长中途 GC，出队顺序与指针身份必须保持 ----
	q := NewQueue[payload](8)
	for i := 0; i < n; i++ {
		q.Add(orig[i])
		if i == n/2 {
			gcStress() // 扩容/回绕过程中触发 GC
		}
	}
	gcStress()
	ck(q.Size() == n, fmt.Sprintf("queue size after enqueue got=%d want=%d", q.Size(), n))
	for i := 0; i < n; i++ {
		p, ok := q.Get()
		ck(ok && p == orig[i], fmt.Sprintf("queue FIFO pointer mismatch at %d", i))
		if p == nil {
			continue
		}
		ck(p.id == int64(i) && p.name == fmt.Sprintf("p%d", i), "queue payload corrupted")
		ck(len(p.blob) == 8+i%5 && p.blob[0] == byte(i), "queue blob corrupted")
	}
	ck(q.Size() == 0, "queue not empty after draining")
	_, ok := q.Get()
	ck(!ok, "empty queue returned a value")

	// GC 后再入队，触发第二轮扩容；新旧元素都要跨 GC 存活。
	extra := make([]*payload, 17)
	for i := range extra {
		extra[i] = &payload{id: int64(1000 + i), name: fmt.Sprintf("x%d", i), blob: []byte{byte(i), 0x5a}}
	}
	payloadSink = append(payloadSink, extra...)
	for i := range extra {
		q.Add(extra[i])
	}
	gcStress()
	for i := range extra {
		p, ok := q.Get()
		ck(ok && p == extra[i], "post-GC enqueue pointer identity changed")
		ck(p != nil && p.name == fmt.Sprintf("x%d", i), "post-GC enqueue payload corrupted")
	}

	// ---- 栈：Pop 逆序、PopBottom 正序，均跨 GC ----
	s := &Stack[payload]{}
	for i := 0; i < n; i++ {
		s.Push(orig[i])
	}
	gcStress()
	for i := n - 1; i >= 0; i-- {
		p, ok := s.Pop()
		ck(ok && p == orig[i], fmt.Sprintf("stack Pop pointer mismatch at %d", i))
		if p != nil {
			ck(p.id == int64(i) && p.name == fmt.Sprintf("p%d", i), "stack Pop payload corrupted")
		}
	}
	ck(s.Size() == 0, "stack not empty after popping all")

	s2 := &Stack[payload]{}
	for i := 0; i < n; i++ {
		s2.Push(orig[i])
	}
	gcStress()
	for i := 0; i < 3; i++ {
		p, ok := s2.PopBottom()
		ck(ok && p == orig[i], fmt.Sprintf("PopBottom mismatch at %d", i))
	}

	// ---- RangeDelete：删除偶数，剩余全奇数，GC 后复查 ----
	m := n
	nums := make([]*int64, m)
	sr := &Stack[int64]{}
	for i := 0; i < m; i++ {
		v := int64(i + 1)
		nums[i] = &v
		sr.Push(nums[i])
	}
	gcStress()
	removed := sr.RangeDelete(func(p *int64) (bool, bool) {
		return *p%2 == 0, true
	})
	ck(removed == m/2, fmt.Sprintf("RangeDelete removed got=%d want=%d", removed, m/2))
	ck(sr.Size() == m/2, fmt.Sprintf("RangeDelete size got=%d want=%d", sr.Size(), m/2))
	gcStress()
	var sum int64
	for i := 0; i < sr.Size(); i++ {
		p := sr.items[i]
		ck(p != nil, "deleted stack element left a nil hole")
		if p == nil {
			continue
		}
		ck(*p%2 == 1, "even element survived RangeDelete")
		sum += *p
	}
	var wantSum int64
	for i := 1; i <= m; i += 2 {
		wantSum += int64(i)
	}
	ck(sum == wantSum, fmt.Sprintf("RangeDelete residual sum got=%d want=%d", sum, wantSum))

	// 第二次实例化 Queue[int64]：确认泛型实例间的数据隔离。
	qn := NewQueue[int64](4)
	vals := make([]*int64, 9)
	for i := range vals {
		v := int64(i*11 + 3)
		vals[i] = &v
		qn.Add(vals[i])
	}
	gcStress()
	for i := range vals {
		p, ok := qn.Get()
		ck(ok && p == vals[i] && *p == int64(i*11+3), "Queue[int64] instance corrupted")
	}

	finish("l01_generic_queue_stack")
}
