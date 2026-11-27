// Y08: 全局巨型 gcprog 风暴（P1，覆盖 L18/L13）。
// 对准 B14/B15（issue1002 路径）：巨型全局数组的指针位图由 gcprog 编码/解码，
// 编码、解码或缓存任一环节出错 → 全局根扫描漏标（对象被提前回收 → canary 失配）
// 或多标（pad 非指针字被当指针扫 → GC 直接崩 "bad pointer"）。
//   - 核心全局（手写）：gCorePtr [1<<16]*node、gCoreKv [1<<14]Kv（k string /
//     v *node / s []byte / e any 混合）、gCoreChan chan [512]Kv、gCoreMap
//     map[string][64]*node、gCoreInner [1<<12]Inner（嵌套 struct 数组，多层指针位）；
//   - 生成全局（tools/gen_global_types.py）：36 种不同布局的巨型全局数组
//     （gofmt 后入库，确定性可重跑；总指针位 ≥ 100 万、总量 ≤ 256MB 由脚本保证）；
//   - 运行时：并发 worker 分区改写全局各槽（老→新指针、nil↔对象交替、pad 字写入
//     高位非规范模式——位图多标会当场崩）× 每轮 GC；写入 → GC → 复核（写入后
//     立即 GC 复核 + 回环校验两段）；
//   - reflect.PointerTo/ArrayOf/SliceOf 大量并发构造打 typelinks（全局类型元数据
//     并发压力）+ size/ptrdata 与生成元数据确定性对照 + 周期 reflect.New 物化 gcdata；
//   - STRESS_SCALE=small|medium|full 控制每轮触达面（自测用 small）。
//
// 官方 Go 的 gcprog/typelinks 实现正确 → 恒 PASS；崩溃/漏标仅随 gollvm 出现。
package main

import (
	"fmt"
	"os"
	"reflect"
	"runtime"
	"runtime/debug"
	"strconv"
	"sync"
	"sync/atomic"
	"time"
	"unsafe"
)

const (
	scenario = "y08_global_gcprog_storm"

	corePtrElems   = 1 << 16
	coreKvElems    = 1 << 14
	coreInnerElems = 1 << 12
	chanKvN        = 512

	// chan 内容用固定 seed：任何 worker 收到任何发送者的值都能精确校验。
	chanSeed = uint64(0xC0DEC0DE5EED5EED)

	numCoreGlobals = 5

	fixedRoundsDefault = 40
)

type node struct {
	id  int64
	v   int64
	pad [2]uint64
}

// nodeMagic 与 u01 完全一致。
func nodeMagic(seed uint64, id int64) int64 {
	x := seed + uint64(id)*0x9E3779B97F4A7C15
	x ^= x >> 33
	x *= 0xff51afd7ed558ccd
	x ^= x >> 33
	x *= 0xc4ceb9fe1a85ec53
	x ^= x >> 33
	if x == 0 {
		x = 0x5DEECE66D
	}
	return int64(x)
}

func newNode(seed uint64, id int64) *node {
	return &node{id: id, v: nodeMagic(seed, id)}
}

//go:noinline
func checkNode(n *node, seed uint64, wantID int64, where string) {
	if n == nil {
		failf("%s: nil *node want id=%d（全局根扫描漏标/UAF）", where, wantID)
	}
	if n.id != wantID || n.v != nodeMagic(seed, n.id) {
		failf("%s: canary mismatch id=%d want=%d v=%#x want_v=%#x（漏标/复用）",
			where, n.id, wantID, n.v, nodeMagic(seed, n.id))
	}
	atomic.AddInt64(&canaryChecks, 1)
}

func nodeOrNil(seed uint64, id int64, mode int) *node {
	if mode%3 == 0 {
		return nil
	}
	return newNode(seed, id)
}

// typePtrData 读取运行时类型元数据的 ptrdata 字段（go1.24 internal/abi.Type
// 布局：Size_=off0、PtrBytes=off8，仅 64 位平台）。用于与生成器元数据做
// 确定性对照（B14/B15 前置：全局位图必须真实存在于类型元数据）。
func typePtrData(t reflect.Type) uintptr {
	if unsafe.Sizeof(uintptr(0)) != 8 {
		return 0 // 非 64 位平台跳过 ptrdata 对照（size 仍校验）
	}
	type iface struct {
		typ  unsafe.Pointer
		data unsafe.Pointer
	}
	i := *(*iface)(unsafe.Pointer(&t))
	return *(*uintptr)(unsafe.Add(i.data, 8))
}

// Kv：k string / v *node / s []byte / e any 混合（5 个指针位）。
type Kv struct {
	k string
	v *node
	s []byte
	e any
}

// Mid/Deep/Inner：嵌套 struct 数组，多层指针位（每 Inner 28 个指针位）。
type Mid struct {
	X *node
	Y [2]*node
	Z uint64
	W *node
}

type Deep struct {
	A [3]*node
	S []*node
	P *node
}

type Inner struct {
	P0 *node
	M0 [4]Mid
	D  [3]uint64
	P1 *node
	E  [2]Deep
	B  [16]byte
}

