// L13: 自建 size-class 池的桶容量语义：错误尺寸 Put 不得污染桶，Get 返回期望容量。
package main

import (
	"fmt"
	"math/rand"
	"os"
	"runtime"
	"strconv"
	"sync/atomic"
)

var classes = []int{256, 512, 1024, 2048, 4096}

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

func classFor(n int) int {
	for _, c := range classes {
		if n <= c {
			return c
		}
	}
	return -1
}

func expectedCap(n int) int {
	if c := classFor(n); c >= 0 {
		return c
	}
	return n
}

// sizePool 模拟 pgx iobufpool：按 2 的幂分桶，非桶尺寸的 Put 直接丢弃。
type sizePool struct {
	buckets map[int][][]byte
}

func newSizePool() *sizePool {
	return &sizePool{buckets: make(map[int][][]byte)}
}

func (p *sizePool) Get(n int) []byte {
	if n < 0 {
		return nil
	}
	c := classFor(n)
	if c < 0 {
		return make([]byte, 0, n)
	}
	list := p.buckets[c]
	if len(list) == 0 {
		return make([]byte, 0, c)
	}
	buf := list[len(list)-1]
	p.buckets[c] = list[:len(list)-1]
	return buf[:0]
}

func (p *sizePool) Put(buf []byte) bool {
	c := cap(buf)
	isClass := false
	for _, k := range classes {
		if c == k {
			isClass = true
			break
		}
	}
	if !isClass {
		return false
	}
	p.buckets[c] = append(p.buckets[c], buf[:0])
	return true
}

func (p *sizePool) total() int {
	n := 0
	for _, list := range p.buckets {
		n += len(list)
	}
	return n
}

