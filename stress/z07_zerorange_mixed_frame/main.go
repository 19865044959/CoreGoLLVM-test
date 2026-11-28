// Z07: zerorange 清零区间与大帧栈增长（P0）。
// 目标：go#75477（mips64 zerorange 漏了 +8 偏移 → 清零区间错位、指针槽盖不全，
// "bad pointer in frame"）。x05/y01 只依赖“未清零”制造脏栈，本场景正面校验
// “该清零的确实清零、该保留的完好、指针槽非垃圾”。
//
// mixedFrame 是一个帧估算 ~39KB 的大函数，混入 19 组不同类型的局部（指针数组、
// 字符串数组、slice、interface、map、嵌套 struct 数组、bool/int/byte/float/
// complex/int16/int8/uint32 标量数组），沿 11 个 seed 派生分支做部分初始化 →
// 编译器对未初始化区间发射多个 zerorange/memclr。函数中部调用 growDeep 深递归，
// 强制 morestack 栈增长/复制整个大帧；增长前后各一次 runtime.GC()。
// 增长后校验：
//
//	a) 未走分支的局部 == 零值（Go 语义保证，读它们合法）；
//	b) 已初始化局部的 canary 完好；
//	c) 非 nil 指针槽都指向合法 node（canary 正确，无垃圾）；
//	d) 增长后新写入的局部（postN/postW/postS）不受影响。
//
// 函数入口注册的 defer 在返回时再通过栈指针读取这些局部。
//
// 变体：loopBigFrame 每次迭代声明 32KB 大局部数组并校验每轮重新零化；
// 每轮 mask 在“单 bit / 全 1 / 全 0 / seed 随机”之间轮换，覆盖不同区间形态。
// 独立 gcChurn 并发。所有规格之内的读取均为合法零值读取，不制造 UB。
package main

import (
	"fmt"
	"os"
	"runtime"
	"runtime/debug"
	"strconv"
	"sync"
	"sync/atomic"
	"time"
	"unsafe"
)

const (
	scenario = "z07_zerorange_mixed_frame"
	nodeN    = 512
	zTag     = "z07"
)

type znode struct {
	id  int64
	v   int64
	tag string
	_   int64
}

type zNested struct {
	ptrs [4]*znode
	s    string
	n    int64
	b    bool
	_    [7]byte
}

type zPair struct {
	key uint64
	ptr *znode
}

// frameFootprint 仅用于 preflight 估算大帧字节数（不参与运行逻辑）。
type frameFootprint struct {
	pa     [16]*znode
	ps     [8]string
	sl     []*znode
	ifa    interface{}
	mp     map[int64]*znode
	nest   [6]zNested
	boolA  [64]bool
	ints   [32]int64
	bytesA [4096]byte
	words  [1024]uint64
	pairs  [512]zPair
	big    [2048]int64
	strarr [16]string
	ifarr  [8]interface{}
	f64    [64]float64
	cplx   [32]complex128
	i16    [64]int16
	i8     [128]int8
	u32    [64]uint32
}

type worker struct {
	id         int64
	seed       uint64
	nodes      []*znode
	stringsArr []string
	smallSlice []*znode
	smallMap   map[int64]*znode
	growDepth  int

	rounds  int64
	gcCalls int64
	checks  int64
	sink    int64
}

func mix64(x uint64) uint64 {
	x += 0x9E3779B97F4A7C15
	x = (x ^ (x >> 30)) * 0xBF58476D1CE4E5B9
	x = (x ^ (x >> 27)) * 0x94D049BB133111EB
	x ^= x >> 31
	return x
}

func nodeWant(seed uint64, id int64) int64 {
	v := int64(mix64(seed^uint64(id)*0xD1B54A32D192ED03^0x7777) & 0x7FFFFFFFFFFFFFFF)
	if v == 0 {
		v = 0x2545F4914F6CDD1D
	}
	return v
}

func (w *worker) init() {
	w.nodes = make([]*znode, nodeN)
	for i := range w.nodes {
		w.nodes[i] = &znode{id: int64(i), v: nodeWant(w.seed, int64(i)), tag: zTag}
	}
	w.stringsArr = make([]string, 8)
	for i := range w.stringsArr {
		w.stringsArr[i] = fmt.Sprintf("z07-%d-%d", w.id, i)
	}
	w.smallSlice = w.nodes[:8:8]
	w.smallMap = make(map[int64]*znode, 8)
	for i := 0; i < 8; i++ {
		w.smallMap[int64(i)] = w.nodes[i]
	}
}

