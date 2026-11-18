// L07: []byte→string 零拷贝视图在源存活期内有效；复制语义与别名语义的差异是确定性的。
package main

import (
	"fmt"
	"os"
	"runtime"
	"strconv"
	"strings"
	"sync/atomic"
	"unsafe"
)

var (
	failed int32
	gcSink [][]byte
	seed   int64
)

func ck(ok bool, msg string) {
	if !ok {
		atomic.AddInt32(&failed, 1)
		fmt.Printf("FAIL: %s (seed=%d)\n", msg, seed)
	}
}

func gcStress() {
	for r := 0; r < 2; r++ {
		for i := 0; i < 12; i++ {
			b := make([]byte, 8192)
			b[0] = byte(i)
			b[len(b)-1] = byte(255 - i)
			gcSink = append(gcSink[:0], b)
		}
		gcSink = gcSink[:0]
		runtime.GC()
	}
}

func finish(name string) {
	if n := atomic.LoadInt32(&failed); n != 0 {
		fmt.Printf("FAILED %s (%d failed checks)\n", name, n)
		os.Exit(1)
	}
	fmt.Println("ALL PASS " + name)
}

//go:noinline
func makeSrc(n int, s int64) *[256]byte {
	a := new([256]byte)
	for i := 0; i < n; i++ {
		a[i] = byte('a' + (int64(i)+s)%26)
	}
	return a
}

//go:noinline
func asString(b []byte) string {
	return unsafe.String(unsafe.SliceData(b), len(b))
}

//go:noinline
func detachedView(s int64) string {
	b := make([]byte, 48)
	for i := range b {
		b[i] = byte('A' + (int64(i)+s)%26)
	}
	return asString(b)
}

func expectedDetached(s int64) string {
	b := make([]byte, 48)
	for i := range b {
		b[i] = byte('A' + (int64(i)+s)%26)
	}
	return string(b)
}

func main() {
	if s := os.Getenv("UAF_SEED"); s != "" {
		if v, err := strconv.ParseInt(s, 10, 64); err == nil {
			seed = v
		}
	}
	n := 64 + int(seed%32)

	// 1) 堆数组 -> 零拷贝 string + 复制 string。
	arr := makeSrc(n, seed)
	src := arr[:n]
	view := asString(src)
	copied := string(src)
	want := string(src)

	gcStress()
	ck(view == want, "zero-copy view valid after GC")
	ck(copied == want, "copied string valid after GC")

	// 2) 改写源：零拷贝视图可见，复制版不受影响。
	src[0] = 'Z'
	src[1] = 'Y'
	wantBytes := []byte(want)
	wantBytes[0] = 'Z'
	wantBytes[1] = 'Y'
	wantMut := string(wantBytes)
	ck(view == wantMut, "zero-copy view observes source mutation")
	ck(copied == want, "copied string insulated from source mutation")
	ck(view != copied, "alias and copy differ deterministically")

	// 3) 释放源 slice/数组变量，视图仍保活底层数组（string 指针是 GC 根）。
	arr = nil
	src = nil
	gcStress()
	ck(view == wantMut, "view survives after source slice/array released")

	// 4) 安全模式：先复制再丢弃视图。
	snapshot := strings.Clone(view)
	view = ""
	gcStress()
	ck(snapshot == wantMut, "copy taken before dropping view stays valid")

	// 5) 源仅存在于被调函数局部：返回的零拷贝 string 保活其底层数组。
	dv := detachedView(seed)
	wantD := expectedDetached(seed)
	gcStress()
	ck(dv == wantD, "detached view keeps backing array alive")

	// 6) 复用缓冲：别名视图随内容变化，复制版固定。
	buf := make([]byte, 0, 64)
	buf = append(buf, "lifetime-record-01;alpha;end"...)
	alias := asString(buf)
	cpy := string(buf)
	gcStress()
	ck(alias == "lifetime-record-01;alpha;end", "buffer alias after GC")
	ck(cpy == alias, "buffer copy equals alias while content unchanged")

	buf = buf[:0]
	buf = append(buf, "lifetime-record-02;bravo;end"...)
	ck(alias == "lifetime-record-02;bravo;end", "alias follows buffer reuse")
	ck(cpy == "lifetime-record-01;alpha;end", "copy insulated from buffer reuse")

	snap := strings.Clone(alias)
	buf = buf[:0]
	buf = append(buf, "lifetime-record-03;charl;end"...)
	gcStress()
	ck(alias == "lifetime-record-03;charl;end", "alias follows second reuse after GC")
	ck(snap == "lifetime-record-02;bravo;end", "snapshot stays stable")
	ck(cpy == "lifetime-record-01;alpha;end", "copy stays stable after GC")

	runtime.KeepAlive(buf)
	runtime.KeepAlive(snapshot)
	runtime.KeepAlive(dv)
	finish("l07_byte_to_string_lifetime")
}