var (
	gCorePtr   [corePtrElems]*node
	gCoreKv    [coreKvElems]Kv
	gCoreChan  = make(chan [chanKvN]Kv, 8)
	gCoreMap   = make(map[string][64]*node)
	gCoreInner [coreInnerElems]Inner

	gMapMu sync.Mutex
)

// kvBytes：确定性字节模式（内容校验用）。
func kvBytes(id int64) []byte {
	b := make([]byte, 16)
	for i := range b {
		b[i] = byte(id*31 + int64(i)*7)
	}
	return b
}

func makeKv(seed uint64, id int64, key string, mode int) Kv {
	var kv Kv
	kv.k = key
	if mode%3 != 0 {
		kv.v = newNode(seed, id)
	}
	kv.s = kvBytes(id)
	switch mode % 4 {
	case 1:
		kv.e = newNode(seed, id+1_000_000)
	case 2:
		kv.e = kvBytes(id)
	case 3:
		kv.e = string(kvBytes(id))
	}
	return kv
}

//go:noinline
func checkKv(kv *Kv, seed uint64, id int64, key string, mode int, where string) {
	if kv.k != key {
		failf("%s: k=%q want=%q（Kv 槽被改写/漏标）", where, kv.k, key)
	}
	if mode%3 != 0 {
		checkNode(kv.v, seed, id, where+".v")
	} else if kv.v != nil {
		failf("%s: v 应为 nil", where)
	}
	b := kvBytes(id)
	if len(kv.s) != len(b) || kv.s[0] != b[0] || kv.s[15] != b[15] {
		failf("%s: s len=%d want=%d（Kv 槽被改写）", where, len(kv.s), len(b))
	}
	switch mode % 4 {
	case 0:
		if kv.e != nil {
			failf("%s: e 应为 nil got=%T", where, kv.e)
		}
	case 1:
		n, ok := kv.e.(*node)
		if !ok {
			failf("%s: e 类型=%T want *node", where, kv.e)
		}
		checkNode(n, seed, id+1_000_000, where+".e")
	case 2:
		sb, ok := kv.e.([]byte)
		if !ok || len(sb) != len(b) || sb[0] != b[0] || sb[15] != b[15] {
			failf("%s: e []byte 损坏 type=%T", where, kv.e)
		}
	case 3:
		s, ok := kv.e.(string)
		if !ok || s != string(b) {
			failf("%s: e string 损坏 type=%T", where, kv.e)
		}
	}
	atomic.AddInt64(&kvChecks, 1)
}

func fillInner(in *Inner, seed uint64, id int64, mode int) {
	in.P0 = nodeOrNil(seed, id+0, mode+0)
	for j := range in.M0 {
		m := &in.M0[j]
		mid := id + 10 + int64(j)*10
		m.X = nodeOrNil(seed, mid+0, mode+j)
		m.Y[0] = nodeOrNil(seed, mid+1, mode+j+1)
		m.Y[1] = nodeOrNil(seed, mid+2, mode+j+2)
		m.Z = uint64(mid) + 0x9E3779B97F4A7C15
		m.W = nodeOrNil(seed, mid+3, mode+j+3)
	}
	for j := range in.D {
		in.D[j] = uint64(id) + uint64(j)*0x9E3779B97F4A7C15
	}
	in.P1 = nodeOrNil(seed, id+1, mode+1)
	for j := range in.E {
		d := &in.E[j]
		did := id + 100 + int64(j)*10
		d.A[0] = nodeOrNil(seed, did+0, mode+j)
		d.A[1] = nodeOrNil(seed, did+1, mode+j+1)
		d.A[2] = nodeOrNil(seed, did+2, mode+j+2)
		d.S = make([]*node, 3)
		d.S[0] = nodeOrNil(seed, did+3, mode+j+3)
		d.S[1] = nodeOrNil(seed, did+4, mode+j+4)
		d.S[2] = nodeOrNil(seed, did+5, mode+j+5)
		d.P = nodeOrNil(seed, did+6, mode+j+6)
	}
	for j := range in.B {
		in.B[j] = byte(id + int64(j))
	}
}