// frameMask 按轮次轮换：单 bit（逐个区间形态）/ 全 1 / 全 0 / seed 随机。
func frameMask(seed uint64, r int64) uint64 {
	switch r % 4 {
	case 0:
		return 1 << uint((uint64(r)+seed)%11)
	case 1:
		return 0x7FF
	case 2:
		return 0
	default:
		return mix64(seed*0x9E3779B97F4A7C15+uint64(r)*0xD1B54A32D192ED03+0xA5A5A5A5) & 0x7FF
	}
}

// checkMixedFrame 校验全部 19 组局部：mask 未置位的必须为零值，置位的必须等于
// 初始化 canary；非 nil 指针必须指向合法 node。
//
//go:noinline
func (w *worker) checkMixedFrame(phase string, r int64, mask uint64,
	pa *[16]*znode, ps *[8]string, sl *[]*znode, ifa *interface{}, mp *map[int64]*znode,
	nest *[6]zNested, boolA *[64]bool, ints *[32]int64, bytesA *[4096]byte,
	words *[1024]uint64, pairs *[512]zPair, big *[2048]int64, strarr *[16]string,
	ifarr *[8]interface{}, f64 *[64]float64, cplx *[32]complex128,
	i16 *[64]int16, i8 *[128]int8, u32 *[64]uint32) {

	rr := int(uint64(r) % uint64(len(w.nodes)))
	ctx := func(format string, args ...interface{}) string {
		return fmt.Sprintf("phase=%s worker=%d round=%d mask=%#x ", phase, w.id, r, mask) + fmt.Sprintf(format, args...)
	}

	// a) 指针数组：bit0 初始化偶数槽，bit1 初始化奇数槽 —— 未置位槽必须 nil。
	for i := 0; i < 16; i++ {
		wantIdx := -1
		if mask&1 != 0 && i%2 == 0 {
			wantIdx = (i/2*3 + rr) % len(w.nodes)
		} else if mask&2 != 0 && i%2 == 1 {
			wantIdx = ((i/2)*5 + 1 + rr) % len(w.nodes)
		}
		n := pa[i]
		if wantIdx < 0 {
			if n != nil {
				failf("%s: 未初始化分支 pa[%d] 非 nil (id=%d)", ctx(""), i, n.id)
			}
			continue
		}
		if n == nil || n != w.nodes[wantIdx] {
			failf("%s: pa[%d] 指针丢失/错误 (got=%p want=%p)", ctx(""), i, n, w.nodes[wantIdx])
		}
		w.checkNode(n, int64(wantIdx), ctx("pa[%d]", i))
	}

	// b) 字符串：bit2。
	for i := 0; i < 8; i++ {
		want := ""
		if mask&4 != 0 {
			want = w.stringsArr[(i+rr)%len(w.stringsArr)]
		}
		if ps[i] != want {
			failf("%s: ps[%d]=%q want=%q", ctx(""), i, ps[i], want)
		}
	}
	for i := 0; i < 16; i++ {
		want := ""
		if mask&4 != 0 {
			want = w.stringsArr[(i*3+rr)%len(w.stringsArr)]
		}
		if strarr[i] != want {
			failf("%s: strarr[%d]=%q want=%q", ctx(""), i, strarr[i], want)
		}
	}

	// c) uint64 数组：bit3 初始化前半。
	for i := 0; i < 1024; i++ {
		want := uint64(0)
		if mask&8 != 0 && i < 512 {
			want = uint64(i) ^ w.seed
		}
		if words[i] != want {
			failf("%s: words[%d]=%#x want=%#x", ctx(""), i, words[i], want)
		}
	}

	// d) 嵌套 struct 数组：bit4 初始化前 3 个元素。
	for i := 0; i < 6; i++ {
		nz := nest[i]
		if mask&16 == 0 || i >= 3 {
			if nz.ptrs != [4]*znode{} || nz.s != "" || nz.n != 0 || nz.b {
				failf("%s: 未初始化分支 nest[%d] 非零值 (%v s=%q n=%d b=%v)", ctx(""), i, nz.ptrs, nz.s, nz.n, nz.b)
			}
			continue
		}
		for j := 0; j < 4; j++ {
			idx := (i*4 + j + rr) % len(w.nodes)
			if nz.ptrs[j] == nil || nz.ptrs[j] != w.nodes[idx] {
				failf("%s: nest[%d].ptrs[%d] 丢失/错误 (got=%p want=%p)", ctx(""), i, j, nz.ptrs[j], w.nodes[idx])
			}
			w.checkNode(nz.ptrs[j], int64(idx), ctx("nest[%d].ptrs[%d]", i, j))
		}
		if nz.s != w.stringsArr[i%len(w.stringsArr)] || nz.n != int64(i*11)+r || nz.b != (i%2 == 0) {
			failf("%s: nest[%d] 内容损坏 (s=%q n=%d b=%v)", ctx(""), i, nz.s, nz.n, nz.b)
		}
	}

	// e) int 标量数组：bit5。
	for i := 0; i < 32; i++ {
		want := int64(0)
		if mask&32 != 0 {
			want = int64(i*7) + r + 1
		}
		if ints[i] != want {
			failf("%s: ints[%d]=%d want=%d", ctx(""), i, ints[i], want)
		}
	}
	for i := 0; i < 64; i++ {
		want := int16(0)
		if mask&32 != 0 {
			want = int16(i*3 + 1)
		}
		if i16[i] != want {
			failf("%s: i16[%d]=%d want=%d", ctx(""), i, i16[i], want)
		}
		want32 := uint32(0)
		if mask&32 != 0 {
			want32 = uint32(i*5 + 7)
		}
		if u32[i] != want32 {
			failf("%s: u32[%d]=%d want=%d", ctx(""), i, u32[i], want32)
		}
	}

	// f) struct 数组：bit6 初始化前 256 项。
	for i := 0; i < 512; i++ {
		p := pairs[i]
		if mask&64 == 0 || i >= 256 {
			if p.key != 0 || p.ptr != nil {
				failf("%s: 未初始化分支 pairs[%d] 非零值 (key=%#x ptr=%p)", ctx(""), i, p.key, p.ptr)
			}
			continue
		}
		wantKey := uint64(i*3) + 1
		if p.key != wantKey {
			failf("%s: pairs[%d].key=%#x want=%#x", ctx(""), i, p.key, wantKey)
		}
		idx := (i*7 + rr) % len(w.nodes)
		if p.ptr == nil || p.ptr != w.nodes[idx] {
			failf("%s: pairs[%d].ptr 丢失/错误 (got=%p want=%p)", ctx(""), i, p.ptr, w.nodes[idx])
		}
		w.checkNode(p.ptr, int64(idx), ctx("pairs[%d].ptr", i))
	}

	// g) int64 大数组：bit7 初始化前半。
	for i := 0; i < 2048; i++ {
		want := int64(0)
		if mask&128 != 0 && i < 1024 {
			want = int64(i)*3 + r + 1
		}
		if big[i] != want {
			failf("%s: big[%d]=%d want=%d", ctx(""), i, big[i], want)
		}
	}

	// h) slice/interface/map/interface 数组：bit8。
	if mask&256 == 0 {
		if *sl != nil {
			failf("%s: 未初始化分支 sl 非 nil (len=%d)", ctx(""), len(*sl))
		}
		if *ifa != nil {
			failf("%s: 未初始化分支 ifa 非 nil (%T)", ctx(""), *ifa)
		}
		if *mp != nil {
			failf("%s: 未初始化分支 mp 非 nil (len=%d)", ctx(""), len(*mp))
		}
		for i := 0; i < 8; i++ {
			if ifarr[i] != nil {
				failf("%s: 未初始化分支 ifarr[%d] 非 nil", ctx(""), i)
			}
		}
	} else {
		if len(*sl) != 8 {
			failf("%s: sl len=%d want=8", ctx(""), len(*sl))
		}
		for i := 0; i < 8; i++ {
			if (*sl)[i] == nil || (*sl)[i] != w.nodes[i] {
				failf("%s: sl[%d] 丢失/错误 (got=%p want=%p)", ctx(""), i, (*sl)[i], w.nodes[i])
			}
			w.checkNode((*sl)[i], int64(i), ctx("sl[%d]", i))
		}
		n, ok := (*ifa).(*znode)
		if !ok || n == nil || n != w.nodes[rr] {
			failf("%s: ifa 丢失/错误 (got=%T want=%p)", ctx(""), *ifa, w.nodes[rr])
		}
		w.checkNode(n, int64(rr), ctx("ifa"))
		if len(*mp) != 8 {
			failf("%s: mp len=%d want=8", ctx(""), len(*mp))
		}
		for i := 0; i < 8; i++ {
			v, ok := (*mp)[int64(i)]
			if !ok || v == nil || v != w.nodes[i] {
				failf("%s: mp[%d] 丢失/错误 (got=%p want=%p)", ctx(""), i, v, w.nodes[i])
			}
		}
		for i := 0; i < 8; i++ {
			if i%2 == 0 {
				n, ok := ifarr[i].(*znode)
				if !ok || n == nil || n != w.nodes[i] {
					failf("%s: ifarr[%d] 丢失/错误 (got=%T want=%p)", ctx(""), i, ifarr[i], w.nodes[i])
				}
				w.checkNode(n, int64(i), ctx("ifarr[%d]", i))
			} else if ifarr[i] != nil {
				failf("%s: 未初始化分支 ifarr[%d] 非 nil", ctx(""), i)
			}
		}
	}

	// i) byte/float/complex 数组：bit9。
	for i := 0; i < 4096; i++ {
		want := byte(0)
		if mask&512 != 0 {
			want = byte(i*13) + byte(r)
		}
		if bytesA[i] != want {
			failf("%s: bytesA[%d]=%#x want=%#x", ctx(""), i, bytesA[i], want)
		}
	}
	for i := 0; i < 64; i++ {
		want := 0.0
		if mask&512 != 0 {
			want = float64(i) + 0.5
		}
		if f64[i] != want {
			failf("%s: f64[%d]=%v want=%v", ctx(""), i, f64[i], want)
		}
	}
	for i := 0; i < 32; i++ {
		want := complex(0, 0)
		if mask&512 != 0 {
			want = complex(float64(i), float64(-i))
		}
		if cplx[i] != want {
			failf("%s: cplx[%d]=%v want=%v", ctx(""), i, cplx[i], want)
		}
	}

	// j) bool 与 int8 数组：bit10。
	for i := 0; i < 64; i++ {
		want := false
		if mask&1024 != 0 {
			want = i%3 == 0
		}
		if boolA[i] != want {
			failf("%s: boolA[%d]=%v want=%v", ctx(""), i, boolA[i], want)
		}
	}
	for i := 0; i < 128; i++ {
		want := int8(0)
		if mask&1024 != 0 {
			want = int8(i + 1)
		}
		if i8[i] != want {
			failf("%s: i8[%d]=%d want=%d", ctx(""), i, i8[i], want)
		}
	}

	w.checks++
}

