// L08: 手写 amd64 汇编实现“异或折叠 + 循环左移”的字节流校验。
// 每个字节：acc = ROL64(acc,1) ^ byte；与 Go 参考实现对照，
// 覆盖种子、长度、分块续算、子切片与 GC。
package main

import (
	"bytes"
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

func xorFoldRolAsm(p *byte, n int, seed uint64) uint64

func xorFoldRolGo(b []byte, seed uint64) uint64 {
	acc := seed
	for _, c := range b {
		acc = bits.RotateLeft64(acc, 1) ^ uint64(c)
	}
	return acc
}

func ptr(b []byte) *byte {
	if len(b) == 0 {
		return nil
	}
	return &b[0]
}

func filler(n, seed int) []byte {
	b := make([]byte, n)
	x := uint32(seed)*2654435761 + 23
	for i := range b {
		x = x*1664525 + 1013904223
		b[i] = byte(x >> 24)
	}
	return b
}

func check(b []byte, seed uint64, label string) {
	want := xorFoldRolGo(b, seed)
	got := xorFoldRolAsm(ptr(b), len(b), seed)
	ck(got == want, fmt.Sprintf("%s n=%d seed=%#x got=%#x want=%#x", label, len(b), seed, got, want))
	buf := append([]byte(nil), b...)
	gcStress()
	got2 := xorFoldRolAsm(ptr(b), len(b), seed)
	ck(got2 == want, fmt.Sprintf("%s after GC n=%d seed=%#x", label, len(b), seed))
	ck(bytes.Equal(b, buf), fmt.Sprintf("%s source mutated", label))
	if len(b) > 5 {
		sub := b[2 : len(b)-2]
		ck(xorFoldRolAsm(ptr(sub), len(sub), seed) == xorFoldRolGo(sub, seed),
			fmt.Sprintf("%s sub-slice n=%d", label, len(b)))
	}
	runtime.KeepAlive(b)
}

func main() {
	seeds := []uint64{0, 1, 2, ^uint64(0), 0x8000000000000000, 0x9e3779b97f4a7c15}
	sizes := []int{0, 1, 2, 3, 7, 8, 15, 16, 17, 31, 32, 33, 63, 64, 65, 255, 256, 1024, 65537}
	for si, n := range sizes {
		b := filler(n, si+1)
		for _, seed := range seeds {
			check(b, seed, "seq")
		}
	}
	check(make([]byte, 4096), 12345, "zeros")
	check(bytes.Repeat([]byte{0xff}, 4096), 54321, "ones")

	// 分块续算：seed 传上一段结果，等价于整段一次计算（状态仅在 acc）。
	full := filler(10000, 9)
	whole := xorFoldRolGo(full, 77)
	for _, step := range []int{1, 2, 7, 16, 33, 128, 1024, 4096} {
		acc := uint64(77)
		for off := 0; off < len(full); off += step {
			end := off + step
			if end > len(full) {
				end = len(full)
			}
			acc = xorFoldRolAsm(ptr(full[off:end]), end-off, acc)
		}
		ck(acc == whole, fmt.Sprintf("chunked step=%d got=%#x want=%#x", step, acc, whole))
	}

	finish("l08_asm_xor_fold_rol")
}