//go:noinline
func checkInner(in *Inner, seed uint64, id int64, mode int, where string) {
	if mode%3 != 0 {
		checkNode(in.P0, seed, id+0, where+".P0")
	} else if in.P0 != nil {
		failf("%s: P0 应为 nil", where)
	}
	for j := range in.M0 {
		m := &in.M0[j]
		mid := id + 10 + int64(j)*10
		if (mode+j)%3 != 0 {
			checkNode(m.X, seed, mid+0, where+".M.X")
		} else if m.X != nil {
			failf("%s: M[%d].X 应为 nil", where, j)
		}
		if (mode+j+1)%3 != 0 {
			checkNode(m.Y[0], seed, mid+1, where+".M.Y0")
		} else if m.Y[0] != nil {
			failf("%s: M[%d].Y0 应为 nil", where, j)
		}
		if (mode+j+2)%3 != 0 {
			checkNode(m.Y[1], seed, mid+2, where+".M.Y1")
		} else if m.Y[1] != nil {
			failf("%s: M[%d].Y1 应为 nil", where, j)
		}
		if m.Z != uint64(mid)+0x9E3779B97F4A7C15 {
			failf("%s: M[%d].Z 被改写", where, j)
		}
		if (mode+j+3)%3 != 0 {
			checkNode(m.W, seed, mid+3, where+".M.W")
		} else if m.W != nil {
			failf("%s: M[%d].W 应为 nil", where, j)
		}
	}
	for j := range in.D {
		if in.D[j] != uint64(id)+uint64(j)*0x9E3779B97F4A7C15 {
			failf("%s: D[%d] 被改写", where, j)
		}
	}
	if (mode+1)%3 != 0 {
		checkNode(in.P1, seed, id+1, where+".P1")
	} else if in.P1 != nil {
		failf("%s: P1 应为 nil", where)
	}
	for j := range in.E {
		d := &in.E[j]
		did := id + 100 + int64(j)*10
		if (mode+j)%3 != 0 {
			checkNode(d.A[0], seed, did+0, where+".E.A0")
		} else if d.A[0] != nil {
			failf("%s: E[%d].A0 应为 nil", where, j)
		}
		if (mode+j+1)%3 != 0 {
			checkNode(d.A[1], seed, did+1, where+".E.A1")
		} else if d.A[1] != nil {
			failf("%s: E[%d].A1 应为 nil", where, j)
		}
		if (mode+j+2)%3 != 0 {
			checkNode(d.A[2], seed, did+2, where+".E.A2")
		} else if d.A[2] != nil {
			failf("%s: E[%d].A2 应为 nil", where, j)
		}
		if len(d.S) != 3 {
			failf("%s: E[%d].S len=%d want=3", where, j, len(d.S))
		}
		for k := range d.S {
			if (mode+j+3+k)%3 != 0 {
				checkNode(d.S[k], seed, did+3+int64(k), where+".E.S")
			} else if d.S[k] != nil {
				failf("%s: E[%d].S[%d] 应为 nil", where, j, k)
			}
		}
		if (mode+j+6)%3 != 0 {
			checkNode(d.P, seed, did+6, where+".E.P")
		} else if d.P != nil {
			failf("%s: E[%d].P 应为 nil", where, j)
		}
	}
	for j := range in.B {
		if in.B[j] != byte(id+int64(j)) {
			failf("%s: B[%d] 被改写", where, j)
		}
	}
	atomic.AddInt64(&innerChecks, 1)
}

// ---------------- 生成全局的通用读写（unsafe 布局访问） ----------------

// 生成元素布局：[r]{ [g]*node ; [d]uint64 }，最后一组以 pad 结尾。
func genPtrOffset(m genMeta, i int) uintptr {
	grp := i / m.g
	return uintptr(grp*(m.g+m.d)+(i%m.g)) * 8
}

func genPadOffset(m genMeta, j int) uintptr {
	grp := j / m.d
	return uintptr(grp*(m.g+m.d)+m.g+(j%m.d)) * 8
}

// padWord：高位非规范模式（0xDEAD...）——若位图把 pad 字当指针扫，GC 立即崩（多标探针）。
func padWord(seed uint64, tag int64, j int) uint64 {
	lo := (seed*0x9E3779B97F4A7C15 + uint64(tag)*0x2545F4914F6CDD1D + uint64(j)*0x9E3779B97F4A7C15) & 0x0000FFFFFFFFFFFF
	return 0xDEAD000000000000 | lo
}

func writeGenElem(ep unsafe.Pointer, m genMeta, seed uint64, tag int64, idBase int64) {
	for i := 0; i < m.r*m.g; i++ {
		mode := int((tag + int64(i)) % 7)
		*(**node)(unsafe.Add(ep, genPtrOffset(m, i))) = nodeOrNil(seed, idBase+int64(i), mode)
	}
	for j := 0; j < m.r*m.d; j++ {
		*(*uint64)(unsafe.Add(ep, genPadOffset(m, j))) = padWord(seed, tag, j)
	}
}

//go:noinline
func checkGenElem(ep unsafe.Pointer, m genMeta, seed uint64, tag int64, idBase int64, where string) {
	for i := 0; i < m.r*m.g; i++ {
		mode := int((tag + int64(i)) % 7)
		p := *(**node)(unsafe.Add(ep, genPtrOffset(m, i)))
		if mode%3 == 0 {
			if p != nil {
				failf("%s: ptr[%d] 应为 nil（漏标/残留）", where, i)
			}
			continue
		}
		checkNode(p, seed, idBase+int64(i), where)
	}
	for j := 0; j < m.r*m.d; j++ {
		v := *(*uint64)(unsafe.Add(ep, genPadOffset(m, j)))
		if v != padWord(seed, tag, j) {
			failf("%s: pad[%d]=%#x want=%#x（全局被改写/位图错位）", where, j, v, padWord(seed, tag, j))
		}
	}
	atomic.AddInt64(&genChecks, 1)
	atomic.AddInt64(&padChecks, int64(m.r*m.d))
}

//go:noinline
func checkGenElemInit(ep unsafe.Pointer, m genMeta, where string) {
	for i := 0; i < m.r*m.g; i++ {
		if p := *(**node)(unsafe.Add(ep, genPtrOffset(m, i))); p != nil {
			failf("%s: ptr[%d] 初始应为 nil", where, i)
		}
	}
}