//go:noinline
func (w *worker) checkNode(n *znode, idx int64, ctx string) {
	if n == nil || n.id != idx || n.v != nodeWant(w.seed, idx) || n.tag != zTag {
		var gotID, gotV int64 = -1, -1
		var gotTag string
		if n != nil {
			gotID, gotV, gotTag = n.id, n.v, n.tag
		}
		failf("worker=%d seed=%d %s: 指针槽垃圾/内容损坏 (id=%d v=%d tag=%q wantIdx=%d)",
			w.id, w.seed, ctx, gotID, gotV, gotTag, idx)
	}
}

// growDeep 在 mixedFrame 中部强制 morestack 栈增长/复制。
// 递归返回后继续使用旧帧的指针/数值槽，确保深递归期间这些槽是活的。
//
//go:noinline
func (w *worker) growDeep(depth int, seed uint64) int64 {
	var pad [48]*znode
	var nums [64]uint64
	idx := depth % len(w.nodes)
	pad[0] = w.nodes[idx]
	pad[47] = w.nodes[(idx+1)%len(w.nodes)]
	nums[0] = seed + uint64(depth)
	nums[63] = uint64(depth) + 1
	if depth > 0 {
		v := w.growDeep(depth-1, seed)
		if pad[0] == nil || pad[0] != w.nodes[idx] || pad[0].v != nodeWant(w.seed, int64(idx)) {
			failf("worker=%d seed=%d growDeep depth=%d: 递归返回后 pad[0] 损坏", w.id, w.seed, depth)
		}
		if pad[47] == nil || pad[47] != w.nodes[(idx+1)%len(w.nodes)] {
			failf("worker=%d seed=%d growDeep depth=%d: 递归返回后 pad[47] 损坏", w.id, w.seed, depth)
		}
		if nums[0] != seed+uint64(depth) || nums[63] != uint64(depth)+1 {
			failf("worker=%d seed=%d growDeep depth=%d: 递归返回后 nums 槽损坏", w.id, w.seed, depth)
		}
		return v + int64(nums[0]&1)
	}
	return int64(nums[0] & 1)
}

