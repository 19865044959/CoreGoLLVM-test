// L05: sync.Pool 中存放含 slice 的结构体（page.buf + []span，span 内含 *tag）；
// 每轮 Reset 后重建 span 视图，持有期间显式 GC，校验内容/指针，最后归还并跨 GC 复用。
package main

import (
	"fmt"
	"os"
	"runtime"
	"strconv"
	"sync"
	"sync/atomic"
)

const poolRounds = 500

var (
	failed int32
	gcSink [][]byte
)

type tag struct {
	id   int
	name string
}

type span struct {
	off int
	n   int
	t   *tag
}

type page struct {
	owner *tag
	buf   []byte
	spans []span
}

var pagePool = sync.Pool{New: func() interface{} {
	return &page{
		owner: &tag{id: -1, name: "new"},
		buf:   make([]byte, 0, 4096),
		spans: make([]span, 0, 32),
	}
}}

func ck(ok bool, msg string) {
	if !ok {
		atomic.AddInt32(&failed, 1)
		fmt.Println("FAIL:", msg)
	}
}

func ckf(ok bool, format string, args ...interface{}) {
	if !ok {
		atomic.AddInt32(&failed, 1)
		fmt.Printf("FAIL: "+format+"\n", args...)
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

func loadSeed() uint64 {
	s := os.Getenv("UAF_SEED")
	if s == "" {
		return 1
	}
	n, err := strconv.ParseUint(s, 10, 64)
	if err != nil {
		return 1
	}
	return n
}

func finish(name string, seed uint64) {
	if n := atomic.LoadInt32(&failed); n != 0 {
		fmt.Printf("FAILED %s (%d failed checks, seed=%d)\n", name, n, seed)
		os.Exit(1)
	}
	fmt.Println("ALL PASS " + name)
}

func spanByte(round, k, i int, seed uint64) byte {
	return byte((uint64(round)*31 + uint64(k)*17 + uint64(i)*7 + seed*13 + 5) % 251)
}

func verifyPage(p *page, round int, seed uint64) {
	if p.owner == nil || p.owner.id != round || p.owner.name != fmt.Sprintf("own-%04d", round) {
		ckf(false, "round=%d page owner corrupted", round)
		return
	}
	wantSpans := 8 + (round*7+int(seed))%17
	if len(p.spans) != wantSpans {
		ckf(false, "round=%d spans=%d want=%d", round, len(p.spans), wantSpans)
		return
	}
	prevEnd := 0
	for k, s := range p.spans {
		if s.t == nil {
			ckf(false, "round=%d span=%d nil tag", round, k)
			return
		}
		wid := round*100 + k
		if s.t.id != wid || s.t.name != fmt.Sprintf("t-%05d", wid) {
			ckf(false, "round=%d span=%d tag corrupted", round, k)
			return
		}
		if s.off != prevEnd || s.n <= 0 || s.off+s.n > len(p.buf) {
			ckf(false, "round=%d span=%d range off=%d n=%d len=%d", round, k, s.off, s.n, len(p.buf))
			return
		}
		for i := 0; i < s.n; i++ {
			if p.buf[s.off+i] != spanByte(round, k, i, seed) {
				ckf(false, "round=%d span=%d byte=%d mismatch", round, k, i)
				return
			}
		}
		prevEnd = s.off + s.n
	}
	if prevEnd != len(p.buf) {
		ckf(false, "round=%d buf len=%d span end=%d", round, len(p.buf), prevEnd)
	}
}

func main() {
	seed := loadSeed()

	for round := 0; round < poolRounds; round++ {
		p := pagePool.Get().(*page)
		if p.owner == nil || len(p.spans) != 0 {
			ckf(false, "round=%d pooled page state ownerNil=%v spans=%d", round, p.owner == nil, len(p.spans))
		}
		p.owner = &tag{id: round, name: fmt.Sprintf("own-%04d", round)}
		p.buf = p.buf[:0]
		p.spans = p.spans[:0]

		n := 8 + (round*7+int(seed))%17
		for k := 0; k < n; k++ {
			wid := round*100 + k
			sz := 4 + (round+k)%37
			off := len(p.buf)
			for i := 0; i < sz; i++ {
				p.buf = append(p.buf, spanByte(round, k, i, seed))
			}
			p.spans = append(p.spans, span{off: off, n: sz, t: &tag{id: wid, name: fmt.Sprintf("t-%05d", wid)}})
		}

		// page 仍被局部变量持有：GC 不得回收 buf 底层数组、spans 数组或 *tag。
		if round%25 == 0 {
			gcStress()
		}
		verifyPage(p, round, seed)

		// Reset 语义后归还；GC 可能清空池，下次 Get 走 New 或旧对象都必须满足契约。
		p.buf = p.buf[:0]
		p.spans = p.spans[:0]
		pagePool.Put(p)
		if round%100 == 99 {
			runtime.GC()
		}
	}

	// 池被 GC 清空后连续 Get：无论新旧对象，owner 非 nil 且已归还的 slice 长度归零。
	gcStress()
	for k := 0; k < 40; k++ {
		p := pagePool.Get().(*page)
		if p.owner == nil {
			ckf(false, "post-GC page owner nil k=%d", k)
		}
		if len(p.spans) != 0 || len(p.buf) != 0 {
			ckf(false, "post-GC page not reset k=%d bufLen=%d spans=%d", k, len(p.buf), len(p.spans))
		}
		p.owner = &tag{id: 10000 + k, name: fmt.Sprintf("own-%04d", 10000+k)}
		p.buf = append(p.buf, byte(k), byte(k+1), byte(k+2))
		p.spans = append(p.spans, span{off: 0, n: 3, t: p.owner})
		if k%8 == 0 {
			runtime.GC()
		}
		if len(p.spans) != 1 || p.spans[0].t != p.owner || len(p.buf) != 3 || p.buf[2] != byte(k+2) {
			ckf(false, "post-GC page reuse k=%d corrupted", k)
		}
		p.buf = p.buf[:0]
		p.spans = p.spans[:0]
		pagePool.Put(p)
	}

	finish("l05_pool_struct_slice", seed)
}