// ---------------- worker ----------------

type worker struct {
	id       int64
	seed     uint64
	base     int64
	scale    int
	nworkers int64

	rounds int64
	visits int64
	gVisit []int64 // len = numCoreGlobals + len(genTable)
}

var (
	scaleStride   = [...]int{64, 512, 4096}
	scaleVisits   = [...]int{1, 2, 4}
	scaleReflectN = [...]int{4, 16, 32}
)

func (w *worker) strideFor(part int) int {
	s := scaleStride[w.scale]
	if s > part/2 {
		s = part / 2
	}
	if s < 1 {
		s = 1
	}
	return s
}

// slotID：全局槽内容 id（worker 内唯一）。
func (w *worker) slotID(tag int64, idx int64) int64 {
	return w.base + tag*100_000_000 + idx*1024
}

func kvKey(worker, tag int64, idx int64) string {
	return "w" + strconv.FormatInt(worker, 10) + "-" + strconv.FormatInt(tag, 10) + "-" + strconv.FormatInt(idx, 10)
}

func mapKey(worker, tag int64) string {
	return "m" + strconv.FormatInt(worker, 10) + "-" + strconv.FormatInt(tag, 10)
}

func (w *worker) maybeReflectCore(g int, tag int64) {
	var t reflect.Type
	switch g {
	case 0:
		t = reflect.TypeOf(gCorePtr)
	case 1:
		t = reflect.TypeOf(gCoreKv)
	case 2:
		t = reflect.TypeOf(gCoreChan)
	case 3:
		t = reflect.TypeOf(gCoreMap)
	case 4:
		t = reflect.TypeOf(gCoreInner)
	}
	if t.Size() == 0 {
		failf("y08.reflect: 核心类型 %d size=0", g)
	}
	pt := reflect.PointerTo(t)
	at := reflect.ArrayOf(2, t)
	if pt.Elem() != t || at.Len() != 2 || at.Elem() != t {
		failf("y08.reflect: 核心类型构造损坏 g=%d", g)
	}
	if g == 2 {
		if et := t.Elem(); et.Size() != chanKvN*64 {
			failf("y08.reflect: chan 元素类型 size=%d want=%d", et.Size(), chanKvN*64)
		}
	}
	atomic.AddInt64(&reflectOps, 1)
}

func (w *worker) reflectOps(gi int, tag int64) {
	t := genTypeList()[gi]
	m := genTable[gi]
	if t.Size() != m.size || typePtrData(t) != m.ptrdata {
		failf("y08.reflect: %s size/ptrdata=%d/%d want=%d/%d（类型元数据损坏）",
			m.name, t.Size(), typePtrData(t), m.size, m.ptrdata)
	}
	pt := reflect.PointerTo(t)
	if pt.Size() != 8 || pt.Elem() != t {
		failf("y08.reflect: PointerTo(%s) 损坏", m.name)
	}
	at := reflect.ArrayOf(2, t)
	if at.Len() != 2 || at.Elem() != t || at.Size() != 2*m.size {
		failf("y08.reflect: ArrayOf(2,%s) 损坏", m.name)
	}
	st := reflect.SliceOf(t)
	if st.Elem() != t || st.Size() != 24 {
		failf("y08.reflect: SliceOf(%s) 损坏", m.name)
	}
	for j := 0; j < scaleReflectN[w.scale]; j++ {
		p2 := reflect.PointerTo(at)
		a3 := reflect.ArrayOf(3, st)
		if p2.Elem() != at || a3.Len() != 3 || a3.Elem() != st {
			failf("y08.reflect: 链式构造损坏")
		}
		atomic.AddInt64(&reflectOps, 1)
	}
	if tag%8 == 0 && m.size <= 4<<20 {
		o := reflect.New(t) // 物化 gcdata（gcprog 解码路径）
		runtime.KeepAlive(o)
		atomic.AddInt64(&reflectNews, 1)
	}
}

func (w *worker) visitCorePtr(tag int64) {
	part := corePtrElems / int(w.nworkers)
	stride := w.strideFor(part)
	regions := part / stride
	region := int(tag % int64(regions))
	w.maybeReflectCore(0, tag)
	for i := 0; i < stride; i++ {
		idx := w.id + int64(region*stride+i)*w.nworkers
		// 回环校验：期望值 = regions 轮前的写入（或初始 nil）。
		if tag >= int64(regions) {
			expTag := tag - int64(regions)
			mode := int((expTag + int64(i)) % 7)
			p := gCorePtr[idx]
			if mode%3 == 0 {
				if p != nil {
					failf("y08.coreptr: idx=%d expTag=%d 应为 nil（漏标/残留）", idx, expTag)
				}
			} else {
				checkNode(p, w.seed, w.slotID(expTag, idx), "y08.coreptr.cycle")
			}
		} else if gCorePtr[idx] != nil {
			failf("y08.coreptr: idx=%d 初始应为 nil", idx)
		}
		mode := int((tag + int64(i)) % 7)
		gCorePtr[idx] = nodeOrNil(w.seed, w.slotID(tag, idx), mode)
		atomic.AddInt64(&slotWrites, 1)
	}
}