// mixedFrame 是大帧本体：11 个分支组合的部分初始化 + 中部深递归栈增长。
//
//go:noinline
func (w *worker) mixedFrame(r int64, mask uint64) (sum int64) {
	var (
		pa     [16]*znode
		ps     [8]string
		sl     []*znode
		ifa    interface{}
		mp     map[int64]*znode
		nest   [6]zNested
		boolA  [64]bool
		ints   [32]int64
		bytesA [4096]byte
		words  [1024]uint64
		pairs  [512]zPair
		big    [2048]int64
		strarr [16]string
		ifarr  [8]interface{}
		f64    [64]float64
		cplx   [32]complex128
		i16    [64]int16
		i8     [128]int8
		u32    [64]uint32
		postN  *znode
		postW  [4]uint64
		postS  string
	)
	rr := int(uint64(r) % uint64(len(w.nodes)))

	// defer 在返回时通过栈指针再读这些大帧局部（栈增长后指针必须被正确调整）。
	defer w.checkMixedFrame("defer", r, mask,
		&pa, &ps, &sl, &ifa, &mp, &nest, &boolA, &ints, &bytesA, &words,
		&pairs, &big, &strarr, &ifarr, &f64, &cplx, &i16, &i8, &u32)

	// ---- 沿 seed 派生分支做部分初始化（留下多个未初始化区间） ----
	if mask&1 != 0 {
		for i := 0; i < 8; i++ {
			pa[i*2] = w.nodes[(i*3+rr)%len(w.nodes)]
		}
	}
	if mask&2 != 0 {
		for i := 0; i < 8; i++ {
			pa[i*2+1] = w.nodes[(i*5+1+rr)%len(w.nodes)]
		}
	}
	if mask&4 != 0 {
		for i := range ps {
			ps[i] = w.stringsArr[(i+rr)%len(w.stringsArr)]
		}
		for i := range strarr {
			strarr[i] = w.stringsArr[(i*3+rr)%len(w.stringsArr)]
		}
	}
	if mask&8 != 0 {
		for i := 0; i < 512; i++ {
			words[i] = uint64(i) ^ w.seed
		}
	}
	if mask&16 != 0 {
		for i := 0; i < 3; i++ {
			var nz zNested
			for j := 0; j < 4; j++ {
				nz.ptrs[j] = w.nodes[(i*4+j+rr)%len(w.nodes)]
			}
			nz.s = w.stringsArr[i%len(w.stringsArr)]
			nz.n = int64(i*11) + r
			nz.b = i%2 == 0
			nest[i] = nz
		}
	}
	if mask&32 != 0 {
		for i := 0; i < 32; i++ {
			ints[i] = int64(i*7) + r + 1
		}
		for i := 0; i < 64; i++ {
			i16[i] = int16(i*3 + 1)
			u32[i] = uint32(i*5 + 7)
		}
	}
	if mask&64 != 0 {
		for i := 0; i < 256; i++ {
			pairs[i] = zPair{key: uint64(i*3) + 1, ptr: w.nodes[(i*7+rr)%len(w.nodes)]}
		}
	}
	if mask&128 != 0 {
		for i := 0; i < 1024; i++ {
			big[i] = int64(i)*3 + r + 1
		}
	}
	if mask&256 != 0 {
		sl = w.smallSlice
		ifa = w.nodes[rr]
		mp = w.smallMap
		for i := 0; i < 8; i += 2 {
			ifarr[i] = w.nodes[i]
		}
	}
	if mask&512 != 0 {
		for i := 0; i < 4096; i++ {
			bytesA[i] = byte(i*13) + byte(r)
		}
		for i := 0; i < 64; i++ {
			f64[i] = float64(i) + 0.5
		}
		for i := 0; i < 32; i++ {
			cplx[i] = complex(float64(i), float64(-i))
		}
	}
	if mask&1024 != 0 {
		for i := 0; i < 64; i++ {
			boolA[i] = i%3 == 0
		}
		for i := 0; i < 128; i++ {
			i8[i] = int8(i + 1)
		}
	}

	// ---- 增长前 GC：扫描当前大帧 ----
	runtime.GC()
	w.gcCalls++
	gcYield()
	w.checkMixedFrame("pre-growth", r, mask,
		&pa, &ps, &sl, &ifa, &mp, &nest, &boolA, &ints, &bytesA, &words,
		&pairs, &big, &strarr, &ifarr, &f64, &cplx, &i16, &i8, &u32)

	// ---- 函数中部深递归：强制 morestack 栈增长/复制整个大帧 ----
	g := w.growDeep(w.growDepth, w.seed+uint64(r))

	// ---- 增长后 GC：扫描复制后的帧 ----
	runtime.GC()
	w.gcCalls++
	gcYield()
	w.checkMixedFrame("post-growth", r, mask,
		&pa, &ps, &sl, &ifa, &mp, &nest, &boolA, &ints, &bytesA, &words,
		&pairs, &big, &strarr, &ifarr, &f64, &cplx, &i16, &i8, &u32)

	// ---- 增长后新写入的局部：必须不受栈复制影响 ----
	postIdx := (rr + 7) % len(w.nodes)
	postN = w.nodes[postIdx]
	postS = w.stringsArr[(rr+3)%len(w.stringsArr)]
	postW[0] = w.seed ^ uint64(r)
	for i := 1; i < len(postW); i++ {
		postW[i] = postW[i-1]*6364136223846793005 + 1
	}
	if postN != w.nodes[postIdx] || postN.v != nodeWant(w.seed, int64(postIdx)) || postN.tag != zTag {
		failf("worker=%d seed=%d round=%d: 增长后新写入 postN 损坏", w.id, w.seed, r)
	}
	if postS != w.stringsArr[(rr+3)%len(w.stringsArr)] {
		failf("worker=%d seed=%d round=%d: 增长后新写入 postS 损坏 (%q)", w.id, w.seed, r, postS)
	}
	wantW := w.seed ^ uint64(r)
	for i := 0; i < len(postW); i++ {
		if postW[i] != wantW {
			failf("worker=%d seed=%d round=%d: 增长后新写入 postW[%d]=%#x want=%#x", w.id, w.seed, r, i, postW[i], wantW)
		}
		if i+1 < len(postW) {
			wantW = wantW*6364136223846793005 + 1
		}
	}

	sum = g
	for i := 0; i < 16; i++ {
		if pa[i] != nil {
			sum += pa[i].v
		}
	}
	sum += big[0] + int64(words[0]&1) + int64(len(ps)) + int64(len(strarr))
	if ifa != nil {
		if n, ok := ifa.(*znode); ok {
			sum += n.id
		}
	}
	return sum
}

