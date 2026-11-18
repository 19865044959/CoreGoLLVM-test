// L07: byte buffer 复用别名：slab 子区间分配、跨代覆盖、零拷贝 string 视图与快照对照。
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

type slab struct {
	buf []byte
	off int
	gen int
}

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
		for i := 0; i < 10; i++ {
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

func newSlab() *slab {
	return &slab{buf: make([]byte, 256)}
}

// alloc 返回 8 字节对齐的子区间；cap 不足时返回 nil。
func (s *slab) alloc(n int) []byte {
	n = (n + 7) &^ 7
	if s.off+n > len(s.buf) {
		return nil
	}
	b := s.buf[s.off : s.off+n]
	s.off += n
	return b
}

// fillRegion 写入固定格式记录，正好填满 dst（dst 长度已是 8 的倍数）。
func fillRegion(dst []byte, round, idx, tag int) {
	head := fmt.Sprintf("r%03d-%02d-%02d;", round%1000, idx%100, tag%100)
	copy(dst, head)
	for j := len(head); j < len(dst); j++ {
		dst[j] = byte('a' + (round+idx+j)%26)
	}
}

func regionString(dst []byte) string {
	return unsafe.String(unsafe.SliceData(dst), len(dst))
}

func tortureRounds(def int) int {
	if s := os.Getenv("TORTURE_ROUNDS"); s != "" {
		if v, err := strconv.Atoi(s); err == nil && v > 0 {
			return v
		}
	}
	return def
}

type region struct {
	view  []byte
	alias string
	snap  string
	start int
	size  int
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
	rounds := tortureRounds(48)

	for r := 0; r < rounds; r++ {
		sl := newSlab()
		tag := int(seed) + r
		var regions []region
		var got []string
		var want []string
		start := 0
		for i := 0; i < 30; i++ {
			size := 16 + 8*(i%3)
			view := sl.alloc(size)
			if view == nil {
				break
			}
			ck(len(view) == size, "region size r="+strconv.Itoa(r)+" i="+strconv.Itoa(i))
			ck(sl.off == start+size, "region advance r="+strconv.Itoa(r)+" i="+strconv.Itoa(i))
			fillRegion(view, r, i, tag)
			alias := regionString(view)
			snap := string(view)
			ck(alias == snap, "alias equals snapshot r="+strconv.Itoa(r)+" i="+strconv.Itoa(i))
			ck(unsafe.Pointer(unsafe.StringData(alias)) ==
				unsafe.Add(unsafe.Pointer(unsafe.SliceData(sl.buf)), start),
				"alias data offset r="+strconv.Itoa(r)+" i="+strconv.Itoa(i))
			regions = append(regions, region{view: view, alias: alias, snap: snap, start: start, size: size})
			start += size
			got = append(got, alias)
			want = append(want, snap)
		}
		ck(len(regions) > 0, "some regions allocated r="+strconv.Itoa(r))
		// 剩余空间不足最小对齐单位：再分配必须失败。
		ck(sl.alloc(64) == nil, "slab full r="+strconv.Itoa(r))

		// 区域互不重叠：start 严格递增且等于前一段结尾。
		for i := 1; i < len(regions); i++ {
			ck(regions[i].start == regions[i-1].start+regions[i-1].size,
				"regions disjoint r="+strconv.Itoa(r)+" i="+strconv.Itoa(i))
		}

		// 旧 slab 引用丢弃：只留零拷贝 string 视图时，底层缓冲必须被保活。
		sl = nil
		if r%8 == 7 {
			gcStress()
		}
		for i := range got {
			ck(got[i] == want[i], "alias after slab drop r="+strconv.Itoa(r)+" i="+strconv.Itoa(i))
		}
		gcStress()

		// 新 slab 与旧 slab 的缓冲彼此独立：写入新 slab 不应改变旧视图。
		sl2 := newSlab()
		for i := 0; i < len(regions); i++ {
			view := sl2.alloc(regions[i].size)
			ck(view != nil, "second slab alloc r="+strconv.Itoa(r)+" i="+strconv.Itoa(i))
			fillRegion(view, r+1, i, tag+1)
			ck(unsafe.Pointer(unsafe.SliceData(view)) != unsafe.Pointer(unsafe.SliceData(regions[i].view)),
				"second slab backing differs r="+strconv.Itoa(r)+" i="+strconv.Itoa(i))
			ck(string(view) != regions[i].snap, "new slab content differs r="+strconv.Itoa(r)+" i="+strconv.Itoa(i))
			ck(regions[i].alias == regions[i].snap, "old alias independent r="+strconv.Itoa(r)+" i="+strconv.Itoa(i))
		}
		sl2 = nil
		gcStress()
		for i := 0; i < len(regions); i++ {
			ck(regions[i].alias == regions[i].snap, "old alias after second slab GC i="+strconv.Itoa(i))
		}
		ck(regions[0].snap == want[0], "snapshot after overwrite")

		// 同一 slab 原地 reset + 覆盖：alias 看到新内容，快照保持旧内容。
		reuse := newSlab()
		rv1 := reuse.alloc(24)
		ck(rv1 != nil, "in-place first alloc")
		fillRegion(rv1, r+5, 11, tag+5)
		ra := regionString(rv1)
		rs := string(rv1)
		reuse.off = 0
		rv2 := reuse.alloc(24)
		ck(rv2 != nil, "in-place second alloc")
		fillRegion(rv2, r+6, 12, tag+6)
		ck(unsafe.Pointer(unsafe.SliceData(rv1)) == unsafe.Pointer(unsafe.SliceData(rv2)),
			"in-place reset same buffer")
		ck(ra == regionString(rv2), "alias observes in-place overwrite")
		ck(rs != ra, "snapshot survives in-place overwrite")
		ck(regionString(rv1) == ra, "second view sees same overwrite")
		gcStress()
		ck(ra == regionString(rv2) && rs != ra, "in-place alias/snapshot after GC")
		reuse = nil
		gcStress()
		ck(ra == regionString(rv2) && rs != ra, "in-place alias/snapshot after slab drop")
		runtime.KeepAlive(rv1)
		runtime.KeepAlive(rv2)

		// 半覆盖：写第一段字节视图的前半，后半别名内容不变。
		if len(regions) > 1 {
			firstView := regions[0].view
			half := len(firstView) / 2
			if half > 0 {
				oldTail := string(firstView[half:])
				for j := 0; j < half; j++ {
					firstView[j] = '#'
				}
				ck(string(firstView[half:]) == oldTail, "partial overwrite keeps tail")
				ck(strings.HasPrefix(string(firstView), strings.Repeat("#", half)), "partial overwrite head")
			}
		}

		// 保留最终视图做一次尾部 GC 校验。
		runtime.KeepAlive(regions)
		runtime.KeepAlive(got)
		runtime.KeepAlive(want)
	}

	// 未覆盖的 slack：分配两个区域后，中间对齐填充不写入，边界不串。
	sl := newSlab()
	a := sl.alloc(20) // 对齐后 24
	b := sl.alloc(20)
	ck(len(a) == 24 && len(b) == 24, "aligned region lengths")
	ck(unsafe.Pointer(unsafe.SliceData(b)) ==
		unsafe.Add(unsafe.Pointer(unsafe.SliceData(a)), len(a)), "adjacent regions")
	for i := range a {
		a[i] = 'A'
	}
	for i := range b {
		b[i] = 'B'
	}
	ck(string(a) == strings.Repeat("A", 24), "region a intact")
	ck(string(b) == strings.Repeat("B", 24), "region b intact")
	as := regionString(a)
	bs := regionString(b)
	sl = nil
	gcStress()
	ck(as == strings.Repeat("A", 24) && bs == strings.Repeat("B", 24), "regions after slab drop")
	runtime.KeepAlive(as)
	runtime.KeepAlive(bs)
	finish("l07_slab_reuse_alias_regions")
}