func (w *worker) reVerifyCorePtr(tag int64) {
	part := corePtrElems / int(w.nworkers)
	stride := w.strideFor(part)
	regions := part / stride
	region := int(tag % int64(regions))
	for i := 0; i < stride; i += max(1, stride/16) {
		idx := w.id + int64(region*stride+i)*w.nworkers
		mode := int((tag + int64(i)) % 7)
		p := gCorePtr[idx]
		if mode%3 == 0 {
			if p != nil {
				failf("y08.coreptr.post: idx=%d tag=%d 应为 nil（GC 后残留）", idx, tag)
			}
		} else {
			checkNode(p, w.seed, w.slotID(tag, idx), "y08.coreptr.post")
		}
	}
}

func (w *worker) visitCoreKv(tag int64) {
	part := coreKvElems / int(w.nworkers)
	stride := w.strideFor(part)
	regions := part / stride
	region := int(tag % int64(regions))
	w.maybeReflectCore(1, tag)
	for i := 0; i < stride; i++ {
		idx := w.id + int64(region*stride+i)*w.nworkers
		if tag >= int64(regions) {
			expTag := tag - int64(regions)
			mode := int((expTag + int64(i)) % 5)
			if mode == 0 {
				kv := &gCoreKv[idx]
				if kv.k != "" || kv.v != nil || kv.s != nil || kv.e != nil {
					failf("y08.corekv: idx=%d expTag=%d 应为全零（漏标/残留）", idx, expTag)
				}
			} else {
				checkKv(&gCoreKv[idx], w.seed, w.slotID(expTag, idx), kvKey(w.id, expTag, idx), mode, "y08.corekv.cycle")
			}
		} else {
			kv := &gCoreKv[idx]
			if kv.k != "" || kv.v != nil || kv.s != nil || kv.e != nil {
				failf("y08.corekv: idx=%d 初始应为全零", idx)
			}
		}
		mode := int((tag + int64(i)) % 5)
		if mode == 0 {
			gCoreKv[idx] = Kv{}
		} else {
			gCoreKv[idx] = makeKv(w.seed, w.slotID(tag, idx), kvKey(w.id, tag, idx), mode)
		}
		atomic.AddInt64(&slotWrites, 1)
	}
}

func (w *worker) reVerifyCoreKv(tag int64) {
	part := coreKvElems / int(w.nworkers)
	stride := w.strideFor(part)
	regions := part / stride
	region := int(tag % int64(regions))
	for i := 0; i < stride; i += max(1, stride/16) {
		idx := w.id + int64(region*stride+i)*w.nworkers
		mode := int((tag + int64(i)) % 5)
		if mode == 0 {
			kv := &gCoreKv[idx]
			if kv.k != "" || kv.v != nil || kv.s != nil || kv.e != nil {
				failf("y08.corekv.post: idx=%d tag=%d 应为全零（GC 后残留）", idx, tag)
			}
		} else {
			checkKv(&gCoreKv[idx], w.seed, w.slotID(tag, idx), kvKey(w.id, tag, idx), mode, "y08.corekv.post")
		}
	}
}

func makeChanKvArray(w *worker, tag int64) [chanKvN]Kv {
	idStart := w.base + tag*100_000_000 + 777_777
	label := "ch-" + strconv.FormatInt(idStart, 10)
	var a [chanKvN]Kv
	for i := range a {
		id := idStart + int64(i)
		a[i].k = label
		a[i].v = newNode(chanSeed, id)
		a[i].s = kvBytes(id)
		switch i % 4 {
		case 1:
			a[i].e = newNode(chanSeed, id+1_000_000)
		case 2:
			a[i].e = kvBytes(id)
		case 3:
			a[i].e = string(kvBytes(id))
		}
	}
	return a
}

//go:noinline
func checkChanKvArray(a *[chanKvN]Kv, where string) {
	var idStart int64
	label := ""
	for i := range a {
		if i == 0 {
			if len(a[i].k) < 4 || a[i].k[:3] != "ch-" {
				failf("%s: k 前缀损坏 %q", where, a[i].k)
			}
			v, err := strconv.ParseInt(a[i].k[3:], 10, 64)
			if err != nil {
				failf("%s: k 解析失败 %q", where, a[i].k)
			}
			idStart = v
			label = a[i].k
		} else if a[i].k != label {
			failf("%s: elem[%d].k 不一致", where, i)
		}
		id := idStart + int64(i)
		checkNode(a[i].v, chanSeed, id, where+".v")
		b := kvBytes(id)
		if len(a[i].s) != len(b) || a[i].s[0] != b[0] || a[i].s[15] != b[15] {
			failf("%s: elem[%d].s 损坏", where, i)
		}
		switch i % 4 {
		case 0:
			if a[i].e != nil {
				failf("%s: elem[%d].e 应为 nil", where, i)
			}
		case 1:
			n, ok := a[i].e.(*node)
			if !ok {
				failf("%s: elem[%d].e 类型=%T", where, i, a[i].e)
			}
			checkNode(n, chanSeed, id+1_000_000, where+".e")
		case 2:
			sb, ok := a[i].e.([]byte)
			if !ok || len(sb) != len(b) || sb[0] != b[0] || sb[15] != b[15] {
				failf("%s: elem[%d].e []byte 损坏", where, i)
			}
		case 3:
			s, ok := a[i].e.(string)
			if !ok || s != string(b) {
				failf("%s: elem[%d].e string 损坏", where, i)
			}
		}
	}
	atomic.AddInt64(&chanChecks, 1)
}