// loopBigFrame: 循环体内声明 32KB 大局部数组，校验每次迭代都重新零化；
// 每次迭代只做部分写入，并校验尾部未初始化区间为零。
//
//go:noinline
func (w *worker) loopBigFrame(seed uint64) (sum int64) {
	for it := 0; it < 6; it++ {
		var big [1024]zPair
		var nums [2048]int64
		for i := 0; i < len(big); i++ {
			if big[i].key != 0 || big[i].ptr != nil {
				failf("worker=%d seed=%d loop-it=%d: 未初始化 big[%d] 非零值 (key=%#x ptr=%p)",
					w.id, w.seed, it, i, big[i].key, big[i].ptr)
			}
		}
		for i := 0; i < len(nums); i++ {
			if nums[i] != 0 {
				failf("worker=%d seed=%d loop-it=%d: 未初始化 nums[%d]=%d", w.id, w.seed, it, i, nums[i])
			}
		}
		k := int((seed+uint64(it)*7)%64) + 1
		for i := 0; i < k; i++ {
			big[i] = zPair{key: 0xAB000000 | uint64(i)<<8 | uint64(it), ptr: w.nodes[(i+it)%len(w.nodes)]}
			nums[i] = int64(it*1000+i) + 1
		}
		for i := k; i < len(big); i++ {
			if big[i].key != 0 || big[i].ptr != nil {
				failf("worker=%d seed=%d loop-it=%d: 尾部未初始化 big[%d] 非零值 (key=%#x ptr=%p)",
					w.id, w.seed, it, i, big[i].key, big[i].ptr)
			}
		}
		for i := k; i < len(nums); i++ {
			if nums[i] != 0 {
				failf("worker=%d seed=%d loop-it=%d: 尾部未初始化 nums[%d]=%d", w.id, w.seed, it, i, nums[i])
			}
		}
		for i := 0; i < k; i++ {
			wantKey := 0xAB000000 | uint64(i)<<8 | uint64(it)
			idx := (i + it) % len(w.nodes)
			if big[i].key != wantKey || big[i].ptr == nil || big[i].ptr != w.nodes[idx] {
				failf("worker=%d seed=%d loop-it=%d: big[%d] canary 损坏 (key=%#x ptr=%p want=%p)",
					w.id, w.seed, it, i, big[i].key, big[i].ptr, w.nodes[idx])
			}
			w.checkNode(big[i].ptr, int64(idx), "loopBigFrame")
			if nums[i] != int64(it*1000+i)+1 {
				failf("worker=%d seed=%d loop-it=%d: nums[%d]=%d want=%d", w.id, w.seed, it, i, nums[i], int64(it*1000+i)+1)
			}
		}
		if it%2 == 0 {
			runtime.GC()
			w.gcCalls++
			gcYield()
			for i := 0; i < k; i++ {
				idx := (i + it) % len(w.nodes)
				if big[i].ptr == nil || big[i].ptr != w.nodes[idx] || big[i].ptr.v != nodeWant(w.seed, int64(idx)) {
					failf("worker=%d seed=%d loop-it=%d: GC 后 big[%d].ptr 损坏", w.id, w.seed, it, i)
				}
				if nums[i] != int64(it*1000+i)+1 {
					failf("worker=%d seed=%d loop-it=%d: GC 后 nums[%d] 损坏", w.id, w.seed, it, i)
				}
			}
		}
		for i := 0; i < k; i++ {
			sum += nums[i] + int64(big[i].key&1)
		}
	}
	return sum
}

