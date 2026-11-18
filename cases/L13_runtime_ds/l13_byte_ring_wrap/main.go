// L13: 字节环形缓冲：写满覆盖最旧、读写回绕、两段快照与参考 deque 逐步对照。
package main

import (
	"bytes"
	"fmt"
	"math/rand"
	"os"
	"runtime"
	"strconv"
	"sync/atomic"
)

type byteRing struct {
	buf   []byte
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

func newByteRing(capacity int) *byteRing {
	if capacity <= 0 {
		capacity = 1
	}
	return &byteRing{buf: make([]byte, capacity)}
}

func (r *byteRing) capacity() int { return len(r.buf) }

func (r *byteRing) write(p []byte) int {
	orig := len(p)
	if orig == 0 {
		return 0
	}
	if orig >= len(r.buf) {
		copy(r.buf, p[orig-len(r.buf):])
		r.head = 0
		r.count = len(r.buf)
		return orig
	}
	if overflow := r.count + orig - len(r.buf); overflow > 0 {
		r.head = (r.head + overflow) % len(r.buf)
		r.count -= overflow
	}
	pos := (r.head + r.count) % len(r.buf)
	n := copy(r.buf[pos:], p)
	if n < len(p) {
		copy(r.buf, p[n:])
	}
	r.count += orig
	return orig
}

func (r *byteRing) snapshot() []byte {
	out := make([]byte, r.count)
	first := len(r.buf) - r.head
	if first > r.count {
		first = r.count
	}
	copy(out, r.buf[r.head:r.head+first])
	copy(out[first:], r.buf[:r.count-first])
	return out
}

func (r *byteRing) consume(n int) int {
	if n < 0 {
		n = 0
	}
	if n > r.count {
		n = r.count
	}
	r.head = (r.head + n) % len(r.buf)
	r.count -= n
	return n
}

// refRing 逻辑模型：超容量时保留最后 capacity 字节。
type refRing struct {
	capacity int
	data     []byte
}

func (r *refRing) write(p []byte) {
	r.data = append(r.data, p...)
	if len(r.data) > r.capacity {
		r.data = append([]byte(nil), r.data[len(r.data)-r.capacity:]...)
	}
}

func (r *refRing) consume(n int) int {
	if n < 0 {
		n = 0
	}
	if n > len(r.data) {
		n = len(r.data)
	}
	r.data = r.data[n:]
	return n
}

func patternChunk(n int, tag byte, seed int64) []byte {
	p := make([]byte, n)
	for i := range p {
		p[i] = byte(int(tag)*53 + i*17 + int(seed&0xff))
	}
	return p
}

func main() {
	curSeed = envSeed()

	// 1) 固定边界：跨尾部回绕、满窗覆盖、超容量写入、consume 边界。
	r16 := newByteRing(16)
	var ref refRing
	ref.capacity = 16
	p := patternChunk(10, 1, curSeed)
	r16.write(p)
	ref.write(p)
	ck(bytes.Equal(r16.snapshot(), ref.data), "ring fixed: initial snapshot")
	gcStress()
	ck(bytes.Equal(r16.snapshot(), ref.data), "ring fixed: snapshot after GC")
	ck(r16.consume(5) == 5, "ring fixed: consume 5")
	ref.consume(5)
	p = patternChunk(8, 2, curSeed)
	r16.write(p)
	ref.write(p)
	ck(bytes.Equal(r16.snapshot(), ref.data), "ring fixed: wrap write")
	fill := patternChunk(16, 3, curSeed)
	r16.write(fill)
	ref.write(fill)
	ck(bytes.Equal(r16.snapshot(), ref.data), "ring fixed: exact capacity write")
	p = patternChunk(3, 4, curSeed)
	r16.write(p)
	ref.write(p)
	ck(bytes.Equal(r16.snapshot(), ref.data), "ring fixed: overwrite oldest")
	huge := patternChunk(20, 5, curSeed)
	r16.write(huge)
	ref.write(huge)
	ck(bytes.Equal(r16.snapshot(), ref.data), "ring fixed: oversized write keeps tail")
	ck(r16.consume(0) == 0, "ring fixed: consume 0")
	ck(r16.consume(99) == 16, "ring fixed: consume clamp")
	ref.consume(99)
	ck(len(r16.snapshot()) == 0 && len(ref.data) == 0, "ring fixed: not empty after drain")
	r16.write([]byte("abc"))
	ck(string(r16.snapshot()) == "abc", "ring fixed: reuse after drain")
	snap := r16.snapshot()
	snap[0] = 'Z'
	ck(string(r16.snapshot()) == "abc", "ring fixed: snapshot aliases ring")

	// 2) 随机读写：随机 chunk（含超过容量）、随机消费、随机快照。
	const capacity = 37
	rr := newByteRing(capacity)
	ref2 := refRing{capacity: capacity}
	r := rand.New(rand.NewSource(curSeed))
	for op := 0; op < 4000; op++ {
		switch r.Intn(5) {
		case 0, 1:
			n := r.Intn(capacity*2 + 4)
			p := patternChunk(n, byte(op), curSeed+int64(op))
			if got := rr.write(p); got != n {
				ckSeed(false, fmt.Sprintf("ring random: write returned %d want %d op=%d", got, n, op))
			}
			ref2.write(p)
		case 2, 3:
			n := r.Intn(rr.count + 4)
			got := rr.consume(n)
			want := ref2.consume(n)
			ckSeed(got == want, fmt.Sprintf("ring random: consume mismatch op=%d", op))
		case 4:
			got := rr.snapshot()
			ckSeed(bytes.Equal(got, ref2.data), fmt.Sprintf("ring random: snapshot mismatch op=%d", op))
		}
		if op%13 == 12 {
			ckSeed(bytes.Equal(rr.snapshot(), ref2.data), fmt.Sprintf("ring random: checkpoint mismatch op=%d", op))
		}
		if op%211 == 210 {
			gcStress()
			ckSeed(bytes.Equal(rr.snapshot(), ref2.data), fmt.Sprintf("ring random: snapshot after GC op=%d", op))
		}
		if op%500 == 499 {
			got := rr.consume(rr.count + 5)
			want := ref2.consume(len(ref2.data) + 5)
			ckSeed(got == want, fmt.Sprintf("ring random: periodic drain mismatch op=%d", op))
			ckSeed(len(rr.snapshot()) == 0, fmt.Sprintf("ring random: ring not empty after drain op=%d", op))
		}
	}
	// 3) 收尾：写满、GC、消费一半后再写回绕。
	full := patternChunk(capacity, 0x77, curSeed)
	rr.write(full)
	ref2.write(full)
	gcStress()
	ckSeed(bytes.Equal(rr.snapshot(), ref2.data), "ring random: final snapshot after GC")
	rr.consume(capacity / 2)
	ref2.consume(capacity / 2)
	tail := patternChunk(capacity, 0x33, curSeed+1)
	rr.write(tail)
	ref2.write(tail)
	ckSeed(bytes.Equal(rr.snapshot(), ref2.data), "ring random: wrap tail snapshot")
	ckSeed(rr.head >= 0 && rr.head < rr.capacity() && rr.count >= 0 && rr.count <= capacity, "ring random: head/count range")

	runtime.KeepAlive(r16)
	runtime.KeepAlive(rr)
	runtime.KeepAlive(ref)
	runtime.KeepAlive(ref2)
	finish("l13_byte_ring_wrap")
}
