// L13: bytes.Buffer Reset 复用：Grow/Truncate、读写交错压缩、ReadFrom/WriteTo 与跨 GC 内容。
package main

import (
	"bytes"
	"fmt"
	"io"
	"math/rand"
	"os"
	"runtime"
	"strconv"
	"strings"
	"sync/atomic"
)

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

func pattern(n int, tag byte, seed int64) []byte {
	p := make([]byte, n)
	for i := range p {
		p[i] = byte(int(tag)*37 + i*11 + int(seed&0x3f))
	}
	return p
}

func digest(p []byte) uint64 {
	var s uint64
	for _, c := range p {
		s = s*131 + uint64(c)
	}
	return s
}

type digestWriter struct {
	n   int
	sum uint64
}

func (w *digestWriter) Write(p []byte) (int, error) {
	for _, c := range p {
		w.sum = w.sum*131 + uint64(c)
	}
	w.n += len(p)
	return len(p), nil
}

func main() {
	curSeed = envSeed()
	r := rand.New(rand.NewSource(curSeed))

	// 1) Reset 清空长度，但旧快照内容不受后续写入影响。
	var b bytes.Buffer
	var want []byte
	for i := 0; i < 40; i++ {
		p := pattern(1+r.Intn(5000), byte(i), curSeed)
		if n, err := b.Write(p); err != nil || n != len(p) {
			ck(false, "buffer: initial write")
		}
		want = append(want, p...)
	}
	ck(b.Len() == len(want), "buffer: initial len")
	ck(bytes.Equal(b.Bytes(), want), "buffer: initial content")
	snap := append([]byte(nil), b.Bytes()...)
	gcStress()
	ck(bytes.Equal(b.Bytes(), want), "buffer: content after GC")
	b.Reset()
	ck(b.Len() == 0 && len(b.Bytes()) == 0, "buffer: Reset len")
	ck(bytes.Equal(snap, want), "buffer: snapshot mutated by Reset")
	p2 := pattern(7000, 0x5a, curSeed)
	b.Write(p2)
	ck(bytes.Equal(b.Bytes(), p2), "buffer: content after Reset reuse")
	gcStress()
	ck(bytes.Equal(b.Bytes(), p2), "buffer: content after Reset+GC")
	ck(bytes.Equal(snap, want), "buffer: snapshot mutated by reuse")

	// 2) 同一 Buffer 反复 Reset/重写：容量不下降，内容每次精确重置。
	var reuse bytes.Buffer
	lastCap := 0
	for cycle := 0; cycle < 40; cycle++ {
		reuse.Reset()
		if reuse.Len() != 0 {
			ck(false, "buffer: reuse Len not zero")
			break
		}
		var ref []byte
		for k := 0; k < 3+cycle%4; k++ {
			p := pattern(1+(cycle*37+k*11)%3000, byte(cycle*3+k), curSeed+int64(cycle))
			reuse.Write(p)
			ref = append(ref, p...)
		}
		if reuse.Len() != len(ref) || !bytes.Equal(reuse.Bytes(), ref) {
			ckSeed(false, fmt.Sprintf("buffer: reuse content mismatch at cycle %d", cycle))
			break
		}
		if reuse.Cap() < reuse.Len() {
			ck(false, "buffer: reuse cap < len")
			break
		}
		if reuse.Cap() < lastCap {
			ck(false, "buffer: reuse cap decreased")
			break
		}
		lastCap = reuse.Cap()
		if cycle%10 == 9 {
			gcStress()
			if !bytes.Equal(reuse.Bytes(), ref) {
				ckSeed(false, fmt.Sprintf("buffer: reuse content after GC at cycle %d", cycle))
				break
			}
		}
	}

	// 3) Grow 预留容量、Truncate 截断后重写覆盖。
	var g bytes.Buffer
	g.Grow(1 << 16)
	ck(g.Len() == 0 && g.Cap() >= 1<<16, "buffer: Grow len/cap")
	p := pattern(100000, 0x33, curSeed)
	g.Write(p)
	ck(bytes.Equal(g.Bytes(), p), "buffer: Grow content")
	g.Truncate(40000)
	ck(g.Len() == 40000 && bytes.Equal(g.Bytes(), p[:40000]), "buffer: Truncate content")
	q := pattern(60000, 0x44, curSeed)
	g.Write(q)
	wantG := append(append([]byte(nil), p[:40000]...), q...)
	ck(g.Len() == len(wantG) && bytes.Equal(g.Bytes(), wantG), "buffer: rewrite after Truncate")
	gcStress()
	ck(bytes.Equal(g.Bytes(), wantG), "buffer: Truncate/rewrite after GC")

	// 4) 读写交错：未读数据在后续写入时向前压缩。
	var rw bytes.Buffer
	head := pattern(1000, 1, curSeed)
	rw.Write(head)
	rd := make([]byte, 300)
	if n, err := io.ReadFull(&rw, rd); err != nil || n != 300 {
		ck(false, "buffer: interleaved first read")
	}
	ck(bytes.Equal(rd, head[:300]), "buffer: interleaved first data")
	tailp := pattern(2000, 2, curSeed)
	rw.Write(tailp)
	rest := make([]byte, rw.Len())
	if n, err := io.ReadFull(&rw, rest); err != nil || n != len(rest) {
		ck(false, "buffer: interleaved second read")
	}
	wantRest := append(append([]byte(nil), head[300:]...), tailp...)
	ck(bytes.Equal(rest, wantRest), "buffer: interleaved compacted data")
	ck(rw.Len() == 0, "buffer: interleaved not drained")
	gcStress()

	// 5) ReadFrom 大输入、WriteTo 到独立 writer。
	var rf bytes.Buffer
	src := pattern(50000, 7, curSeed)
	nrf, err := rf.ReadFrom(strings.NewReader(string(src)))
	ck(err == nil && nrf == int64(len(src)), "buffer: ReadFrom count")
	ck(bytes.Equal(rf.Bytes(), src), "buffer: ReadFrom content")
	gcStress()
	ck(bytes.Equal(rf.Bytes(), src), "buffer: ReadFrom content after GC")
	w := &digestWriter{}
	nw, err := rf.WriteTo(w)
	ck(err == nil && nw == int64(len(src)), "buffer: WriteTo count")
	ck(w.n == len(src) && w.sum == digest(src), "buffer: WriteTo digest")
	ck(rf.Len() == 0, "buffer: WriteTo did not drain")

	// 6) 单字节读取 + UnreadByte。
	var ub bytes.Buffer
	ub.WriteString("abcdef")
	c, err := ub.ReadByte()
	ck(err == nil && c == 'a', "buffer: ReadByte")
	ck(ub.UnreadByte() == nil, "buffer: UnreadByte")
	c, err = ub.ReadByte()
	ck(err == nil && c == 'a', "buffer: ReadByte after unread")
	two := make([]byte, 2)
	if n, err := io.ReadFull(&ub, two); err != nil || n != 2 || string(two) != "bc" {
		ck(false, "buffer: two-byte read")
	}

	// 7) 大 Buffer 持有数据跨 GC。
	var big bytes.Buffer
	bigp := pattern(200000, 9, curSeed)
	big.Write(bigp)
	gcStress()
	ck(bytes.Equal(big.Bytes(), bigp), "buffer: big content after GC")
	big.Reset()
	ck(big.Len() == 0, "buffer: big Reset")

	runtime.KeepAlive(b)
	runtime.KeepAlive(reuse)
	runtime.KeepAlive(g)
	runtime.KeepAlive(big)
	finish("l13_bytes_buffer_reset_reuse")
}