func (w *worker) round(r int64) {
	mask := frameMask(w.seed, r)
	w.sink += w.mixedFrame(r, mask)
	w.sink += w.loopBigFrame(w.seed + uint64(r)*0x9E3779B97F4A7C15)
	w.rounds++
	atomic.AddInt64(&beats, 1)
}

func (w *worker) run(seconds int, deadline time.Time, fixed int64) {
	atomic.AddInt64(&started, 1)
	defer atomic.AddInt64(&finished, 1)
	w.init()
	runtime.GC()
	gcYield()
	r := int64(0)
	for {
		if seconds > 0 {
			if time.Now().After(deadline) {
				break
			}
		} else if r >= fixed {
			break
		}
		w.round(r)
		r++
	}
	runtime.GC()
	gcYield()
	if w.rounds == 0 || w.checks == 0 {
		failf("worker=%d seed=%d: no rounds/checks completed", w.id, w.seed)
	}
}

var (
	beats    int64
	gcCycles int64
	started  int64
	finished int64
	gSeed    uint64
)

func failf(format string, args ...interface{}) {
	fmt.Printf("FAIL: seed=%d "+format+"\n", append([]interface{}{gSeed}, args...)...)
	buf := make([]byte, 1<<20)
	n := runtime.Stack(buf, true)
	fmt.Fprintf(os.Stderr, "%s", buf[:n])
	os.Exit(1)
}

