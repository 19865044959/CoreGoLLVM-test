// L13: 指针环形缓冲：满窗覆盖、回绕、弹出/扩容/重置前后的指针保活。
package main

import (
	"fmt"
	"os"
	"runtime"
	"strconv"
	"sync/atomic"
)

type node struct {
	id   int
	name string
	pad  [32]byte
}

type ring struct {
	buf   []*node
	head  int
	count int
}

var (
	failed  int32
	gcSink  [][]byte
	curSeed int64
)

func ck(ok bool, msg string) {
	if !ok {
		atomic.AddInt32(&failed, 1)
		fmt.Println("FAIL:", msg)
	}
}

func ckSeed(ok bool, msg string) {
	if !ok {
		atomic.AddInt32(&failed, 1)
		fmt.Printf("FAIL: %s (seed=%d)\n", msg, curSeed)
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
		fmt.Printf("FAILED %s (%d failed checks)\n", name, n)
		os.Exit(1)
	}
	fmt.Println("ALL PASS " + name)
}

func envSeed() int64 {
	s := os.Getenv("UAF_SEED")
	if s == "" {
		return 1
	}
	v, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return 1
	}
	return v
}

func newNode(id int, seed int64) *node {
	n := &node{id: id, name: fmt.Sprintf("n-%d", id)}
	for i := range n.pad {
		n.pad[i] = byte(id*13 + i*5 + int(seed))
	}
	return n
}

func validNode(n *node, id int, seed int64) bool {
	if n == nil || n.id != id || n.name != fmt.Sprintf("n-%d", id) {
		return false
	}
	for i := range n.pad {
		if n.pad[i] != byte(id*13+i*5+int(seed)) {
			return false
		}
	}
	return true
}

func newRing(capacity int) *ring {
	return &ring{buf: make([]*node, capacity)}
}

func (r *ring) at(i int) *node {
	return r.buf[(r.head+i)%len(r.buf)]
}

// add 在满窗时覆盖最旧元素并返回被覆盖的指针。
func (r *ring) add(p *node) *node {
	if r.count < len(r.buf) {
		r.buf[(r.head+r.count)%len(r.buf)] = p
		r.count++
		return nil
	}
	old := r.buf[r.head]
	r.buf[r.head] = p
	r.head = (r.head + 1) % len(r.buf)
	return old
}

func (r *ring) popFront() *node {
	if r.count == 0 {
		return nil
	}
	p := r.buf[r.head]
	r.buf[r.head] = nil
	r.head = (r.head + 1) % len(r.buf)
	r.count--
	return p
}

func (r *ring) grow(capacity int) {
	if capacity <= len(r.buf) {
		return
	}
	nb := make([]*node, capacity)
	for i := 0; i < r.count; i++ {
		nb[i] = r.at(i)
	}
	r.buf = nb
	r.head = 0
}

func (r *ring) reset() {
	for i := range r.buf {
		r.buf[i] = nil
	}
	r.head = 0
	r.count = 0
}

func checkRing(r *ring, firstID int, seed int64, tag string) {
	if r.count < 0 || r.count > len(r.buf) {
		ckSeed(false, tag+": ring count out of range")
		return
	}
	for i := 0; i < r.count; i++ {
		if !validNode(r.at(i), firstID+i, seed) {
			ckSeed(false, tag+": ring element mismatch at "+strconv.Itoa(i))
			return
		}
	}
}

func main() {
	curSeed = envSeed()
	seed := curSeed

	// 容量 16 的环，添加 2000 个元素，GC 穿插验证存活。
	r := newRing(16)
	added := 0
	for added < 2000 {
		p := newNode(added, seed)
		old := r.add(p)
		if added >= 16 {
			if old == nil || old.id != added-16 {
				ckSeed(false, "ring: wrong evicted node")
				break
			}
		} else if old != nil {
			ckSeed(false, "ring: unexpected eviction before full")
			break
		}
		added++
		if added%181 == 0 {
			gcStress()
			if r.count > 0 {
				checkRing(r, added-r.count, seed, "churn")
			}
		}
	}
	gcStress()
	ckSeed(r.count == 16, "ring: count after churn")
	if r.count == 16 {
		checkRing(r, 2000-16, seed, "full")
	}
	// 扩容后逻辑顺序保持不变。
	r.grow(64)
	ckSeed(len(r.buf) == 64 && r.count == 16, "ring: grow shape")
	if r.count == 16 {
		checkRing(r, 2000-16, seed, "after-grow")
	}

	// 弹出 10 个，再弹空，再弹一次返回 nil。
	for i := 0; i < 10; i++ {
		p := r.popFront()
		if !validNode(p, 2000-16+i, seed) {
			ckSeed(false, "ring: popFront order")
			break
		}
	}
	gcStress()
	if r.count != 6 {
		ckSeed(false, "ring: count after pops")
	} else {
		checkRing(r, 2000-6, seed, "after-pops")
	}
	for r.count > 0 {
		r.popFront()
	}
	ckSeed(r.popFront() == nil, "ring: pop on empty")
	gcStress()

	// 扩容后的环继续回绕：容量 64 填入 100 个。
	added = 0
	for added < 100 {
		p := newNode(5000+added, seed+1)
		old := r.add(p)
		if added >= 64 && (old == nil || old.id != 5000+added-64) {
			ckSeed(false, "ring: wrong eviction after grow")
			break
		}
		added++
		if added%37 == 0 {
			gcStress()
		}
	}
	ckSeed(r.count == 64, "ring: count after second churn")
	checkRing(r, 5000+100-64, seed+1, "post-grow-churn")
	gcStress()
	checkRing(r, 5000+100-64, seed+1, "post-grow-churn after GC")

	// 重置后槽位清空，可再次使用。
	r.reset()
	ckSeed(r.count == 0 && r.popFront() == nil, "ring: reset")
	for i := 0; i < 5; i++ {
		r.add(newNode(9000+i, seed+2))
	}
	gcStress()
	checkRing(r, 9000, seed+2, "after-reset")
	for i := 0; i < 5; i++ {
		if !validNode(r.popFront(), 9000+i, seed+2) {
			ckSeed(false, "ring: pop after reset")
			break
		}
	}

	// 容量 1 的环：每次添加都覆盖前一个元素。
	r1 := newRing(1)
	last := -1
	for i := 0; i < 5; i++ {
		old := r1.add(newNode(9100+i, seed+3))
		if old != nil {
			last = old.id
		}
		if i > 0 && last != 9100+i-1 {
			ckSeed(false, "ring1: wrong evicted node")
			break
		}
		gcStress()
		checkRing(r1, 9100+i, seed+3, "cap1")
	}

	runtime.KeepAlive(r)
	runtime.KeepAlive(r1)
	finish("l13_ptr_ring_buffer")
}
