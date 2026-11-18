// L01: 泛型对象池：Get/Put/Reset、容量上限与丢弃，sync.GC 后复用不串旧指针。
package main

import (
	"fmt"
	"os"
	"runtime"
	"strconv"
	"sync"
	"sync/atomic"
)

type buffer struct {
	id    int64
	inUse bool
	tag   string
	data  []byte
}

type node struct {
	v    int64
	name string
	next *node
}

// Pool[T] 是泛型对象池：New 构造、Reset 清理、Put 超限丢弃。
type Pool[T any] struct {
	mu      sync.Mutex
	free    []*T
	max     int
	newFn   func() *T
	resetFn func(*T)
	gets    int
	puts    int
	news    int
	discard int
}

func newPool[T any](max int, newFn func() *T, resetFn func(*T)) *Pool[T] {
	return &Pool[T]{max: max, newFn: newFn, resetFn: resetFn}
}

func (p *Pool[T]) Get() *T {
	p.mu.Lock()
	n := len(p.free)
	if n > 0 {
		v := p.free[n-1]
		p.free = p.free[:n-1]
		p.gets++
		p.mu.Unlock()
		return v
	}
	p.gets++
	p.news++
	p.mu.Unlock()
	return p.newFn()
}

func (p *Pool[T]) Put(v *T) {
	if v == nil {
		return
	}
	p.resetFn(v)
	p.mu.Lock()
	p.puts++
	if len(p.free) >= p.max {
		p.discard++
		p.mu.Unlock()
		return
	}
	p.free = append(p.free, v)
	p.mu.Unlock()
}

func (p *Pool[T]) Stats() (gets, puts, news, discard int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.gets, p.puts, p.news, p.discard
}

var (
	failed    int32
	gcSink    [][]byte
	entSink   []*node
	uafSeed   int64 = 1
	nextBufID int64
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

func tortureRounds(def int) int {
	s := os.Getenv("TORTURE_ROUNDS")
	if s == "" {
		return def
	}
	n, err := strconv.Atoi(s)
	if err != nil || n <= 0 {
		return def
	}
	if n > 16 {
		n = 16
	}
	return n
}

func newBuffer() *buffer {
	id := atomic.AddInt64(&nextBufID, 1)
	return &buffer{id: id, data: make([]byte, 0, 256)}
}

func resetBuffer(b *buffer) {
	b.inUse = false
	b.tag = ""
	buf := b.data[:cap(b.data)]
	for i := range buf {
		buf[i] = 0
	}
	b.data = b.data[:0]
}

func main() {
	uafSeed = envSeed()
	rounds := tortureRounds(1)
	cycles := 4*rounds + int(uafSeed%3)

	const batch = 24
	p := newPool[buffer](batch, newBuffer, resetBuffer)
	allocated := make(map[*buffer]bool)

	for cyc := 0; cyc < cycles; cyc++ {
		held := make([]*buffer, batch)
		for i := range held {
			b := p.Get()
			ck(b != nil, "buffer pool returned nil")
			ck(!b.inUse, "pool returned an object still marked in-use")
			ck(b.tag == "", "pool returned an object with stale tag")
			ck(len(b.data) == 0, "pool returned an object with stale data length")
			ck(cap(b.data) >= 256, "pool returned an object with shrunk capacity")
			b.inUse = true
			b.tag = fmt.Sprintf("cycle-%d-%d", cyc, i)
			b.data = append(b.data, byte(i), byte(cyc), 0x5a, 0xa5)
			held[i] = b
			allocated[b] = true
		}
		if cyc%2 == 0 {
			gcStress() // 池外对象与池内空闲对象混合存活
		}
		for i, b := range held {
			ck(b.inUse, "in-use object lost flag after GC")
			ck(len(b.data) == 4 && b.data[0] == byte(i) && b.data[2] == 0x5a, "in-use buffer payload corrupted")
		}
		for _, b := range held {
			p.Put(b)
		}
		ck(len(p.free) <= batch, "pool exceeded capacity")
		gcStress()
		for _, b := range held {
			ck(!b.inUse && b.tag == "" && len(b.data) == 0, "Put did not reset object")
			ck(b.data[:cap(b.data)][0] == 0, "Put did not clear backing bytes")
		}
	}

	gets, puts, news, discard := p.Stats()
	ck(gets == cycles*batch, fmt.Sprintf("pool gets got=%d want=%d", gets, cycles*batch))
	ck(puts == cycles*batch, fmt.Sprintf("pool puts got=%d want=%d", puts, cycles*batch))
	ck(news >= 1 && int64(news) <= int64(cycles*batch), "pool news out of range")
	ck(news <= batch+1, fmt.Sprintf("pool allocated too many objects: %d", news))
	ck(discard == 0, "pool discarded objects while under capacity")
	ck(len(allocated) >= 1, "pool never allocated")

	// 池外再 Get 一批：GC 后每个对象仍是有效内存。
	held := make([]*buffer, batch)
	for i := range held {
		held[i] = p.Get()
		held[i].data = append(held[i].data, 0xEE)
		held[i].inUse = true
	}
	gcStress()
	for i, b := range held {
		ck(len(b.data) == 1 && b.data[0] == 0xEE, "post-GC pool object corrupted")
		if i > 0 {
			ck(b != held[i-1], "pool handed out the same object twice")
		}
	}
	for _, b := range held {
		p.Put(b)
	}

	// 第二次实例化 Pool[node]：Reset 必须断开 next 链，杜绝旧指针串线。
	npool := newPool[node](8, func() *node { return &node{} }, func(n *node) {
		n.v = 0
		n.name = ""
		n.next = nil
	})
	heads := make([]*node, 16)
	for round := 0; round < 2; round++ {
		for i := range heads {
			head := npool.Get()
			ck(head.next == nil, "node pool returned object with stale next pointer")
			cur := head
			for j := 0; j < 6; j++ {
				cur.v = int64(round*1000 + i*10 + j)
				cur.name = fmt.Sprintf("n-%d-%d", i, j)
				cur.next = &node{v: -1, name: "fresh"}
				cur = cur.next
			}
			heads[i] = head
			entSink = append(entSink, head)
		}
		gcStress()
		for i, head := range heads {
			cur := head
			for j := 0; j < 6; j++ {
				ck(cur.v == int64(round*1000+i*10+j), "node chain value corrupted after GC")
				ck(cur.name == fmt.Sprintf("n-%d-%d", i, j), "node chain name corrupted after GC")
				if j < 5 {
					ck(cur.next != nil, "node chain broken after GC")
					cur = cur.next
				}
			}
			ck(cur.next != nil && cur.next.v == -1, "node chain tail corrupted")
		}
		for _, head := range heads {
			npool.Put(head)
		}
		gcStress()
		for _, head := range heads {
			ck(head.next == nil && head.v == 0 && head.name == "", "node pool reset did not clear chain")
		}
	}

	finish("l01_generic_object_pool")
}
