// L08: 手写 amd64 汇编实现 Adler-32 校验和（逐字节累加 + 65521 条件归约），
// 与标准库 hash/adler32 全量对照；覆盖全零/全 0xff/递增/伪随机与 GC。
package main

import (
	"fmt"
	"hash/adler32"
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

func adler32Asm(p *byte, n int) uint32

func mkSeq(n, seed int) []byte {
	s := make([]byte, n)
	for i := range s {
		s[i] = byte((i*23 + seed*19) & 0xff)
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

func mkRand(n, seed int) []byte {
	s := make([]byte, n)
	x := uint32(seed)*2654435761 + 17
	for i := range s {
		x ^= x << 13
		x ^= x >> 17
		x ^= x << 5
		s[i] = byte(x)
	}
	return s
}

func ptr(b []byte) *byte {
	if len(b) == 0 {
		return nil
	}
	return &b[0]
}

func check(b []byte, label string) {
	want := adler32.Checksum(b)
	got := adler32Asm(ptr(b), len(b))
	ck(got == want, fmt.Sprintf("%s n=%d got=%08x want=%08x", label, len(b), got, want))

	gcStress()
	ck(adler32Asm(ptr(b), len(b)) == want, fmt.Sprintf("%s after GC n=%d", label, len(b)))
	ck(adler32.Checksum(b) == want, fmt.Sprintf("%s source after GC n=%d", label, len(b)))

	if len(b) > 4 {
		gotSub := adler32Asm(&b[2], len(b)-3)
		wantSub := adler32.Checksum(b[2 : len(b)-1])
		ck(gotSub == wantSub, fmt.Sprintf("%s sub-slice n=%d", label, len(b)))
	}
	runtime.KeepAlive(b)
}

func main() {
	for si, n := range []int{0, 1, 15, 16, 17, 255, 1024, 5552, 10000, 65537} {
		check(mkSeq(n, si+1), "seq")
		check(mkConst(n, 0x00), "zero")
		check(mkConst(n, 0xff), "ff")
		check(mkRand(n, si+3), "rand")
	}
	ck(adler32Asm(nil, 0) == 1, "adler32 nil/0 == 1")
	finish("l08_asm_checksum")
}
