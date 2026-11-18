// L07: 池化 []byte 的别名与复制语义：零拷贝 string 视图随缓冲复用变化，复制版保持不变。
package main

import (
	"fmt"
	"os"
	"runtime"
	"strconv"
	"strings"
	"sync"
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

// 固定复用同一批底层数组的字节池（确定语义的 sync.Pool 模型）。
type freeList struct {
	mu   sync.Mutex
	bufs [][]byte
}

func (f *freeList) get() []byte {
	f.mu.Lock()
	defer f.mu.Unlock()
	if n := len(f.bufs); n > 0 {
		b := f.bufs[n-1]
		f.bufs = f.bufs[:n-1]
		return b[:0]
	}
	return make([]byte, 0, 128)
}

func (f *freeList) put(b []byte) {
	f.mu.Lock()
	f.bufs = append(f.bufs, b[:0])
	f.mu.Unlock()
}

// 所有记录固定 16 字节，保证别名 string 的长度不随复用变化。
func fillRecord(dst []byte, id int, state string) []byte {
	return fmt.Appendf(dst, "rec:%02d;%s;end", id, state)
}

func recordString(id int, state string) string {
	return fmt.Sprintf("rec:%02d;%s;end", id, state)
}

//go:noinline
func zeroCopyString(b []byte) string {
	return unsafe.String(unsafe.SliceData(b), len(b))
}

func main() {
	if s := os.Getenv("UAF_SEED"); s != "" {
		if v, err := strconv.ParseInt(s, 10, 64); err == nil {
			seed = v
		}
	}
	states := []string{"alpha", "bravo", "charl", "delta", "echoo"}
	stateA := states[int(seed%5)]
	stateB := states[int((seed+1)%5)]
	stateC := states[int((seed+2)%5)]
	idA := int(seed % 10)
	idB := (idA + 1) % 90
	idC := (idB + 1) % 90

	pool := &freeList{}
	buf := pool.get()
	buf = fillRecord(buf, idA, stateA)
	wantA := recordString(idA, stateA)
	alias := zeroCopyString(buf)
	copied := string(buf)

	gcStress()
	ck(alias == wantA, "alias string valid after GC while buffer is held")
	ck(copied == wantA, "copied string valid after GC")
	ck(len(alias) == 16 && len(copied) == 16, "record length")

	// 归还后 GC：池的空闲链表必须保活底层数组。
	pool.put(buf)
	gcStress()

	buf2 := pool.get()
	ck(unsafe.SliceData(buf2) == unsafe.StringData(alias), "pool reuses the same backing array")
	ck(len(buf2) == 0, "pool resets length on get")
	buf2 = fillRecord(buf2, idB, stateB)
	wantB := recordString(idB, stateB)
	ck(alias == wantB, "zero-copy view observes pooled buffer reuse")
	ck(copied == wantA, "copied string insulated from pooled reuse")
	ck(unsafe.SliceData(buf2) == unsafe.StringData(alias), "alias still points at reused buffer")

	// 正确用法：归还前先复制。
	snap := strings.Clone(alias)
	pool.put(buf2)
	buf3 := pool.get()
	buf3 = fillRecord(buf3, idC, stateC)
	wantC := recordString(idC, stateC)

	gcStress()
	ck(alias == wantC, "alias follows second reuse")
	ck(snap == wantB, "snapshot taken before release stays stable")
	ck(copied == wantA, "first copy stays stable")
	ck(string(buf3) == wantC, "pooled buffer content after GC")
	ck(len(buf3) == 16 && cap(buf3) >= 16, "pooled buffer shape")

	runtime.KeepAlive(buf3)
	runtime.KeepAlive(snap)
	finish("l07_byte_pool_alias_copy")
}
