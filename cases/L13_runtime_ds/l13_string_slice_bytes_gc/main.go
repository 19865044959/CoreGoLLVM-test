// L13: string / []byte / 子串 / Builder 在 GC 后的内容与底层数组存活。
package main

import (
	"fmt"
	"os"
	"runtime"
	"strings"
	"sync/atomic"
)

type node struct {
	v    int64
	name string
}

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

//go:noinline
func makeParts() (string, string, string, []byte) {
	base := string([]byte("0123456789abcdefghijklmnopqrstuvwxyz"))
	sub := base[7:27]
	b := []byte(base + "-tail")
	s2 := string(b[10:30])
	return base, sub, s2, b
}

func main() {
	base, sub, s2, b := makeParts()
	gcStress()
	ck(base == "0123456789abcdefghijklmnopqrstuvwxyz", "base string after GC")
	ck(sub == "789abcdefghijklmnopq", "substring after GC")
	ck(s2 == "abcdefghijklmnopqrst", "byte-to-string after GC")
	ck(string(b) == base+"-tail", "byte slice after GC")

	var sb strings.Builder
	for i := 0; i < 100; i++ {
		sb.WriteString("chunk-")
	}
	s := sb.String()
	gcStress()
	ck(len(s) == 600 && strings.HasPrefix(s, "chunk-chunk-"), "builder output after GC")

	m := map[string]*node{}
	for i := 0; i < 100; i++ {
		key := string([]byte{'k', byte('0' + i/10%10), byte('0' + i%10)})
		m[key] = &node{v: int64(i), name: "s"}
	}
	gcStress()
	ck(len(m) == 100 && m["k00"] != nil && m["k00"].v == 0 && m["k99"].v == 99, "string-keyed map after GC")

	uni := "中文-hello-世界"
	u := uni[len("中文-"):len("中文-hello")]
	gcStress()
	ck(u == "hello", "unicode substring after GC")

	runtime.KeepAlive(base)
	runtime.KeepAlive(s)
	runtime.KeepAlive(m)
	finish("l13_string_slice_bytes_gc")
}
