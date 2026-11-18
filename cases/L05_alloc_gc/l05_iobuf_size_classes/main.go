// L05: pgx iobufpool 尺寸类边界：0/255/256/257/…/32MB/32MB+1 的分配、池化复用与 GC。
package main

import (
	"fmt"
	"os"
	"runtime"
	"strconv"
	"sync"
	"sync/atomic"
)

const maxPooled = 32 << 20 // 32MB，与 pgx iobufpool 的最大池化尺寸一致

var (
	failed int32
	gcSink [][]byte
	pools  [18]sync.Pool // 256B..32MB 共 18 个 2 的幂尺寸类
)

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

// classOf 返回覆盖 n 的最小尺寸类容量；超过最大池化尺寸返回 0（走精确分配）。
func classOf(n int) int {
	if n <= 0 {
		return 0
	}
	c := 256
	for c < n {
		c <<= 1
		if c > maxPooled {
			return 0
		}
	}
	return c
}

func poolIndex(class int) int {
	i := 0
	for class > 256 {
		class >>= 1
		i++
	}
	return i
}

func iobufGet(size int) []byte {
	if size < 0 {
		return nil
	}
	if size == 0 {
		return []byte{}
	}
	class := classOf(size)
	if class == 0 {
		return make([]byte, size)
	}
	p := &pools[poolIndex(class)]
	if v := p.Get(); v != nil {
		b := v.(*[]byte)
		return (*b)[:size]
	}
	return make([]byte, size, class)
}

// iobufPut 只回收 cap 恰为尺寸类的缓冲；其它尺寸直接丢弃，避免污染桶。
func iobufPut(b []byte) {
	if b == nil {
		return
	}
	c := cap(b)
	if c < 256 || c > maxPooled || c&(c-1) != 0 {
		return
	}
	z := b[:0]
	p := &pools[poolIndex(c)]
	p.Put(&z)
}

func mark(b []byte, base int) {
	for i := range b {
		b[i] = byte(i*29 + base)
	}
}

func marked(b []byte, base int) bool {
	for i := range b {
		if b[i] != byte(i*29+base) {
			return false
		}
	}
	return true
}

func main() {
	seed := loadSeed()

	// 小尺寸类边界：每档都应在自己的桶里返回精确 cap。
	type bc struct {
		req  int
		want int
	}
	small := []bc{
		{0, 0}, {1, 256}, {255, 256}, {256, 256}, {257, 512},
		{511, 512}, {512, 512}, {513, 1024}, {4095, 4096}, {4096, 4096},
		{4097, 8192}, {1 << 20, 1 << 20},
	}
	held := make([][]byte, len(small))
	for i, c := range small {
		b := iobufGet(c.req)
		ckf(len(b) == c.req, "len req=%d got=%d", c.req, len(b))
		ckf(cap(b) == c.want, "cap req=%d got=%d want=%d", c.req, cap(b), c.want)
		if c.req > 0 {
			mark(b, c.req)
			held[i] = b
		}
	}
	// 0 尺寸缓冲仍可用于 append。
	z := append(iobufGet(0), 7, 8)
	ck(len(z) == 2 && z[0] == 7 && z[1] == 8, "append on zero-size buffer")

	gcStress() // 所有缓冲仍被 held 持有，GC 后内容必须完整
	for i, c := range small {
		if c.req > 0 {
			ckf(marked(held[i], c.req), "content corrupted for req=%d after GC", c.req)
			iobufPut(held[i])
		}
	}

	// 32MB 边界：应命中最大池化桶。
	big := iobufGet(maxPooled)
	ck(len(big) == maxPooled && cap(big) == maxPooled, "32MB class shape")
	big[0], big[1], big[len(big)-1] = 0x11, 0x22, 0x33
	gcStress()
	ck(big[0] == 0x11 && big[1] == 0x22 && big[len(big)-1] == 0x33, "32MB buffer after GC")
	iobufPut(big)
	big = nil
	runtime.GC()

	// 32MB+1：超出池化上限，应精确分配且 Put 后不进入任何桶。
	huge := iobufGet(maxPooled + 1)
	ck(len(huge) == maxPooled+1 && cap(huge) == maxPooled+1, "32MB+1 exact allocation")
	huge[0], huge[len(huge)-1] = 0x44, 0x55
	gcStress()
	ck(huge[0] == 0x44 && huge[len(huge)-1] == 0x55, "32MB+1 buffer after GC")
	iobufPut(huge)
	huge = nil

	// 非法尺寸 Put 不得污染桶：下一次 257 仍必须拿到 512 cap。
	iobufPut(make([]byte, 1000))
	b := iobufGet(257)
	ck(len(b) == 257 && cap(b) == 512, "wrong-size Put polluted bucket")
	iobufPut(b)

	// 动态尺寸循环 + 复用缓冲：写入标记、GC、校验、归还。
	for r := 0; r < 160; r++ {
		size := 1 + r%4096
		want := classOf(size)
		buf := iobufGet(size)
		ckf(len(buf) == size && cap(buf) == want, "dynamic size=%d len=%d cap=%d want=%d", size, len(buf), cap(buf), want)
		base := r%251 + 1
		mark(buf, base)
		if r%32 == 0 {
			gcStress()
		}
		ckf(marked(buf, base), "dynamic buffer size=%d corrupted after GC", size)
		iobufPut(buf)
	}

	// 池可能被 GC 清空：无论新旧对象，取出后 len/cap 契约不变。
	gcStress()
	for k := 0; k < 32; k++ {
		size := 256 + k%8
		buf := iobufGet(size)
		ck(len(buf) == size, "reused pooled buffer len")
		ck(cap(buf) >= size, "reused pooled buffer cap")
		mark(buf, k+1)
		ck(marked(buf, k+1), "reused pooled buffer content")
		iobufPut(buf)
	}

	finish("l05_iobuf_size_classes", seed)
}