func (w *worker) visitChan(tag int64) {
	w.maybeReflectCore(2, tag)
	a := makeChanKvArray(w, tag)
	gCoreChan <- a
	got := <-gCoreChan
	checkChanKvArray(&got, "y08.chan")
	atomic.AddInt64(&chanOps, 1)
}

func (w *worker) visitMap(tag int64) {
	w.maybeReflectCore(3, tag)
	gMapMu.Lock()
	if tag >= 4 {
		delete(gCoreMap, mapKey(w.id, tag-4))
	}
	var v [64]*node
	for j := range v {
		v[j] = newNode(w.seed, w.base+tag*100_000_000+int64(j))
	}
	gCoreMap[mapKey(w.id, tag)] = v
	if tag%4 == 0 {
		for s := max(0, tag-3); s <= tag; s++ {
			got, ok := gCoreMap[mapKey(w.id, s)]
			if !ok {
				failf("y08.map: key=%q 缺失", mapKey(w.id, s))
			}
			for j := range got {
				checkNode(got[j], w.seed, w.base+s*100_000_000+int64(j), "y08.map")
			}
			atomic.AddInt64(&mapChecks, 1)
		}
	}
	gMapMu.Unlock()
	atomic.AddInt64(&mapOps, 1)
}

//go:noinline
func checkInnerInit(in *Inner, where string) {
	if in.P0 != nil {
		failf("%s: P0 初始应为 nil", where)
	}
	for j := range in.M0 {
		m := &in.M0[j]
		if m.X != nil || m.Y[0] != nil || m.Y[1] != nil || m.W != nil {
			failf("%s: M[%d] 初始应为 nil", where, j)
		}
	}
	if in.P1 != nil {
		failf("%s: P1 初始应为 nil", where)
	}
	for j := range in.E {
		d := &in.E[j]
		if d.A[0] != nil || d.A[1] != nil || d.A[2] != nil || d.S != nil || d.P != nil {
			failf("%s: E[%d] 初始应为 nil", where, j)
		}
	}
}

func (w *worker) visitCoreInner(tag int64) {
	part := coreInnerElems / int(w.nworkers)
	stride := w.strideFor(part)
	regions := part / stride
	region := int(tag % int64(regions))
	w.maybeReflectCore(4, tag)
	for i := 0; i < stride; i++ {
		idx := w.id + int64(region*stride+i)*w.nworkers
		if tag >= int64(regions) {
			expTag := tag - int64(regions)
			mode := int((expTag + int64(i)) % 5)
			checkInner(&gCoreInner[idx], w.seed, w.slotID(expTag, idx), mode, "y08.coreinner.cycle")
		} else {
			checkInnerInit(&gCoreInner[idx], "y08.coreinner.init")
		}
		var in Inner
		mode := int((tag + int64(i)) % 5)
		fillInner(&in, w.seed, w.slotID(tag, idx), mode)
		gCoreInner[idx] = in
		atomic.AddInt64(&slotWrites, 1)
	}
}

func (w *worker) reVerifyCoreInner(tag int64) {
	part := coreInnerElems / int(w.nworkers)
	stride := w.strideFor(part)
	regions := part / stride
	region := int(tag % int64(regions))
	for i := 0; i < stride; i += max(1, stride/16) {
		idx := w.id + int64(region*stride+i)*w.nworkers
		mode := int((tag + int64(i)) % 5)
		checkInner(&gCoreInner[idx], w.seed, w.slotID(tag, idx), mode, "y08.coreinner.post")
	}
}

func (w *worker) visitGen(gi int, tag int64) {
	m := genTable[gi]
	base := genBasePtrs()[gi]
	elemSize := m.size / uintptr(m.n)
	w.reflectOps(gi, tag)
	part := m.n / int(w.nworkers)
	stride := w.strideFor(part)
	regions := part / stride
	region := int(tag % int64(regions))
	for i := 0; i < stride; i++ {
		elem := w.id + int64(region*stride+i)*w.nworkers
		ep := unsafe.Add(base, uintptr(elem)*elemSize)
		if tag >= int64(regions) {
			expTag := tag - int64(regions)
			checkGenElem(ep, m, w.seed, expTag, w.slotID(expTag, elem), "y08.gen."+m.name+".cycle")
		} else {
			checkGenElemInit(ep, m, "y08.gen."+m.name+".init")
		}
		writeGenElem(ep, m, w.seed, tag, w.slotID(tag, elem))
		atomic.AddInt64(&slotWrites, 1)
	}
}

func (w *worker) reVerifyGen(gi int, tag int64) {
	m := genTable[gi]
	base := genBasePtrs()[gi]
	elemSize := m.size / uintptr(m.n)
	part := m.n / int(w.nworkers)
	stride := w.strideFor(part)
	regions := part / stride
	region := int(tag % int64(regions))
	for i := 0; i < stride; i += max(1, stride/16) {
		elem := w.id + int64(region*stride+i)*w.nworkers
		ep := unsafe.Add(base, uintptr(elem)*elemSize)
		checkGenElem(ep, m, w.seed, tag, w.slotID(tag, elem), "y08.gen."+m.name+".post")
	}
}