func envInt(key string, def, min, max int) int {
	s := os.Getenv(key)
	if s == "" {
		return def
	}
	v, err := strconv.Atoi(s)
	if err != nil || v < min {
		return def
	}
	if v > max {
		return max
	}
	return v
}

func envSeed(def uint64) uint64 {
	s := os.Getenv("UAF_SEED")
	if s == "" {
		return def
	}
	v, err := strconv.ParseUint(s, 10, 64)
	if err != nil || v == 0 {
		return def
	}
	return v
}

// gcYield: GOMAXPROCS=1 时在显式 GC 之后让出 P，避免多 worker 在单 P 上
// 紧邻调用 runtime.GC 造成排队饥饿（与 gcChurn 的让出同理）。
func gcYield() {
	if runtime.GOMAXPROCS(0) == 1 {
		runtime.Gosched()
		time.Sleep(time.Millisecond)
	}
}

func gcChurn(stop <-chan struct{}, wg *sync.WaitGroup) {
	defer wg.Done()
	var sink [][]byte
	for {
		select {
		case <-stop:
			return
		default:
		}
		for i := 0; i < 8; i++ {
			b := make([]byte, 2048)
			b[0] = byte(i)
			sink = append(sink[:0], b)
		}
		for i := range sink {
			sink[i] = nil
		}
		sink = sink[:0]
		runtime.GC()
		atomic.AddInt64(&gcCycles, 1)
		// window（GOMAXPROCS=1+STW）下无休眠 GC 循环会饿死 worker/watchdog。
		if runtime.GOMAXPROCS(0) == 1 {
			runtime.Gosched()
			time.Sleep(time.Millisecond)
		}
	}
}

