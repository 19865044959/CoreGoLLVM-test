// L08: 手写 amd64 汇编按字典序比较两个字节串（有界 strcmp 语义），
// 返回 -1/0/1；与 Go 参考实现逐例对照，覆盖前缀、首/中/末差异与空输入。
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

func strcmpAsm(a, b *byte, na, nb int) int

func strcmpGo(a, b []byte) int {
	n := len(a)
	if len(b) < n {
		n = len(b)
	}
	for i := 0; i < n; i++ {
		if a[i] < b[i] {
			return -1
		}
		if a[i] > b[i] {
			return 1
		}
	}
	if len(a) < len(b) {
		return -1
	}
	if len(a) > len(b) {
		return 1
	}
	return 0
}

func ptr(b []byte) *byte {
	if len(b) == 0 {
		return nil
	}
	return &b[0]
}

func mk(n int, base, step int) []byte {
	s := make([]byte, n)
	for i := range s {
		s[i] = byte((base + i*step) & 0xff)
	}
	return s
}

func main() {
	type pair struct{ a, b []byte }
	cases := []pair{
		{nil, nil},
		{[]byte{}, []byte{}},
		{[]byte{0}, nil},
		{[]byte("abc"), []byte("abc")},
		{[]byte("abc"), []byte("abd")},
		{[]byte("abd"), []byte("abc")},
		{[]byte("ab"), []byte("abc")},
		{[]byte("abc"), []byte("ab")},
		{[]byte{0x00, 0xff, 0x10}, []byte{0x00, 0xfe, 0x10}},
		{[]byte{0x80, 1, 2}, []byte{0x7f, 1, 2}},
		{[]byte("prefix"), []byte("prefab")},
	}
	for i, c := range cases {
		got := strcmpAsm(ptr(c.a), ptr(c.b), len(c.a), len(c.b))
		want := strcmpGo(c.a, c.b)
		ck(got == want, fmt.Sprintf("strcmp case=%d got=%d want=%d", i, got, want))
	}

	// 定种子生成的长串：相同、首位差异、中位差异、末位差异。
	for _, n := range []int{1, 2, 15, 16, 17, 63, 256, 1024} {
		a := mk(n, 7, 11)
		b := append([]byte(nil), a...)
		ck(strcmpAsm(ptr(a), ptr(b), n, n) == 0, fmt.Sprintf("equal n=%d", n))
		if n > 0 {
			for _, pos := range []int{0, n / 2, n - 1} {
				b[pos] ^= 0x40
				got := strcmpAsm(ptr(a), ptr(b), n, n)
				ck(got == strcmpGo(a, b), fmt.Sprintf("differ n=%d pos=%d", n, pos))
				ck(got != 0, fmt.Sprintf("differ nonzero n=%d pos=%d", n, pos))
				b[pos] ^= 0x40
			}
		}
		gcStress()
		ck(strcmpAsm(ptr(a), ptr(b), n, n) == 0, fmt.Sprintf("equal after GC n=%d", n))
		runtime.KeepAlive(a)
		runtime.KeepAlive(b)
	}

	// 非零起始偏移的子切片。
	s := mk(512, 3, 5)
	t := append([]byte(nil), s...)
	t[100]++
	ck(strcmpAsm(&s[64], &t[64], 200, 200) == strcmpGo(s[64:264], t[64:264]), "sub-slice compare")

	finish("l08_asm_strcmp_lex")
}
