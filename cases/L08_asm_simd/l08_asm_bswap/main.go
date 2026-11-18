// L08: 手写 amd64 汇编字节交换（BSWAPQ/BSWAPL/ROLW）与 uint64 切片原地翻转，
// 与 math/bits.ReverseBytes* 对照；覆盖边界值、定种子随机值与 GC。
package main

import (
	"fmt"
	"math/bits"
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

func bswap64Asm(x uint64) uint64
func bswap32Asm(x uint64) uint64
func bswap16Asm(x uint64) uint64
func bswapBufAsm(p *uint64, n int)

func mkWords(n, seed int) []uint64 {
	s := make([]uint64, n)
	x := uint64(seed)*0x2545f4914f6cdd1d + 7
	for i := range s {
		x ^= x << 13
		x ^= x >> 7
		x ^= x << 17
		s[i] = x
	}
	return s
}

func swapBufGo(s []uint64) {
	for i := range s {
		s[i] = bits.ReverseBytes64(s[i])
	}
}

func main() {
	vals := []uint64{
		0, 1, 2, 0x0102030405060708, 0xffffffffffffffff,
		0x8000000000000000, 0x00000000ffffffff, 0x1122334455667788,
	}
	vals = append(vals, mkWords(300, 9)...)
	for i, x := range vals {
		got := bswap64Asm(x)
		want := bits.ReverseBytes64(x)
		ck(got == want, fmt.Sprintf("bswap64 i=%d got=%016x want=%016x", i, got, want))
		ck(bswap64Asm(got) == x, fmt.Sprintf("bswap64 idempotent i=%d", i))

		x32 := uint32(x)
		g32 := uint32(bswap32Asm(uint64(x32)))
		w32 := bits.ReverseBytes32(x32)
		ck(g32 == w32, fmt.Sprintf("bswap32 i=%d got=%08x want=%08x", i, g32, w32))

		x16 := uint16(x)
		g16 := uint16(bswap16Asm(uint64(x16)))
		w16 := bits.ReverseBytes16(x16)
		ck(g16 == w16, fmt.Sprintf("bswap16 i=%d got=%04x want=%04x", i, g16, w16))
	}

	gcStress()
	ck(bswap64Asm(0x0102030405060708) == 0x0807060504030201, "bswap64 after GC")
	ck(uint32(bswap32Asm(0x01020304)) == 0x04030201, "bswap32 after GC")
	ck(uint16(bswap16Asm(0x0102)) == 0x0201, "bswap16 after GC")

	for si, n := range []int{0, 1, 2, 7, 64, 257} {
		s := mkWords(n, si+1)
		want := append([]uint64(nil), s...)
		swapBufGo(want)
		var p *uint64
		if n > 0 {
			p = &s[0]
		}
		bswapBufAsm(p, n)
		eq := true
		for i := range s {
			if s[i] != want[i] {
				eq = false
				break
			}
		}
		ck(eq, fmt.Sprintf("bswap buffer n=%d", n))

		gcStress()
		eq = true
		for i := range s {
			if s[i] != want[i] {
				eq = false
				break
			}
		}
		ck(eq, fmt.Sprintf("bswap buffer after GC n=%d", n))

		bswapBufAsm(p, n)
		eq = true
		orig := mkWords(n, si+1)
		for i := range s {
			if s[i] != orig[i] {
				eq = false
				break
			}
		}
		ck(eq, fmt.Sprintf("bswap buffer double n=%d", n))

		if n > 4 {
			bswapBufAsm(&s[1], n-2)
			check := mkWords(n, si+1)
			swapBufGo(check[1 : n-1])
			eq = true
			for i := range s {
				if s[i] != check[i] {
					eq = false
					break
				}
			}
			ck(eq, fmt.Sprintf("bswap buffer sub-slice n=%d", n))
		}
		runtime.KeepAlive(s)
	}

	bswapBufAsm(nil, 0)
	finish("l08_asm_bswap")
}
