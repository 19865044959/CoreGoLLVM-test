// L01: 泛型定长环形缓冲：2 的幂掩码回绕、满时覆盖最旧元素、At/Slice/Snapshot。
// 与 l01_generic_queue_stack 的动态扩容队列不同，本例验证固定容量的覆盖语义与跨回绕读取。
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

type Ring[T any] struct {
	buf  []*T
	mask int
	head int
	size int
}

func NewRing[T any](hint int) *Ring[T] {
	if hint < 1 {
		hint = 1
	}
	n := 1
	for n < hint {
		n <<= 1
	}
	return &Ring[T]{buf: make([]*T, n), mask: n - 1}
}

// Push 在满时覆盖最旧元素并返回被覆盖的指针。
func (r *Ring[T]) Push(p *T) (*T, bool) {
	if r.size == len(r.buf) {
		old := r.buf[r.head]
		r.buf[r.head] = p
		r.head = (r.head + 1) & r.mask
		return old, true
	}
	r.buf[(r.head+r.size)&r.mask] = p
	r.size++
	return nil, false
}

func (r *Ring[T]) Pop() (*T, bool) {
	if r.size == 0 {
		return nil, false
	}
	p := r.buf[r.head]
	r.buf[r.head] = nil
	r.head = (r.head + 1) & r.mask
	r.size--
	return p, true
}

// At 按"从最旧到最新"的逻辑下标访问。
func (r *Ring[T]) At(i int) (*T, bool) {
	if i < 0 || i >= r.size {
		return nil, false
	}
	return r.buf[(r.head+i)&r.mask], true
}

func (r *Ring[T]) Snapshot() []*T {
	out := make([]*T, r.size)
	for i := 0; i < r.size; i++ {
		out[i] = r.buf[(r.head+i)&r.mask]
	}
	return out
}

// Slice 复制区间到新切片，跨回绕时逐元素拷贝，不与内部数组共享存储。
func (r *Ring[T]) Slice(start, n int) ([]*T, bool) {
	if start < 0 || n < 0 || start+n > r.size {
		return nil, false
	}
	out := make([]*T, n)
	for i := 0; i < n; i++ {
		out[i] = r.buf[(r.head+start+i)&r.mask]
	}
	return out, true
}

func (r *Ring[T]) Len() int     { return r.size }
func (r *Ring[T]) Cap() int     { return len(r.buf) }
func (r *Ring[T]) IsFull() bool { return r.size == len(r.buf) }

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

func main() {
	uafSeed = envSeed()
	rounds := tortureRounds(3)

	for round := 0; round < rounds; round++ {
		ring := NewRing[payload](8 << (round % 4))
		capHint := ring.Cap()
		ops := 180 + int(uafSeed%40)
		hist := make([]*payload, 0, ops)
		for i := 0; i < ops; i++ {
			p := &payload{
				id:   int64(round*10000 + i),
				name: fmt.Sprintf("r%d_%d", round, i),
				blob: make([]byte, 5+i%7),
			}
			p.blob[0] = byte(i)
			p.blob[len(p.blob)-1] = byte(i * 3)
			hist = append(hist, p)
			ev, wrapped := ring.Push(p)
			if len(hist) > capHint {
				want := hist[len(hist)-capHint-1]
				ck(wrapped && ev == want, fmt.Sprintf("round=%d push %d evicted wrong pointer", round, i))
			} else {
				ck(!wrapped, fmt.Sprintf("round=%d push %d wrapped before full", round, i))
			}
			ck(ring.Len() <= ring.Cap(), "ring exceeded capacity")
			if i%48 == 47 {
				gcStress()
			}
		}
		payloadSink = append(payloadSink, hist...)

		wantLen := ops
		if wantLen > capHint {
			wantLen = capHint
		}
		start := len(hist) - wantLen
		ck(ring.Len() == wantLen, fmt.Sprintf("round=%d len got=%d want=%d", round, ring.Len(), wantLen))
		ck(ring.IsFull() == (wantLen == capHint), "IsFull mismatch")

		for i := 0; i < wantLen; i++ {
			p, ok := ring.At(i)
			want := hist[start+i]
			ck(ok && p == want, fmt.Sprintf("round=%d At(%d) pointer mismatch", round, i))
			if p != nil {
				ck(p.id == want.id && p.name == want.name, "At payload corrupted")
				ck(len(p.blob) == len(want.blob) && p.blob[0] == want.blob[0] &&
					p.blob[len(p.blob)-1] == want.blob[len(want.blob)-1], "At blob corrupted")
			}
		}
		_, ok := ring.At(-1)
		ck(!ok, "negative index accepted")
		_, ok = ring.At(wantLen)
		ck(!ok, "out-of-range index accepted")

		snap := ring.Snapshot()
		ck(len(snap) == wantLen, "Snapshot length mismatch")
		for i := range snap {
			ck(snap[i] == hist[start+i], "Snapshot pointer mismatch")
		}

		// 跨回绕 Slice，并验证返回切片不与内部存储共享指针槽。
		mid := wantLen / 3
		sl, ok := ring.Slice(mid, wantLen-mid)
		ck(ok && len(sl) == wantLen-mid, "Slice length mismatch")
		for i := range sl {
			ck(sl[i] == hist[start+mid+i], "Slice pointer mismatch")
		}
		if len(sl) > 0 {
			saved := sl[0]
			sl[0] = hist[0]
			got, _ := ring.At(mid)
			ck(got == saved, "Slice shares backing storage with ring")
			sl[0] = saved
		}
		_, ok = ring.Slice(-1, 1)
		ck(!ok, "Slice accepted negative start")
		_, ok = ring.Slice(0, wantLen+1)
		ck(!ok, "Slice accepted oversized range")

		// 出队顺序必须严格等于逻辑顺序。
		for i := 0; i < wantLen; i++ {
			p, ok := ring.Pop()
			ck(ok && p == hist[start+i], fmt.Sprintf("round=%d Pop %d FIFO mismatch", round, i))
			if p != nil {
				ck(p.name == fmt.Sprintf("r%d_%d", round, start+i), "Pop payload corrupted")
			}
		}
		_, ok = ring.Pop()
		ck(!ok, "empty ring returned a value")
		ck(ring.Len() == 0, "ring not empty after drain")
		gcStress()
		for i := start; i < len(hist); i += 29 {
			ck(hist[i].id == int64(round*10000+i), "hist payload corrupted after GC")
		}
	}

	// 第二次实例化 Ring[string]：固定容量 8，覆盖后仅保留最后 8 个。
	sr := NewRing[string](8)
	vals := make([]*string, 40)
	for i := range vals {
		v := fmt.Sprintf("v%02d", i)
		vals[i] = &v
		sr.Push(vals[i])
	}
	gcStress()
	ck(sr.Len() == 8, fmt.Sprintf("string ring len got=%d want=8", sr.Len()))
	for i := 0; i < 8; i++ {
		p, ok := sr.At(i)
		ck(ok && p == vals[32+i], "string ring pointer mismatch")
		ck(p != nil && *p == fmt.Sprintf("v%02d", 32+i), "string ring value corrupted")
	}
	snap := sr.Snapshot()
	for i := range snap {
		ck(snap[i] == vals[32+i], "string ring snapshot mismatch")
	}

	finish("l01_generic_ringbuf")
}
