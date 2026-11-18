// L07: atomic.Pointer 无锁队列。Michael-Scott 风格 head/tail 原子指针 + 不可变 payload，
// 顺序/并发入队出队、遍历、GC 后存活与唯一性全部校验。
package main

import (
	"fmt"
	"os"
	"runtime"
	"strconv"
	"sync"
	"sync/atomic"
)

var (
	failed int32
	gcSink [][]byte
	seed   int64
)

func ck(ok bool, msg string) {
	if !ok {
		atomic.AddInt32(&failed, 1)
		fmt.Printf("FAIL: %s (seed=%d)\n", msg, seed)
	}
}

func gcStress() {
	for r := 0; r < 2; r++ {
		for i := 0; i < 12; i++ {
			b := make([]byte, 6144+i*29)
			b[0] = byte(i)
			b[len(b)-1] = byte(220 - i)
			gcSink = append(gcSink[:0], b)
		}
		gcSink = gcSink[:0]
		runtime.GC()
	}
}

func finish(name string) {
	if n := atomic.LoadInt32(&failed); n != 0 {
		fmt.Printf("FAILED %s (%d failed checks)\n", name, n)
		os.Exit(1)
	}
	fmt.Println("ALL PASS " + name)
}

func tortureRounds(def int) int {
	if s := os.Getenv("TORTURE_ROUNDS"); s != "" {
		if v, err := strconv.Atoi(s); err == nil && v > 0 {
			if v > 16 {
				v = 16
			}
			return v
		}
	}
	return def
}

type qLeaf struct {
	v   int64
	tag string
}

type qPayload struct {
	seq  int64
	tag  string
	data []byte
	leaf *qLeaf
}

type qNode struct {
	p    *qPayload
	next atomic.Pointer[qNode]
}

type qQueue struct {
	head atomic.Pointer[qNode]
	tail atomic.Pointer[qNode]
}

func newQ() *qQueue {
	d := &qNode{}
	q := &qQueue{}
	q.head.Store(d)
	q.tail.Store(d)
	return q
}

func (q *qQueue) enqueue(p *qPayload) {
	n := &qNode{p: p}
	for {
		tail := q.tail.Load()
		next := tail.next.Load()
		if next == nil {
			if tail.next.CompareAndSwap(nil, n) {
				q.tail.CompareAndSwap(tail, n)
				return
			}
			continue
		}
		q.tail.CompareAndSwap(tail, next)
	}
}

func (q *qQueue) dequeue() *qPayload {
	for {
		head := q.head.Load()
		tail := q.tail.Load()
		next := head.next.Load()
		if head == tail {
			if next == nil {
				return nil
			}
			q.tail.CompareAndSwap(tail, next)
			continue
		}
		if next == nil {
			continue
		}
		if q.head.CompareAndSwap(head, next) {
			head.next.Store(nil)
			p := next.p
			next.p = nil // 已消费节点转为哨兵，遍历时跳过
			return p
		}
	}
}

func (q *qQueue) traverse(fn func(*qPayload)) int {
	n := q.head.Load()
	count := 0
	for n != nil {
		if n.p != nil {
			count++
			if fn != nil {
				fn(n.p)
			}
		}
		n = n.next.Load()
	}
	return count
}

func newPayload(seq int64, s int64) *qPayload {
	n := 7 + int(seq%5)
	data := make([]byte, n)
	for k := range data {
		data[k] = byte((seq*31 + int64(k*7) + s) & 0xff)
	}
	return &qPayload{
		seq:  seq,
		tag:  "p-" + strconv.FormatInt(seq, 10),
		data: data,
		leaf: &qLeaf{v: seq*29 + s, tag: "l-" + strconv.FormatInt(seq, 10)},
	}
}

