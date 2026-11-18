// L05: 单个 8MB 超大对象被切成大量相互重叠/相邻的子切片与内部指针视图；
// 丢弃父切片后仅靠子切片与 *byte 内部指针跨 GC 保活，校验内容和别名关系。
package main

import (
	"fmt"
	"os"
	"runtime"
	"strconv"
	"sync/atomic"
)

const (
	hugeSize   = 8 << 20
	chunkCount = 96
	regionSize = hugeSize / chunkCount
)

var (
	failed int32
	gcSink [][]byte
)

type hugeView struct {
	id      int
	off     int
	n       int
	data    []byte
	ptr     *byte
	sum     uint64
	spotOff int
}

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

func hugeByte(i int, seed uint64) byte {
	return byte((uint64(i)*13 + uint64(i>>7)*29 + seed*7 + 3) % 251)
}

func fillHuge(b []byte, seed uint64) {
	for i := range b {
		b[i] = hugeByte(i, seed)
	}
}

func sumRange(b []byte) uint64 {
	h := uint64(1469598103934665603)
	for i := range b {
		h ^= uint64(b[i])
		h *= 1099511628211
	}
	return h
}

func checkView(v *hugeView, seed uint64) bool {
	if len(v.data) != v.n {
		return false
	}
	if *v.ptr != hugeByte(v.off+v.n/2, seed) {
		return false
	}
	if v.data[v.spotOff] != hugeByte(v.off+v.spotOff, seed) {
		return false
	}
	if sumRange(v.data) != v.sum {
		return false
	}
	return true
}

func main() {
	seed := loadSeed()

	huge := make([]byte, hugeSize)
	fillHuge(huge, seed)

	views := make([]hugeView, 0, chunkCount)
	for c := 0; c < chunkCount; c++ {
		off := c*regionSize + (c*37)%89
		maxN := regionSize - 200
		n := 512 + (c*911)%(maxN-512+1)
		d := huge[off : off+n : off+n] // 满容量表达式：append 不得越界改邻居
		views = append(views, hugeView{
			id:      c,
			off:     off,
			n:       n,
			data:    d,
			ptr:     &huge[off+n/2],
			sum:     sumRange(d),
			spotOff: (c * 13) % n,
		})
	}

	// 子切片与父缓冲共享同一底层数组：通过子切片写入立刻在父视图中可见。
	for i := 0; i < 8; i++ {
		v := &views[i]
		old := v.data[0]
		v.data[0] = old ^ 0x5a
		ckf(huge[v.off] == old^0x5a, "view=%d not aliasing parent", v.id)
		v.data[0] = old
	}

	// 在尾部独立区域验证 full-slice 表达式后的 append 只写自身容量范围。
	tailOff := hugeSize - 128*1024
	seg := huge[tailOff : tailOff+16 : tailOff+96]
	orig16 := huge[tailOff+16]
	orig17 := huge[tailOff+17]
	seg = append(seg, 0xAA)
	ck(len(seg) == 17 && cap(seg) == 96, "tail append len/cap")
	ck(huge[tailOff+16] == 0xAA, "tail append did not write backing")
	ck(huge[tailOff+17] == orig17, "tail append overran its own len")
	huge[tailOff+16] = orig16

	// 丢弃父切片：只有子切片和内部指针可见，GC 必须保活整个大对象。
	huge = nil
	for i := 0; i < 24; i++ {
		junk := make([]byte, 512<<10)
		junk[0] = byte(i)
		gcSink = append(gcSink[:0], junk)
	}
	gcSink = gcSink[:0]
	runtime.GC()
	runtime.GC()

	for i := range views {
		if !checkView(&views[i], seed) {
			ckf(false, "view=%d off=%d n=%d corrupted after parent drop+GC", views[i].id, views[i].off, views[i].n)
		}
	}

	// 只保留 1/8 的视图，其余底层引用释放后再 GC。
	var kept []hugeView
	for i := 0; i < len(views); i += 8 {
		kept = append(kept, views[i])
	}
	views = nil
	gcStress()
	for i := range kept {
		if !checkView(&kept[i], seed) {
			ckf(false, "kept view=%d corrupted after trimming", kept[i].id)
		}
	}

	// 最终只留一个 *byte 内部指针：没有 slice header，仍应保活整个 8MB 对象。
	last := kept[len(kept)-1]
	lastData := last.data
	keepPtr := last.ptr
	kept = nil
	gcStress()
	ck(*keepPtr == hugeByte(last.off+last.n/2, seed), "interior pointer survives without slice header")
	if lastData[last.spotOff] != hugeByte(last.off+last.spotOff, seed) {
		ck(false, "retained sub-slice corrupted at final stage")
	}

	lastData = nil
	gcStress()
	ck(*keepPtr == hugeByte(last.off+last.n/2, seed), "interior pointer survives after dropping last sub-slice")
	runtime.KeepAlive(keepPtr)

	finish("l05_huge_self_slice", seed)
}
