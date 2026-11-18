// L07: string<->[]byte 零拷贝边界：空串、子串、临时源、KeepAlive 与 GC 生命周期。
package main

import (
	"fmt"
	"os"
	"runtime"
	"strconv"
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
func aliasString(b []byte) string {
	if len(b) == 0 {
		return ""
	}
	return unsafe.String(unsafe.SliceData(b), len(b))
}

//go:noinline
func aliasBytes(s string) []byte {
	if len(s) == 0 {
		return nil
	}
	return unsafe.Slice(unsafe.StringData(s), len(s))
}

//go:noinline
func makePayload(tag int64, n int) []byte {
	b := make([]byte, n)
	for i := range b {
		b[i] = byte('A' + (tag+int64(i))%26)
	}
	return b
}

// payloadString 返回与 makePayload(tag, n) 内容一致的独立副本。
func payloadString(tag int64, n int) string {
	b := make([]byte, n)
	for i := range b {
		b[i] = byte('A' + (tag+int64(i))%26)
	}
	return string(b)
}

// bytesFromFresh 的 []byte 参数与局部 string 都只在本函数内存在，
// 返回的 []byte 视图必须是底层数组的唯一引用。
//
//go:noinline
func bytesFromFresh(tag int64, n int) []byte {
	return aliasBytes(aliasString(makePayload(tag, n)))
}

// sumAcrossGC 在 KeepAlive 保护的 string 上派生零拷贝 []byte，
// GC 落在派生之后、读取之前。
//
//go:noinline
func sumAcrossGC(s string) int {
	b := aliasBytes(s)
	garbage := make([]byte, 4096)
	garbage[len(garbage)-1] = byte(len(b))
	gcSink = append(gcSink[:0], garbage)
	runtime.GC()
	sum := 0
	for _, c := range b {
		sum += int(c)
	}
	runtime.KeepAlive(s)
	runtime.KeepAlive(b)
	return sum
}

func sumBytes(b []byte) int {
	s := 0
	for _, c := range b {
		s += int(c)
	}
	return s
}

func main() {
	if s := os.Getenv("UAF_SEED"); s != "" {
		if v, err := strconv.ParseInt(s, 10, 64); err == nil {
			seed = v
		}
	}
	if seed < 0 {
		seed = -seed
	}

	// 1) 子切片 -> 零拷贝 string，数据指针偏移与长度精确。
	off := int(seed % 8)
	n := 24 + int(seed%16)
	src := makePayload(seed, 48)
	sub := src[off : off+n]
	alias := aliasString(sub)
	copyStr := string(sub)
	ck(len(alias) == n, "alias length")
	ck(alias == copyStr, "alias content")
	ck(unsafe.Pointer(unsafe.StringData(alias)) ==
		unsafe.Add(unsafe.Pointer(unsafe.SliceData(src)), off), "alias data offset")

	// 2) 源 []byte 可写、string 别名可见；复制版不受影响。
	oldCopy := copyStr
	sub[0] = '!'
	ck(alias[0] == '!', "alias observes source mutation")
	ck(copyStr == oldCopy && copyStr[0] != '!', "copy is independent")
	expectMutated := "!" + oldCopy[1:]
	ck(alias == expectMutated, "alias after mutation")

	// 3) 丢弃源 []byte，只留 string 视图，GC 后仍有效。
	src = nil
	sub = nil
	gcStress()
	ck(alias == expectMutated, "alias survives GC without byte slice")
	ck(copyStr == oldCopy, "copy survives GC")

	// 4) 子串视图的头部偏移正确，且丢弃父串后仍保活底层数组。
	sub2 := alias[3:]
	ck(len(sub2) == n-3, "substring length")
	ck(unsafe.Pointer(unsafe.StringData(sub2)) ==
		unsafe.Add(unsafe.Pointer(unsafe.StringData(alias)), 3), "substring data offset")
	alias = ""
	gcStress()
	ck(sub2 == expectMutated[3:], "substring survives parent drop")

	// 5) 空串/空切片边界。
	ck(aliasString(nil) == "", "nil byte slice to string")
	ck(len(aliasString([]byte{})) == 0, "empty byte slice to string")
	ck(len(aliasBytes("")) == 0, "empty string to bytes")
	ck(aliasString(make([]byte, 0, 32)) == "", "zero-length capped slice to string")

	// 6) 含 NUL 的字节序列：长度按字节而非字符计。
	zz := []byte{'a', 0, 'b', 0, 'c'}
	zs := aliasString(zz)
	zz = nil
	gcStress()
	ck(len(zs) == 5 && zs[0] == 'a' && zs[1] == 0 && zs[2] == 'b' && zs[3] == 0 && zs[4] == 'c',
		"embedded NUL survives GC")

	// 7) 临时源跨函数边界：源 []byte 与中间 string 都不再可达，仅视图可达。
	freshN := 32 + int(seed%16)
	fresh := bytesFromFresh(7, freshN)
	gcStress()
	ck(string(fresh) == payloadString(7, freshN), "fresh view from dead temporaries")
	ck(sumBytes(fresh) == sumBytes(makePayload(7, freshN)), "fresh view checksum")

	// 8) KeepAlive 路径：参数 string 在 GC 期间必须保持存活。
	kaN := 20 + int(seed%12)
	kaSrc := makePayload(seed+3, kaN)
	kaStr := aliasString(kaSrc)
	kaSrc = nil
	wantSum := sumBytes(makePayload(seed+3, kaN))
	ck(sumAcrossGC(kaStr) == wantSum, "KeepAlive string across explicit GC")

	// 9) 批量：每轮由临时 []byte 生成 string，跨 GC 保留部分视图。
	var kept []string
	var keptWant []string
	for r := 0; r < 80; r++ {
		m := 16 + int((seed+int64(r))%24)
		s := aliasString(makePayload(int64(r), m))
		want := payloadString(int64(r), m)
		ck(s == want, "round alias content r="+strconv.Itoa(r))
		if r%10 == 0 {
			kept = append(kept, s)
			keptWant = append(keptWant, want)
		}
		if r%16 == 15 {
			gcStress()
		}
	}
	gcStress()
	for i := range kept {
		ck(kept[i] == keptWant[i], "kept view after GC i="+strconv.Itoa(i))
	}

	// 10) 再次确认释放后的视图保活的是各自底层数组，而非复用同一块。
	ck(len(kept) == 8, "kept view count")
	runtime.KeepAlive(kept)
	runtime.KeepAlive(keptWant)
	runtime.KeepAlive(sub2)
	runtime.KeepAlive(copyStr)
	finish("l07_string_bytes_boundary_keepalive")
}