func (w *worker) visit(g int) {
	t := w.gVisit[g]
	switch g {
	case 0:
		w.visitCorePtr(t)
	case 1:
		w.visitCoreKv(t)
	case 2:
		w.visitChan(t)
	case 3:
		w.visitMap(t)
	case 4:
		w.visitCoreInner(t)
	default:
		w.visitGen(g-numCoreGlobals, t)
	}
	w.gVisit[g] = t + 1
}

func (w *worker) reVerify(g int, tag int64) {
	switch g {
	case 0:
		w.reVerifyCorePtr(tag)
	case 1:
		w.reVerifyCoreKv(tag)
	case 2: // chan 往返即校验
	case 3: // map 自带回环校验
	case 4:
		w.reVerifyCoreInner(tag)
	default:
		w.reVerifyGen(g-numCoreGlobals, tag)
	}
}

func (w *worker) round(r int64) {
	n := numCoreGlobals + len(genTable)
	type touched struct {
		g   int
		tag int64
	}
	toRecheck := make([]touched, 0, scaleVisits[w.scale])
	for v := 0; v < scaleVisits[w.scale]; v++ {
		// 分片索引：workers 交错覆盖全局表，避免小轮数时部分路径（chan/map/gen）
		// 永远不被访问（每个 worker 独占自己的分片）。
		g := int((w.visits*w.nworkers + w.id) % int64(n))
		w.visit(g)
		toRecheck = append(toRecheck, touched{g, w.gVisit[g] - 1})
		w.visits++
	}
	runtime.GC()
	atomic.AddInt64(&gcCycles, 1)
	// 写入 → GC → 复核：本轮写入的区域在 GC 后精确复核（漏标 → canary 失配）。
	for _, t := range toRecheck {
		w.reVerify(t.g, t.tag)
	}
	w.rounds++
	atomic.AddInt64(&beats, 1)
}

func (w *worker) run(seconds int, deadline time.Time, fixed int64) {
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
}

// ---------------- 通用 harness ----------------

