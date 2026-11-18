// L05: 大数组零化语义：make 出的含指针/字符串/切片的数 MB 切片必须全零；
// 写满后丢弃、制造垃圾并 GC，再次按相同尺寸 make 仍必须全零（含 clobberfree
// 复用已释放 span 的情况）。随后重新填充并跨 GC 校验内容。
package main

import (
	"fmt"
	"os"
	"runtime"
	"strconv"
	"sync/atomic"
)

const (
	zCells  = 64 << 10
	zPtrs   = 1 << 18
	zFixedN = 1 << 13
	zBlob   = 6 << 20
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

func ckf(ok bool, format string, args ...interface{}) {
	if !ok {
		atomic.AddInt32(&failed, 1)
		fmt.Printf("FAIL: "+format+"\n", args...)
	}
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

func gcStress() {
	for i := 0; i < 24; i++ {
		b := make([]byte, 64<<10)
		b[0] = byte(i)
		gcSink = append(gcSink[:0], b)
	}
	gcSink = gcSink[:0]
	runtime.GC()
	runtime.GC()
}

type znode struct {
	v    int64
	name string
}

type zCell struct {
	p *znode
	s string
	b []byte
	n int64
}

type zFixed struct {
	buf [256]byte
	s   string
	p   *znode
}

func zCellBad(cs []zCell) int {
	bad := 0
	for i := range cs {
		if cs[i].p != nil || cs[i].s != "" || cs[i].b != nil || cs[i].n != 0 {
			bad++
		}
	}
	return bad
}

func zPtrBad(ps []*znode) int {
	bad := 0
	for i := range ps {
		if ps[i] != nil {
			bad++
		}
	}
	return bad
}

func zFixedBad(fs []zFixed) int {
	bad := 0
	for i := range fs {
		if fs[i].p != nil || fs[i].s != "" {
			bad++
			continue
		}
		for j := 0; j < len(fs[i].buf); j++ {
			if fs[i].buf[j] != 0 {
				bad++
				break
			}
		}
	}
	return bad
}

func zBlobBad(b []byte) int {
	bad := 0
	for i := range b {
		bad |= int(b[i])
	}
	return bad
}

func zFillCell(c *zCell, i int, seed uint64) {
	c.p = &znode{v: int64(i)*3 + int64(seed%7), name: fmt.Sprintf("z-%d", i)}
	c.s = fmt.Sprintf("s-%d-%d", i, seed%11)
	c.b = make([]byte, 8+i%41)
	for j := range c.b {
		c.b[j] = byte((seed*7 + uint64(i)*13 + uint64(j)*5) % 251)
	}
	c.n = int64(i) - int64(seed%5)
}

func zCellCheck(cs []zCell, seed uint64) {
	for i := range cs {
		c := &cs[i]
		ckf(c.p != nil && c.p.v == int64(i)*3+int64(seed%7), "cell=%d pointer/value corrupted", i)
		ckf(c.s == fmt.Sprintf("s-%d-%d", i, seed%11), "cell=%d string corrupted", i)
		ckf(len(c.b) == 8+i%41, "cell=%d slice len=%d", i, len(c.b))
		for j := 0; j < len(c.b); j += 11 {
			if c.b[j] != byte((seed*7+uint64(i)*13+uint64(j)*5)%251) {
				ckf(false, "cell=%d slice byte at=%d", i, j)
				break
			}
		}
		ckf(c.n == int64(i)-int64(seed%5), "cell=%d scalar corrupted", i)
	}
}

func zFixedCheck(fs []zFixed, seed uint64) {
	for i := range fs {
		f := &fs[i]
		ckf(f.p != nil && f.p.v == int64(i)+int64(seed%13), "fixed=%d pointer corrupted", i)
		ckf(f.s == fmt.Sprintf("f-%d", i), "fixed=%d string corrupted", i)
		ckf(f.buf[0] == byte(uint64(i)*7+seed), "fixed=%d buf[0]", i)
		ckf(f.buf[len(f.buf)-1] == byte(uint64(i)*11+seed+3), "fixed=%d buf[last]", i)
	}
}

func main() {
	seed := loadSeed()

	cells := make([]zCell, zCells)
	ptrs := make([]*znode, zPtrs)
	fixed := make([]zFixed, zFixedN)
	blob := make([]byte, zBlob)

	ckf(zCellBad(cells) == 0, "fresh cells not zeroed")
	ckf(zPtrBad(ptrs) == 0, "fresh pointer slice not zeroed")
	ckf(zFixedBad(fixed) == 0, "fresh fixed array not zeroed")
	ckf(zBlobBad(blob) == 0, "fresh byte blob not zeroed")

	for i := range cells {
		zFillCell(&cells[i], i, seed)
	}
	for i := range ptrs {
		ptrs[i] = &znode{v: int64(i) + int64(seed%13), name: fmt.Sprintf("p-%d", i)}
	}
	for i := range fixed {
		fixed[i].p = &znode{v: int64(i) + int64(seed%13), name: fmt.Sprintf("fx-%d", i)}
		fixed[i].s = fmt.Sprintf("f-%d", i)
		fixed[i].buf[0] = byte(uint64(i)*7 + seed)
		fixed[i].buf[len(fixed[i].buf)-1] = byte(uint64(i)*11 + seed + 3)
	}
	for i := range blob {
		blob[i] = byte((uint64(i)*3 + seed) % 251)
	}

	gcStress()
	zCellCheck(cells, seed)
	zFixedCheck(fixed, seed)
	for i := 0; i < len(ptrs); i += 997 {
		ckf(ptrs[i] != nil && ptrs[i].v == int64(i)+int64(seed%13), "ptr[%d] corrupted after GC", i)
	}
	ckf(zBlobBad(blob) != 0, "filled blob unexpectedly zero after GC")

	// 丢弃第一代，制造垃圾并多轮 GC，让已释放 span 有机会被复用。
	cells = nil
	ptrs = nil
	fixed = nil
	blob = nil
	gcStress()
	gcStress()

	cells2 := make([]zCell, zCells)
	ptrs2 := make([]*znode, zPtrs)
	fixed2 := make([]zFixed, zFixedN)
	blob2 := make([]byte, zBlob)

	ckf(zCellBad(cells2) == 0, "reused-span cells not zeroed")
	ckf(zPtrBad(ptrs2) == 0, "reused-span pointer slice not zeroed")
	ckf(zFixedBad(fixed2) == 0, "reused-span fixed array not zeroed")
	ckf(zBlobBad(blob2) == 0, "reused-span byte blob not zeroed")

	for i := range cells2 {
		zFillCell(&cells2[i], i, seed+1)
	}
	for i := range ptrs2 {
		ptrs2[i] = &znode{v: int64(i) + int64((seed+1)%13), name: fmt.Sprintf("q-%d", i)}
	}
	for i := range fixed2 {
		fixed2[i].p = &znode{v: int64(i) + int64((seed+1)%13), name: fmt.Sprintf("gx-%d", i)}
		fixed2[i].s = fmt.Sprintf("f-%d", i)
		fixed2[i].buf[0] = byte(uint64(i)*7 + seed + 1)
		fixed2[i].buf[len(fixed2[i].buf)-1] = byte(uint64(i)*11 + seed + 4)
	}
	gcStress()
	zCellCheck(cells2, seed+1)
	zFixedCheck(fixed2, seed+1)
	for i := 0; i < len(ptrs2); i += 997 {
		ckf(ptrs2[i] != nil && ptrs2[i].v == int64(i)+int64((seed+1)%13), "second ptr[%d] corrupted after GC", i)
	}

	// 再次丢弃并 GC，确保第二代释放后第三代仍从零开始。
	cells2 = nil
	ptrs2 = nil
	fixed2 = nil
	blob2 = nil
	gcStress()
	cells3 := make([]zCell, zCells)
	fixed3 := make([]zFixed, zFixedN)
	ckf(zCellBad(cells3) == 0, "third-generation cells not zeroed")
	ckf(zFixedBad(fixed3) == 0, "third-generation fixed array not zeroed")
	runtime.KeepAlive(cells3)
	runtime.KeepAlive(fixed3)

	finish("l05_make_zero_large", seed)
}