func (p *sizePool) bucketLen(c int) int {
	return len(p.buckets[c])
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

func main() {
	curSeed = envSeed()

	// 1) Get 的桶选择：最小可容纳尺寸的桶；超过最大桶则精确分配。
	boundary := []struct {
		req  int
		want int
	}{
		{0, 256}, {1, 256}, {255, 256}, {256, 256},
		{257, 512}, {511, 512}, {512, 512},
		{513, 1024}, {1023, 1024}, {1024, 1024},
		{1025, 2048}, {2047, 2048}, {2048, 2048},
		{2049, 4096}, {4095, 4096}, {4096, 4096},
		{4097, 4097}, {8192, 8192},
	}
	p := newSizePool()
	for _, bc := range boundary {
		buf := p.Get(bc.req)
		ckSeed(cap(buf) == bc.want, fmt.Sprintf("Get(%d) cap: got %d want %d", bc.req, cap(buf), bc.want))
		ckSeed(len(buf) == 0, fmt.Sprintf("Get(%d) len must be 0", bc.req))
		full := buf[:cap(buf)]
		for i := range full {
			full[i] = byte(bc.req + 1)
		}
		p.Put(buf)
	}
	ckSeed(p.total() == 5, "only bucket-sized buffers retained")

	// 2) 错误尺寸 Put 必须被丢弃，后续 Get 不得拿到这些缓冲。
	p2 := newSizePool()
	bad300 := make([]byte, 0, 300)
	bad300 = bad300[:cap(bad300)]
	for i := range bad300 {
		bad300[i] = 0xEE
	}
	bad100 := make([]byte, 0, 100)
	bad100 = bad100[:cap(bad100)]
	for i := range bad100 {
		bad100[i] = 0xEE
	}
	bad5000 := make([]byte, 0, 5000)
	bad5000 = bad5000[:cap(bad5000)]
	for i := range bad5000 {
		bad5000[i] = 0xEE
	}
	ckSeed(!p2.Put(bad300), "Put cap=300 rejected")
	ckSeed(!p2.Put(bad100), "Put cap=100 rejected")
	ckSeed(!p2.Put(bad5000), "Put cap=5000 rejected")
	ckSeed(!p2.Put(nil), "Put nil rejected")
	ckSeed(p2.total() == 0, "wrong-size Put leaves pool empty")
	b256 := p2.Get(256)
	ckSeed(cap(b256) == 256 && b256[:1][0] == 0, "Get(256) after wrong-size Put returns fresh zeroed bucket")
	b4096 := p2.Get(4096)
	ckSeed(cap(b4096) == 4096 && b4096[:1][0] == 0, "Get(4096) not contaminated by cap=5000 buffer")
	big := p2.Get(5000)
	ckSeed(cap(big) == 5000, "oversize Get exact capacity")
	big = big[:cap(big)]
	big[0] = 0xEE
	ckSeed(!p2.Put(big), "Put oversize rejected")
	big2 := p2.Get(5000)
	ckSeed(cap(big2) == 5000 && big2[:1][0] == 0, "oversize buffer not reused after rejected Put")
	ckSeed(p2.Get(-1) == nil, "Get(-1) returns nil")

	// 3) 正确尺寸 Put 后复用底层数组（不清零，但 len=0）。
	p3 := newSizePool()
	buf := p3.Get(300)
	ckSeed(cap(buf) == 512, "Get(300) uses 512 bucket")
	full := buf[:cap(buf)]
	full[0] = 0xAB
	ckSeed(p3.Put(buf), "Put cap=512 accepted")
	buf2 := p3.Get(300)
	ckSeed(cap(buf2) == 512, "reused buffer cap")
	ckSeed(len(buf2) == 0, "reused buffer len reset")
	ckSeed(buf2[:1][0] == 0xAB, "reused buffer keeps backing bytes")
	ckSeed(p3.total() == 0, "reused buffer removed from bucket")
	other := p3.Get(256)
	ckSeed(cap(other) == 256, "different class gets different bucket")

	// 4) GC 后池内缓冲仍可用。
	for i := 0; i < 5; i++ {
		gcStress()
	}
	got := p2.Get(256)
	ckSeed(cap(got) == 256 && len(got) == 0, "pooled buffer survives GC")
	if cap(got) > 0 {
		got = got[:cap(got)]
		got[0] = 0x11
	}
	p2.Put(got)

	// 5) 随机尺寸 churn + 定期 GC；顺序 Get/Put 下每类桶最多驻留一个缓冲。
	r := rand.New(rand.NewSource(curSeed))
	p4 := newSizePool()
	used := make(map[int]bool)
	accepted := 0
	for i := 0; i < 400; i++ {
		n := r.Intn(6000) - 10
		buf := p4.Get(n)
		if n < 0 {
			ckSeed(buf == nil, "random Get(negative) nil")
			continue
		}
		want := expectedCap(n)
		ckSeed(cap(buf) == want, fmt.Sprintf("random Get(%d) cap: got %d want %d", n, cap(buf), want))
		ckSeed(len(buf) == 0, "random Get len 0")
		if classFor(n) >= 0 {
			used[cap(buf)] = true
		}
		if cap(buf) > 0 {
			buf[:cap(buf)][0] = byte(i)
		}
		if p4.Put(buf) {
			accepted++
		}
		if i%37 == 0 {
			gcStress()
		}
	}
	ckSeed(accepted >= len(classes), "churn accepted bucket-sized puts")
	ckSeed(p4.total() == len(used), fmt.Sprintf("one buffer per touched class: got %d want %d", p4.total(), len(used)))
	gcStress()
	for _, c := range classes {
		for p4.bucketLen(c) > 0 {
			buf := p4.Get(c)
			ckSeed(cap(buf) == c, fmt.Sprintf("drain class %d cap", c))
		}
	}
	ckSeed(p4.total() == 0, "pool drained")

	runtime.KeepAlive(p)
	runtime.KeepAlive(p2)
	runtime.KeepAlive(p3)
	finish("l13_iobuf_capacity_semantics")
}