var (
	beats        int64
	gcCycles     int64
	started      int64
	finished     int64
	gSeed        uint64
	slotWrites   int64
	canaryChecks int64
	kvChecks     int64
	innerChecks  int64
	genChecks    int64
	padChecks    int64
	chanOps      int64
	chanChecks   int64
	mapOps       int64
	mapChecks    int64
	reflectOps   int64
	reflectNews  int64
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

func parseScale(s string) int {
	switch s {
	case "", "small":
		return 0
	case "medium":
		return 1
	case "full":
		return 2
	default:
		failf("phase=preflight: STRESS_SCALE=%q want small|medium|full", s)
		return 0
	}
}

func scaleName(s int) string {
	switch s {
	case 0:
		return "small"
	case 1:
		return "medium"
	default:
		return "full"
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

func gcChurn(stop <-chan struct{}, wg *sync.WaitGroup) {
	defer wg.Done()
	for {
		select {
		case <-stop:
			return
		default:
		}
		runtime.GC()
		atomic.AddInt64(&gcCycles, 1)
		// window（GOMAXPROCS=1+STW）下无休眠 GC 循环会饿死 worker/watchdog。
		if runtime.GOMAXPROCS(0) == 1 {
			runtime.Gosched()
			time.Sleep(time.Millisecond) // 节奏控制，非断言
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
				failf("phase=watchdog: 6 个心跳周期无进展 (%s, beats=%d)", time.Since(lastChange), cur)
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

func main() {
	setupCrashOutput()
	seconds := envInt("STRESS_SECONDS", 60, 0, 86400)
	workers := envInt("STRESS_WORKERS", 4, 1, 16)
	hb := envInt("STRESS_HEARTBEAT", 5, 1, 60)
	seed := envSeed(1)
	fixed := int64(envInt("STRESS_FIXED_ROUNDS", fixedRoundsDefault, 1, 1000000))
	scale := parseScale(os.Getenv("STRESS_SCALE"))
	gSeed = seed

	// 生成器确定性对照 + 预算校验（B14/B15 前置：位图必须真实存在于元数据）。
	tl := genTypeList()
	if len(tl) != len(genTable) {
		failf("phase=preflight: genTypeList len=%d want=%d", len(tl), len(genTable))
	}
	var genBytes uintptr
	var genBits int64
	for i := range tl {
		m := genTable[i]
		if tl[i].Size() != m.size || typePtrData(tl[i]) != m.ptrdata {
			failf("phase=preflight: %s size/ptrdata=%d/%d want=%d/%d（生成元数据失配）",
				m.name, tl[i].Size(), typePtrData(tl[i]), m.size, m.ptrdata)
		}
		genBytes += m.size
		genBits += int64(m.ptrbits)
	}
	coreBytes := reflect.TypeOf(gCorePtr).Size() + reflect.TypeOf(gCoreKv).Size() +
		reflect.TypeOf(gCoreInner).Size() +
		uintptr(cap(gCoreChan))*reflect.TypeOf(gCoreChan).Elem().Size()
	coreBits := int64(corePtrElems + 5*coreKvElems + 28*coreInnerElems)
	totalBytes := genBytes + coreBytes
	totalBits := genBits + coreBits
	if totalBytes > 256<<20 {
		failf("phase=preflight: 全局总量 %d 超过 256MB", totalBytes)
	}
	if totalBits < 1_000_000 {
		failf("phase=preflight: 总指针位 %d < 100 万", totalBits)
	}

	fmt.Printf("scenario=%s phase=preflight go=%s goos=%s goarch=%s gomaxprocs=%d seed=%d seconds=%d workers=%d fixed_rounds=%d heartbeat=%ds scale=%s globals=%d globals_bytes=%d ptrbits=%d\n",
		scenario, runtime.Version(), runtime.GOOS, runtime.GOARCH, runtime.GOMAXPROCS(0),
		seed, seconds, workers, fixed, hb, scaleName(scale), len(tl)+numCoreGlobals, totalBytes, totalBits)

	runtime.GC()
	baseGoroutines := runtime.NumGoroutine()
	deadline := time.Now().Add(time.Duration(seconds) * time.Second)
	ws := make([]*worker, workers)
	for i := 0; i < workers; i++ {
		ws[i] = &worker{
			id:       int64(i),
			seed:     seed*1_000_003 + uint64(i+1)*7919,
			base:     int64(i) * 1_000_000_000_000,
			scale:    scale,
			nworkers: int64(workers),
			gVisit:   make([]int64, numCoreGlobals+len(genTable)),
		}
	}

	stop := make(chan struct{})
	var aux sync.WaitGroup
	aux.Add(1)
	go gcChurn(stop, &aux)
	aux.Add(1)
	go watchdog(time.Duration(hb)*time.Second, stop, &aux)

	var wg sync.WaitGroup
	for _, w := range ws {
		wg.Add(1)
		go func(w *worker) {
			atomic.AddInt64(&started, 1)
			defer func() {
				atomic.AddInt64(&finished, 1)
				wg.Done()
			}()
			w.run(seconds, deadline, fixed)
		}(w)
	}
	wg.Wait()
	close(stop)
	aux.Wait()

	if s, f := atomic.LoadInt64(&started), atomic.LoadInt64(&finished); s != int64(workers) || f != s {
		failf("phase=final: started=%d finished=%d workers=%d", s, f, workers)
	}
	var rounds int64
	for _, w := range ws {
		rounds += w.rounds
		if seconds == 0 && w.rounds != fixed {
			failf("phase=final worker=%d: rounds=%d want=%d", w.id, w.rounds, fixed)
		}
	}
	if rounds == 0 {
		failf("phase=final: 没有完成任何轮次")
	}
	if atomic.LoadInt64(&canaryChecks) == 0 || atomic.LoadInt64(&reflectOps) == 0 ||
		atomic.LoadInt64(&chanChecks) == 0 || atomic.LoadInt64(&mapChecks) == 0 ||
		atomic.LoadInt64(&genChecks) == 0 || atomic.LoadInt64(&kvChecks) == 0 ||
		atomic.LoadInt64(&innerChecks) == 0 {
		failf("phase=final: 关键路径未执行 canary=%d reflect=%d chan=%d map=%d gen=%d kv=%d inner=%d",
			atomic.LoadInt64(&canaryChecks), atomic.LoadInt64(&reflectOps),
			atomic.LoadInt64(&chanChecks), atomic.LoadInt64(&mapChecks),
			atomic.LoadInt64(&genChecks), atomic.LoadInt64(&kvChecks),
			atomic.LoadInt64(&innerChecks))
	}

	ng := waitGoroutines(baseGoroutines, 5*time.Second)
	if ng > baseGoroutines+4 {
		failf("phase=final: goroutines baseline=%d after=%d: goroutine 泄漏", baseGoroutines, ng)
	}

	var visitsTotal int64
	for _, w := range ws {
		visitsTotal += w.visits
	}
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	fmt.Printf("metrics: rounds=%d visits=%d slot_writes=%d canary_checks=%d kv_checks=%d inner_checks=%d gen_checks=%d pad_checks=%d chan_ops=%d chan_checks=%d map_ops=%d map_checks=%d reflect_ops=%d reflect_news=%d gc_churn=%d num_gc=%d heap_alloc=%d heap_objects=%d goroutines=%d\n",
		rounds, visitsTotal,
		atomic.LoadInt64(&slotWrites), atomic.LoadInt64(&canaryChecks), atomic.LoadInt64(&kvChecks),
		atomic.LoadInt64(&innerChecks), atomic.LoadInt64(&genChecks), atomic.LoadInt64(&padChecks),
		atomic.LoadInt64(&chanOps), atomic.LoadInt64(&chanChecks), atomic.LoadInt64(&mapOps),
		atomic.LoadInt64(&mapChecks), atomic.LoadInt64(&reflectOps), atomic.LoadInt64(&reflectNews),
		atomic.LoadInt64(&gcCycles), m.NumGC, m.HeapAlloc, m.HeapObjects, ng)
	fmt.Printf("STRESS PASS %s\n", scenario)
}
