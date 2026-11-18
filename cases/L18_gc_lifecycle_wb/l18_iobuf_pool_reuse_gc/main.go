// L18: sync.Pool 尺寸类缓冲复用；Put 后 GC、外部引用存活、错误尺寸丢弃与标记字节校验。
package main

import (
	"fmt"
	"os"
	"runtime"
	"sync"
	"sync/atomic"
)

const (
	minClass   = 256
	maxPooled  = 32 << 20 // 32 MiB
	classCount = 18       // 256B .. 32MiB
)

var (
	failed int32
	pools  [classCount]sync.Pool
)

func ck(ok bool, msg string) {
	if !ok {
		atomic.AddInt32(&failed, 1)
		fmt.Println("FAIL:", msg)
	}
}

func gcStress() {
	var sink [][]byte
	for i := 0; i < 16; i++ {
		b := make([]byte, 4096)
		b[0] = byte(i)
		sink = append(sink, b)
	}
	runtime.GC()
	runtime.GC()
	runtime.KeepAlive(sink)
}

func classIndex(size int) int {
	idx := 0
	for s := minClass; s < size; s <<= 1 {
		idx++
	}
	return idx
}

func classSize(idx int) int { return minClass << idx }

// poolGet 返回 len 精确为 n 的缓冲；n<=32MiB 时 cap 为 2 的幂尺寸类。
func poolGet(n int) *[]byte {
	if n < 0 {
		panic("negative buffer size")
	}
	if n > maxPooled {
		b := make([]byte, n)
		return &b
	}
	idx := classIndex(n)
	if v := pools[idx].Get(); v != nil {
		b := v.(*[]byte)
		*b = (*b)[:n]
		return b
	}
	b := make([]byte, n, classSize(idx))
	return &b
}

// poolPut 只回收 256B..32MiB 且 cap 为 2 的幂的缓冲，其余直接丢弃。
func poolPut(b *[]byte) {
	if b == nil {
		return
	}
	c := cap(*b)
	if c < minClass || c > maxPooled || c&(c-1) != 0 {
		return
	}
	idx := classIndex(c)
	if classSize(idx) != c {
		return
	}
	pools[idx].Put(b)
}

func marker(i int) byte { return byte(0x40 + i%61) }

func writeMarker(b []byte, m byte) {
	if len(b) == 0 {
		return
	}
	b[0] = m
	b[len(b)/2] = m
	b[len(b)-1] = m
}

func hasMarker(b []byte, m byte) bool {
	return len(b) > 0 && b[0] == m && b[len(b)/2] == m && b[len(b)-1] == m
}

// externalRefAfterPut: Put 后仍持有外部引用，GC（clobberfree 会清零释放内存）后内容必须完好。
func externalRefAfterPut() {
	b := poolGet(4096)
	ck(len(*b) == 4096 && cap(*b) == 4096, "external ref initial len/cap")
	m := marker(7)
	writeMarker(*b, m)
	poolPut(b)
	gcStress()
	ck(hasMarker(*b, m), "buffer held by external reference corrupted after Put+GC")
	b = nil
	gcStress()
}

func wrongSizePut() {
	// 非 2 的幂 cap 必须被丢弃，不得污染 512B 尺寸类。
	bad := make([]byte, 300, 300)
	poolPut(&bad)
	// 正确尺寸的 512B 缓冲则应可回收/复用。
	good := make([]byte, 7, 512)
	writeMarker(good, marker(9))
	poolPut(&good)
	b := poolGet(300)
	ck(len(*b) == 300 && cap(*b) == 512, "wrong-sized Put polluted 512B class")
	poolPut(b)
}

func main() {
	sizes := []int{0, 1, 4, 128, 255, 256, 257, 511, 512, 513, 1000, 1023, 1024, 4096, 65535, 65536}

	// 主循环：取/写标记/放回，穿插 GC；每次 Get 的 len/cap 契约必须成立。
	for round := 0; round < 24; round++ {
		for i, req := range sizes {
			b := poolGet(req)
			ck(len(*b) == req, fmt.Sprintf("Get(%d) len=%d", req, len(*b)))
			want := req
			if req > maxPooled {
				want = req
			} else {
				want = classSize(classIndex(req))
			}
			ck(cap(*b) == want, fmt.Sprintf("Get(%d) cap=%d want=%d", req, cap(*b), want))
			if req > 0 {
				writeMarker(*b, marker(round+i))
			}
			poolPut(b)
		}
		if round%4 == 0 {
			gcStress()
		}
	}

	// 连续 Get/Put（无 GC 间隔）应观察到池复用：复用缓冲保留我们上次写入的首字节标记。
	seen := 0
	last := marker(3)
	for i := 0; i < 2048; i++ {
		b := poolGet(4)
		if (*b)[0] == last {
			seen++
		}
		writeMarker(*b, last)
		poolPut(b)
	}
	ck(seen > 0, "sync.Pool never reused a 256B-class buffer in 2048 attempts")

	// GC 间隔后再取，池可能已丢弃旧缓冲，但仍必须返回正确尺寸类。
	for i := 0; i < 8; i++ {
		gcStress()
		b := poolGet(257)
		ck(len(*b) == 257 && cap(*b) == 512, "post-GC reuse broke size class")
		writeMarker(*b, marker(i))
		poolPut(b)
	}

	externalRefAfterPut()
	wrongSizePut()

	// 大缓冲（>32MiB）跳过池，精确分配；用小尺寸验证同一分支逻辑。
	big := poolGet(maxPooled + 1)
	ck(len(*big) == maxPooled+1 && cap(*big) == maxPooled+1, "oversize allocation not exact")
	big = nil
	gcStress()

	if n := atomic.LoadInt32(&failed); n != 0 {
		fmt.Printf("FAILED l18_iobuf_pool_reuse_gc (%d failed checks)\n", n)
		os.Exit(1)
	}
	fmt.Println("ALL PASS l18_iobuf_pool_reuse_gc")
}