func checkPayload(p *qPayload, seq int64, s int64, stage string) {
	ck(p != nil, stage+": nil payload seq="+strconv.FormatInt(seq, 10))
	if p == nil {
		return
	}
	ck(p.seq == seq, stage+": seq got="+strconv.FormatInt(p.seq, 10))
	ck(p.tag == "p-"+strconv.FormatInt(seq, 10), stage+": tag seq="+strconv.FormatInt(seq, 10))
	if p.leaf == nil {
		ck(false, stage+": nil leaf seq="+strconv.FormatInt(seq, 10))
	} else {
		ck(p.leaf.v == seq*29+s && p.leaf.tag == "l-"+strconv.FormatInt(seq, 10), stage+": leaf seq="+strconv.FormatInt(seq, 10))
	}
	ck(len(p.data) == 7+int(seq%5), stage+": data len seq="+strconv.FormatInt(seq, 10))
	for k := 0; k < len(p.data); k++ {
		ck(p.data[k] == byte((seq*31+int64(k*7)+s)&0xff), stage+": data seq="+strconv.FormatInt(seq, 10)+" k="+strconv.Itoa(k))
	}
}

func main() {
	if env := os.Getenv("UAF_SEED"); env != "" {
		if v, err := strconv.ParseInt(env, 10, 64); err == nil {
			seed = v
		}
	}
	if seed < 0 {
		seed = -seed
	}

	q := newQ()

	// 1) 顺序入队 24 个，GC 后按序出队 10 个。
	const first = 24
	for i := int64(0); i < first; i++ {
		q.enqueue(newPayload(i, seed))
	}
	gcStress()
	for i := int64(0); i < 10; i++ {
		p := q.dequeue()
		checkPayload(p, i, seed, "sequential first")
	}
	ck(q.traverse(nil) == first-10, "remaining count after first dequeue")
	gcStress()
	idx := int64(10)
	q.traverse(func(p *qPayload) {
		checkPayload(p, idx, seed, "traverse after GC")
		idx++
	})
	ck(idx == first, "traverse covered remaining nodes")

	// 2) 再入队 16 个，遍历顺序必须是 10..39。
	const second = 16
	for i := int64(0); i < second; i++ {
		q.enqueue(newPayload(first+i, seed))
	}
	gcStress()
	idx = 10
	q.traverse(func(p *qPayload) {
		checkPayload(p, idx, seed, "traverse second batch")
		idx++
	})
	ck(idx == first+second, "traverse second batch count")

	// 3) 全部出队，顺序 10..39。
	for i := int64(10); i < first+second; i++ {
		p := q.dequeue()
		checkPayload(p, i, seed, "drain")
	}
	ck(q.dequeue() == nil, "queue empty")
	ck(q.traverse(nil) == 0, "traverse empty queue")
	gcStress()
	ck(q.dequeue() == nil, "queue still empty after GC")

	// 4) 并发生产者 + 单消费者；payload 只经 atomic 队列可达。
	rounds := tortureRounds(2)
	per := (96 + int(seed%5)*16) * rounds
	const producers = 4
	total := int64(producers * per)
	var wg sync.WaitGroup
	wg.Add(producers)
	for g := 0; g < producers; g++ {
		go func(g int) {
			defer wg.Done()
			base := int64(g * per)
			for i := 0; i < per; i++ {
				seq := base + int64(i) + 1
				q.enqueue(newPayload(seq, seed))
			}
		}(g)
	}
	seen := make(map[int64]bool, total)
	var got int64
	for got < total {
		p := q.dequeue()
		if p == nil {
			runtime.Gosched()
			continue
		}
		ck(!seen[p.seq], "duplicate seq "+strconv.FormatInt(p.seq, 10))
		seen[p.seq] = true
		checkPayload(p, p.seq, seed, "concurrent")
		got++
		if got%64 == 0 {
			runtime.GC()
		}
	}
	wg.Wait()
	ck(len(seen) == int(total), "all concurrent payloads unique")
	ck(q.dequeue() == nil, "queue empty after concurrent")
	gcStress()
	ck(len(seen) == int(total) && got == total, "seen count after GC")

	finish("l07_atomic_pointer_queue")
}