func watchdog(interval time.Duration, stop <-chan struct{}, wg *sync.WaitGroup) {
	defer wg.Done()
	last := atomic.LoadInt64(&beats)
	lastChange := time.Now()
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-stop:
			return
		case <-t.C:
			cur := atomic.LoadInt64(&beats)
			if cur != last {
				last = cur
				lastChange = time.Now()
				continue
			}
			if time.Since(lastChange) > 6*interval {
				failf("phase=watchdog seed=%d: 无 worker 进展 %s (beats=%d)", gSeed, time.Since(lastChange), cur)
			}
		}
	}
}

func waitGoroutines(base int, grace time.Duration) int {
	giveUp := time.Now().Add(grace)
	for {
		n := runtime.NumGoroutine()
		if n <= base || time.Now().After(giveUp) {
			return n
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func setupCrashOutput() {
	p := os.Getenv("STRESS_CRASH_FILE")
	if p == "" {
		return
	}
	f, err := os.OpenFile(p, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return
	}
	_ = debug.SetCrashOutput(f, debug.CrashOptions{})
}

func main() {
	setupCrashOutput()
	seconds := envInt("STRESS_SECONDS", 60, 0, 86400)
	workers := envInt("STRESS_WORKERS", 4, 1, 32)
	hb := envInt("STRESS_HEARTBEAT", 5, 1, 60)
	seed := envSeed(1)
	fixed := int64(envInt("STRESS_FIXED_ROUNDS", 16, 1, 1000000))
	growDepth := envInt("STRESS_GROW_DEPTH", 512, 16, 4096)
	gSeed = seed

	fmt.Printf("scenario=%s phase=preflight go=%s goos=%s goarch=%s gomaxprocs=%d seed=%d seconds=%d workers=%d fixed_rounds=%d heartbeat=%ds frame_estimate=%dB grow_depth=%d locals=19 masks=11bit\n",
		scenario, runtime.Version(), runtime.GOOS, runtime.GOARCH, runtime.GOMAXPROCS(0), seed, seconds, workers, fixed, hb,
		unsafe.Sizeof(frameFootprint{}), growDepth)

	baseGoroutines := runtime.NumGoroutine()
	deadline := time.Now().Add(time.Duration(seconds) * time.Second)
	ws := make([]*worker, workers)
	for i := 0; i < workers; i++ {
		ws[i] = &worker{id: int64(i), seed: seed*1000 + uint64(i) + 1, growDepth: growDepth}
	}

	var wg sync.WaitGroup
	for _, w := range ws {
		wg.Add(1)
		go func(w *worker) {
			defer wg.Done()
			w.run(seconds, deadline, fixed)
		}(w)
	}
	stop := make(chan struct{})
	var aux sync.WaitGroup
	aux.Add(1)
	go gcChurn(stop, &aux)
	aux.Add(1)
	go watchdog(time.Duration(hb)*time.Second, stop, &aux)

	wg.Wait()
	close(stop)
	aux.Wait()

	if s, f := atomic.LoadInt64(&started), atomic.LoadInt64(&finished); s != int64(workers) || f != s {
		failf("phase=final seed=%d: started=%d finished=%d workers=%d", seed, s, f, workers)
	}

	var rounds, checks, gcs, sink int64
	for _, w := range ws {
		rounds += w.rounds
		checks += w.checks
		gcs += w.gcCalls
		sink += w.sink
		if seconds == 0 && w.rounds != fixed {
			failf("phase=final seed=%d worker=%d: rounds=%d want=%d", seed, w.id, w.rounds, fixed)
		}
	}
	if rounds == 0 || checks == 0 {
		failf("phase=final seed=%d: no rounds/checks completed (rounds=%d checks=%d)", seed, rounds, checks)
	}

	ng := waitGoroutines(baseGoroutines, 5*time.Second)
	if ng > baseGoroutines+4 {
		failf("phase=final seed=%d: goroutines baseline=%d after=%d: goroutine leak", seed, baseGoroutines, ng)
	}

	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	fmt.Printf("metrics: rounds=%d frame_checks=%d explicit_gc=%d gc_churn=%d workers=%d frame_estimate=%dB grow_depth=%d sink=%d heap_alloc=%d heap_objects=%d num_gc=%d goroutines=%d\n",
		rounds, checks, gcs, atomic.LoadInt64(&gcCycles), workers, unsafe.Sizeof(frameFootprint{}), growDepth, sink,
		m.HeapAlloc, m.HeapObjects, m.NumGC, ng)
	fmt.Printf("STRESS PASS %s\n", scenario)
}
