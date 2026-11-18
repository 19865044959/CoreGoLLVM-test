// L08: 手写 amd64 SSE2 求字节缓冲的无符号最小值/最大值（PMINUB/PMAXUB），
// 与纯 Go 遍历对照；覆盖 16 字节块边界、极值模式、子切片与 GC。
package main

import (
	"fmt"
	"os"
	"runtime"
	"sync/atomic"
)

var (
	failed int32
	gcSink [][]byte
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
		fmt.Printf("FAILED %s (%d failed checks)\n", name, n)
		os.Exit(1)
	}
	fmt.Println("ALL PASS " + name)
}

func minmaxU8Asm(p *byte, n int) (minV, maxV int)

func mkSeq(n, seed int) []byte {
	s := make([]byte, n)
	for i := range s {
		s[i] = byte((i*37 + seed*13) & 0xff)
	}
	return s
}

func mkConst(n int, v byte) []byte {
	s := make([]byte, n)
	for i := range s {
		s[i] = v
	}
	return s
}

func mkAlt(n, seed int) []byte {
	s := make([]byte, n)
	for i := range s {
		if (i+seed)%2 == 0 {
			s[i] = 0xff
		}
	}
	return s
}

func mkSparse(n, seed int) []byte {
	s := mkSeq(n, seed)
	for i := range s {
		if i%17 != 0 {
			s[i] = 0x7f
		}
	}
	return s
}

func minmaxGo(b []byte) (int, int) {
	if len(b) == 0 {
		return 0, 0
	}
	mn, mx := int(b[0]), int(b[0])
	for _, v := range b[1:] {
		if int(v) < mn {
			mn = int(v)
		}
		if int(v) > mx {
			mx = int(v)
		}
	}
	return mn, mx
}

func ptr(b []byte) *byte {
	if len(b) == 0 {
		return nil
	}
	return &b[0]
}

func check(b []byte, label string) {
	wantMin, wantMax := minmaxGo(b)
	gotMin, gotMax := minmaxU8Asm(ptr(b), len(b))
	ck(gotMin == wantMin && gotMax == wantMax,
		fmt.Sprintf("%s n=%d got=(%d,%d) want=(%d,%d)", label, len(b), gotMin, gotMax, wantMin, wantMax))

	gcStress()
	gotMin, gotMax = minmaxU8Asm(ptr(b), len(b))
	ck(gotMin == wantMin && gotMax == wantMax, fmt.Sprintf("%s after GC n=%d", label, len(b)))

	if len(b) > 4 {
		subMin, subMax := minmaxU8Asm(&b[1], len(b)-2)
		wMin, wMax := minmaxGo(b[1 : len(b)-1])
		ck(subMin == wMin && subMax == wMax, fmt.Sprintf("%s sub-slice n=%d", label, len(b)))
	}
	runtime.KeepAlive(b)
}

func main() {
	for si, n := range []int{0, 1, 2, 15, 16, 17, 31, 32, 33, 64, 255, 1000} {
		check(mkSeq(n, si+1), "seq")
		check(mkConst(n, 0x00), "zero")
		check(mkConst(n, 0xff), "ff")
		check(mkAlt(n, si), "alt")
		check(mkSparse(n, si+3), "sparse")
	}
	ck(func() bool { mn, mx := minmaxU8Asm(nil, 0); return mn == 0 && mx == 0 }(), "nil/0")
	finish("l08_asm_minmax_u8")
}
