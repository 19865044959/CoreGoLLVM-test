// Y07: typedmemmove 穷举（P0，covers L18/L17）。
// 目标：C17 GoTypedMemmove——含指针字段大值（Big 264B）的全部批量搬运路径，
// 与 GC 落点穷举（搬运前/搬运中/搬运后）组合：
//   - copy(dst, src)（dst/src 为 []Big，非 []*T）：脏 dst（预填指向短命节点的
//     大值）+ 写前 GC 窗口；拆分两段 copy 中间 GC（半写 dst 被扫描）；搬运后
//     src 丢弃、GC 后逐字段 canary 校验；
//   - 并发 GC 与 copy 写屏障重叠（u01 风格：copy 期间另 goroutine GC）；
//   - append 扩容搬运（增长 memmove 老元素 + 批量 append(dst, src...)）；
//   - m[k] = bigV map 值拷贝 + 增长 evacuation（值 typedmemmove）+ delete/重插；
//   - chan send/recv 大值（buffer 值拷贝）；装箱 interface{}（堆拷贝）；
//   - 自搬运 copy(a, a) 重叠区（右移/左移，逐字段核对置换后的 canary）；
//   - dst 在栈（addrtaken [2]Big）与堆两种；
//   - 源在老年代（两轮 GC 晋升）、目标年轻/年老：搬运时写屏障。
// 所有 node 的 v 是确定性 canary（nodeMagic(seed,id)），任何一次读取不匹配
// 立即 FAIL——typedmemmove 漏写屏障/漏标当场现形。
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
)

const (
	scenario = "y07_typedmemmove_gc"

	bigSlots  = 8
	bigWords  = 16
	bigStride = 1000 // 每个 Big 的节点 id 步长（跨 Big 不冲突）

	nPerVariant = 8 // 各变体元素数
	poisonN     = 48

	poisonIDBase = int64(1) << 50
)

type node struct {
	id  int64
	v   int64
	pad [2]uint64
}

// Big 含指针字段大值（264B：16 指针 + 16 words + seq）。
type Big struct {
	a     [bigSlots]*node
	b     [bigSlots]*node
	words [bigWords]uint64
	seq   int64
}

// ---------- 通用 canary ----------

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

func wordConst(i int) uint64 {
	return uint64(i+1) * 0x9E3779B97F4A7C15
}

func checkNode(n *node, wantID int64, seed uint64, phase string) {
	if n == nil {
		failf("phase=%s: nil *node want id=%d (UAF/typedmemmove 漏标)", phase, wantID)
	}
	if n.id != wantID {
		failf("phase=%s: id=%d want=%d (搬运后内容错位/复用)", phase, n.id, wantID)
	}
	if n.v != nodeMagic(seed, n.id) {
		failf("phase=%s id=%d: canary v=%#x want=%#x (UAF/typedmemmove 漏标)", phase, n.id, n.v, nodeMagic(seed, n.id))
	}
	atomic.AddInt64(&canaryChecks, 1)
}

//go:noinline
func mkBig(seed uint64, base int64) Big {
	var bv Big
	bv.seq = base
	for i := 0; i < bigSlots; i++ {
		bv.a[i] = newNode(seed, base+int64(i))
		bv.b[i] = newNode(seed, base+int64(bigSlots+i))
	}
	for i := range bv.words {
		bv.words[i] = wordConst(i)
	}
	return bv
}

// checkBig 逐字段校验搬运后的大值：节点指针/words/seq 全部与 (seed, base) 期望一致。
//
//go:noinline
func checkBig(seed uint64, base int64, bv Big, phase string) {
	for i := 0; i < bigSlots; i++ {
		checkNode(bv.a[i], base+int64(i), seed, phase+".a")
		checkNode(bv.b[i], base+int64(bigSlots+i), seed, phase+".b")
	}
	for i := range bv.words {
		if bv.words[i] != wordConst(i) {
			failf("phase=%s: words[%d]=%#x (搬运后内容被改写)", phase, i, bv.words[i])
		}
	}
	if bv.seq != base {
		failf("phase=%s: seq=%d want=%d (搬运后内容被改写)", phase, bv.seq, base)
	}
	runtime.KeepAlive(&bv)
}

//go:noinline
func mkSrcSlice(seed uint64, base int64, n int) []Big {
	s := make([]Big, n)
	for i := range s {
		s[i] = mkBig(seed, base+int64(i)*bigStride)
	}
	return s
}

//go:noinline
func checkSlice(seed uint64, base int64, s []Big, phase string) {
	if len(s) != nPerVariant {
		failf("phase=%s: len=%d want=%d", phase, len(s), nPerVariant)
	}
	for i := range s {
		checkBig(seed, base+int64(i)*bigStride, s[i], phase)
	}
}

// ---------- 变体 1：copy（搬运前/后 GC + 拆分中间 GC） ----------

// copyPlain：dst 新鲜零值——搬运前 GC（零值 dst 被扫）→ copy → src 丢弃 →
// 搬运后 GC（搬运后的指针只经 dst 可达，必须被标记）。
//
//go:noinline
func copyPlain(seed uint64, base int64) int64 {
	dst := make([]Big, nPerVariant)
	src := mkSrcSlice(seed, base, nPerVariant)
	runtime.GC() // 搬运前 GC
	atomic.AddInt64(&gcCycles, 1)
	copy(dst, src)
	src = nil // 搬运后指针只经 dst 可达
	runtime.GC()
	atomic.AddInt64(&gcCycles, 1)
	checkSlice(seed, base, dst, "y07.copy-plain")
	var s int64
	for i := range dst {
		s += dst[i].seq
	}
	return s
}

// copySplit：拆分两段 copy，段间 GC（半写 dst 被扫描——搬运中间落点）。
//
//go:noinline
func copySplit(seed uint64, base int64) int64 {
	dst := make([]Big, nPerVariant)
	src := mkSrcSlice(seed, base, nPerVariant)
	mid := nPerVariant / 2
	copy(dst[:mid], src[:mid])
	runtime.GC() // 搬运中间 GC：dst 半写（前半新指针 + 后半零值）
	atomic.AddInt64(&gcCycles, 1)
	copy(dst[mid:], src[mid:])
	src = nil
	runtime.GC() // 搬运后 GC
	atomic.AddInt64(&gcCycles, 1)
	checkSlice(seed, base, dst, "y07.copy-split")
	var s int64
	for i := range dst {
		s += dst[i].seq
	}
	return s
}

// copyDirtyConc：脏 dst（预填指向短命节点的大值，同 size class 填埋）+ 写前 GC
// + 并发 GC 与 copy 写屏障重叠（u01 风格）+ 搬运后 GC。
//
//go:noinline
func copyDirtyConc(seed uint64, base int64) int64 {
	dst := make([]Big, nPerVariant)
	for i := range dst {
		dst[i] = mkBig(seed, base+int64(i)*bigStride) // 预填（短命：即将被 clobber）
	}
	src := mkSrcSlice(seed, base, nPerVariant)
	runtime.GC() // 写前 GC：扫描脏 dst
	atomic.AddInt64(&gcCycles, 1)
	done := make(chan struct{})
	go func() {
		runtime.GC() // 并发 GC：与 copy 的写屏障重叠
		atomic.AddInt64(&gcCycles, 1)
		close(done)
	}()
	copy(dst, src)
	<-done
	src = nil
	runtime.GC() // 搬运后 GC
	atomic.AddInt64(&gcCycles, 1)
	checkSlice(seed, base, dst, "y07.copy-dirty-conc")
	var s int64
	for i := range dst {
		s += dst[i].seq
	}
	return s
}

// ---------- 变体 2：append 扩容搬运 ----------

// appendGrow：从空容量逐步 append，触发扩容 memmove（老元素）；增长中间 GC；
// 再批量 append(dst, src...)；搬运后 GC 全量校验。
//
//go:noinline
func appendGrow(seed uint64, base int64) int64 {
	dst := make([]Big, 0, 2)
	for i := 0; i < nPerVariant/2; i++ {
		if i == nPerVariant/4 {
			runtime.GC() // 扩容搬运中间 GC
			atomic.AddInt64(&gcCycles, 1)
		}
		dst = append(dst, mkBig(seed, base+int64(i)*bigStride))
	}
	more := mkSrcSlice(seed, base+int64(nPerVariant/2)*bigStride, nPerVariant/2)
	runtime.GC() // 批量 append 前 GC
	atomic.AddInt64(&gcCycles, 1)
	dst = append(dst, more...) // 批量 append：增长 memmove + 新段
	more = nil
	runtime.GC() // 搬运后 GC
	atomic.AddInt64(&gcCycles, 1)
	checkSlice(seed, base, dst, "y07.append-grow")
	var s int64
	for i := range dst {
		s += dst[i].seq
	}
	return s
}

// ---------- 变体 3：map 值拷贝 + 增长 evacuation ----------

//go:noinline
func mapAssign(seed uint64, base int64) int64 {
	m := make(map[int64]Big)
	for i := 0; i < nPerVariant*8; i++ {
		if i == nPerVariant*4 {
			runtime.GC() // 插入中间 GC（evacuation/桶写入窗口）
			atomic.AddInt64(&gcCycles, 1)
		}
		m[int64(i)] = mkBig(seed, base+int64(i)*bigStride) // map 值拷贝
	}
	for i := 0; i < 4; i++ {
		delete(m, int64(i))
	}
	for i := 0; i < 4; i++ {
		m[int64(i)] = mkBig(seed, base+int64(nPerVariant*8+i)*bigStride) // delete 后重插
	}
	runtime.GC() // 搬运后 GC：值只经 map 桶可达
	atomic.AddInt64(&gcCycles, 1)
	var s int64
	for i := 0; i < nPerVariant*8; i++ {
		wantBase := base + int64(i)*bigStride
		if i < 4 {
			wantBase = base + int64(nPerVariant*8+i)*bigStride
		}
		rv, ok := m[int64(i)]
		if !ok {
			failf("phase=y07.map: key=%d 缺失", i)
		}
		checkBig(seed, wantBase, rv, "y07.map")
		s += rv.seq
	}
	return s
}

// ---------- 变体 4：chan send/recv 大值 ----------

//go:noinline
func chanSendRecv(seed uint64, base int64) int64 {
	ch := make(chan Big, nPerVariant)
	for i := 0; i < nPerVariant; i++ {
		ch <- mkBig(seed, base+int64(i)*bigStride) // send：值拷入 buffer
	}
	runtime.GC() // 写后 GC：值只经 chan buffer 可达
	atomic.AddInt64(&gcCycles, 1)
	var s int64
	for i := 0; i < nPerVariant; i++ {
		if i == nPerVariant/2 {
			runtime.GC() // 半读 GC
			atomic.AddInt64(&gcCycles, 1)
		}
		rv := <-ch
		checkBig(seed, base+int64(i)*bigStride, rv, "y07.chan")
		s += rv.seq
	}
	return s
}

// ---------- 变体 5：interface 装箱大值 ----------

//go:noinline
func ifaceBox(seed uint64, base int64) int64 {
	boxes := make([]interface{}, 0, nPerVariant)
	for i := 0; i < nPerVariant; i++ {
		boxes = append(boxes, mkBig(seed, base+int64(i)*bigStride)) // 装箱：堆拷贝
	}
	runtime.GC() // 装箱后 GC：值只经 iface data 可达
	atomic.AddInt64(&gcCycles, 1)
	var s int64
	for i := range boxes {
		if i == nPerVariant/2 {
			runtime.GC() // 半拆箱 GC
			atomic.AddInt64(&gcCycles, 1)
		}
		rv, ok := boxes[i].(Big)
		if !ok {
			failf("phase=y07.box: 拆箱类型=%T want=Big", boxes[i])
		}
		checkBig(seed, base+int64(i)*bigStride, rv, "y07.box")
		s += rv.seq
	}
	return s
}

// ---------- 变体 6：自搬运 copy(a, a) 重叠区 ----------

// overlapMove：右移 copy(a[1:], a[:n-1]) → 中间 GC → 左移 copy(a[:n-1], a[1:])
// → GC 后按置换后的期望逐字段核对（memmove 重叠语义）。
//
//go:noinline
func overlapMove(seed uint64, base int64) int64 {
	a := mkSrcSlice(seed, base, nPerVariant)
	runtime.GC() // 搬运前 GC
	atomic.AddInt64(&gcCycles, 1)
	copy(a[1:], a[:nPerVariant-1]) // 右移：a[i] = orig[i-1]
	runtime.GC()                    // 中间 GC（重叠搬运后半写状态）
	atomic.AddInt64(&gcCycles, 1)
	checkBig(seed, base, a[0], "y07.overlap.r0")
	for i := 1; i < nPerVariant; i++ {
		checkBig(seed, base+int64(i-1)*bigStride, a[i], "y07.overlap.right")
	}
	copy(a[:nPerVariant-1], a[1:]) // 左移：a[i] = a[i+1]（n-1 保持）
	runtime.GC()                    // 搬运后 GC
	atomic.AddInt64(&gcCycles, 1)
	for i := 0; i < nPerVariant-1; i++ {
		checkBig(seed, base+int64(i)*bigStride, a[i], "y07.overlap.left")
	}
	checkBig(seed, base+int64(nPerVariant-2)*bigStride, a[nPerVariant-1], "y07.overlap.tail")
	var s int64
	for i := range a {
		s += a[i].seq
	}
	return s
}

// ---------- 变体 7：dst 在栈（addrtaken） ----------

// stackDst：栈上 [2]Big（addrtaken：defer 闭包 + noinline 非保留调用），
// copy 写入栈 dst（GoRemoveStackWriteBarriers 路径）；写前 GC 扫未初始化槽
//（脏字节窗口）、写后 GC 校验。
//
//go:noinline
func stackDst(seed uint64, base int64) int64 {
	var sdst [2]Big
	defer func() {
		checkBig(seed, base, sdst[0], "y07.stackdst.defer.0")
		checkBig(seed, base+bigStride, sdst[1], "y07.stackdst.defer.1")
	}()
	src := mkSrcSlice(seed, base, 2)
	runtime.GC() // 写前 GC：栈 dst 未初始化窗口被扫描
	atomic.AddInt64(&gcCycles, 1)
	copy(sdst[:], src)
	src = nil
	runtime.GC() // 写后 GC：栈 dst 的指针必须被标记
	atomic.AddInt64(&gcCycles, 1)
	checkStackDst(&sdst, seed, base)
	return sdst[0].seq + sdst[1].seq
}

//go:noinline
func checkStackDst(s *[2]Big, seed uint64, base int64) int64 {
	checkBig(seed, base, s[0], "y07.stackdst.0")
	checkBig(seed, base+bigStride, s[1], "y07.stackdst.1")
	return s[0].seq + s[1].seq
}

// ---------- 变体 8：源老年代 → 目标年轻（搬运时写屏障） ----------

// oldToYoung：src 两轮 GC 晋升老年代；目标年轻（晋升后分配），并发 GC 与
// 搬运重叠（写屏障窗口）；搬运后 GC 校验。oldToOld 对照。
//
//go:noinline
func oldToYoung(seed uint64, base int64) int64 {
	src := mkSrcSlice(seed, base, nPerVariant)
	runtime.GC()
	runtime.GC() // 两轮晋升老年代
	atomic.AddInt64(&gcCycles, 2)
	dst := make([]Big, nPerVariant) // 年轻目标
	done := make(chan struct{})
	go func() {
		runtime.GC() // 并发 GC：与 old→young 搬运写屏障重叠
		atomic.AddInt64(&gcCycles, 1)
		close(done)
	}()
	copy(dst, src)
	<-done
	src = nil
	runtime.GC() // 搬运后 GC
	atomic.AddInt64(&gcCycles, 1)
	checkSlice(seed, base, dst, "y07.old-to-young")
	var s int64
	for i := range dst {
		s += dst[i].seq
	}
	return s
}

//go:noinline
func oldToOld(seed uint64, base int64) int64 {
	src := mkSrcSlice(seed, base, nPerVariant)
	dst := make([]Big, nPerVariant)
	runtime.GC()
	runtime.GC() // 双晋升：src/dst 均老年代
	atomic.AddInt64(&gcCycles, 2)
	copy(dst, src)
	src = nil
	runtime.GC() // old→old 搬运后 GC
	atomic.AddInt64(&gcCycles, 1)
	checkSlice(seed, base, dst, "y07.old-to-old")
	var s int64
	for i := range dst {
		s += dst[i].seq
	}
	return s
}

// ---------- poison（同 size class 填埋） ----------

var poisonSeq int64

//go:noinline
func poisonAlloc(seed uint64, n int) []*node {
	seq := atomic.AddInt64(&poisonSeq, 1)
	out := make([]*node, n)
	for i := range out {
		out[i] = newNode(seed, poisonIDBase+seq*1_000_000+int64(i))
	}
	return out
}

// ---------- worker ----------

type worker struct {
	id    int64
	seed  uint64
	base0 int64

	next   int64
	rounds int64
	checks int64
}

//go:noinline
func (w *worker) round(r int64) {
	base := w.base0 + r*50_000

	poison := poisonAlloc(w.seed, poisonN) // 同 size class 短命对象填埋
	_ = poison[0]
	runtime.KeepAlive(poison)

	_ = copyPlain(w.seed, base)
	_ = copySplit(w.seed, base+nPerVariant*bigStride)
	_ = copyDirtyConc(w.seed, base+2*nPerVariant*bigStride)
	_ = appendGrow(w.seed, base+3*nPerVariant*bigStride)
	_ = mapAssign(w.seed, base+4*nPerVariant*bigStride)
	_ = chanSendRecv(w.seed, base+5*nPerVariant*bigStride)
	_ = ifaceBox(w.seed, base+6*nPerVariant*bigStride)
	_ = overlapMove(w.seed, base+7*nPerVariant*bigStride)
	_ = stackDst(w.seed, base+8*nPerVariant*bigStride)
	_ = oldToYoung(w.seed, base+9*nPerVariant*bigStride)
	_ = oldToOld(w.seed, base+10*nPerVariant*bigStride)

	w.rounds++
	w.next = r + 1
	w.checks += nPerVariant * 16 * 12
	atomic.AddInt64(&beats, 1)
}

func (w *worker) run(seconds int, deadline time.Time, fixed int64) {
	atomic.AddInt64(&started, 1)
	defer atomic.AddInt64(&finished, 1)
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

// ---------- 通用 harness ----------

var (
	beats int64

	started  int64
	finished int64

	canaryChecks int64

	gcCycles      int64
	gcChurnCycles int64

	gSeed uint64
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

// gcChurn：分配-释放-GC 循环。GOMAXPROCS=1（window profile）下必须先 Gosched
// 让出 P、再 1ms sleep，否则 worker 被饿死（心跳 watchdog 触发误报）。
func gcChurn(stop <-chan struct{}, wg *sync.WaitGroup) {
	defer wg.Done()
	var sink [][]byte
	for {
		select {
		case <-stop:
			return
		default:
		}
		for i := 0; i < 16; i++ {
			b := make([]byte, 4096)
			b[0] = byte(i)
			sink = append(sink, b)
		}
		for i := range sink {
			sink[i] = nil
		}
		sink = sink[:0]
		runtime.Gosched() // GOMAXPROCS=1 时先让出 P，防 window 饿死
		runtime.GC()
		atomic.AddInt64(&gcChurnCycles, 1)
		time.Sleep(time.Millisecond) // 1ms sleep：单 P 下 gcChurn 不能独占
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
				failf("phase=watchdog: no worker progress for %s (beats=%d)", time.Since(lastChange), cur)
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
	workers := envInt("STRESS_WORKERS", 4, 1, 32)
	hb := envInt("STRESS_HEARTBEAT", 5, 1, 60)
	seed := envSeed(1)
	fixed := int64(envInt("STRESS_FIXED_ROUNDS", 8, 1, 1000000))
	gSeed = seed

	if s := reflect.TypeOf(Big{}).Size(); s != 264 {
		failf("phase=preflight: Big size=%d want=264 (typedmemmove 形态假设不成立)", s)
	}

	fmt.Printf("scenario=%s phase=preflight go=%s goos=%s goarch=%s gomaxprocs=%d seed=%d seconds=%d workers=%d fixed_rounds=%d heartbeat=%ds big_size=%d per_variant=%d\n",
		scenario, runtime.Version(), runtime.GOOS, runtime.GOARCH, runtime.GOMAXPROCS(0), seed, seconds, workers, fixed, hb,
		reflect.TypeOf(Big{}).Size(), nPerVariant)

	baseGoroutines := runtime.NumGoroutine()
	deadline := time.Now().Add(time.Duration(seconds) * time.Second)
	ws := make([]*worker, workers)
	for i := 0; i < workers; i++ {
		ws[i] = &worker{
			id:    int64(i),
			seed:  seed*1_000_003 + uint64(i+1)*7919,
			base0: int64(i)*10_000_000_000 + 1_000_000,
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
			defer wg.Done()
			w.run(seconds, deadline, fixed)
		}(w)
	}
	wg.Wait()
	close(stop)
	aux.Wait()

	if s, f := atomic.LoadInt64(&started), atomic.LoadInt64(&finished); s != int64(workers) || f != s {
		failf("phase=final: started=%d finished=%d workers=%d", s, f, workers)
	}
	var roundsTotal, checksTotal int64
	for _, w := range ws {
		roundsTotal += w.rounds
		checksTotal += w.checks
		if seconds == 0 && w.next != fixed {
			failf("phase=final worker=%d: rounds=%d want=%d", w.id, w.next, fixed)
		}
	}
	if roundsTotal == 0 {
		failf("phase=final: 没有完成任何轮次")
	}

	ng := waitGoroutines(baseGoroutines, 5*time.Second)
	if ng > baseGoroutines+4 {
		failf("phase=final: goroutines baseline=%d after=%d: goroutine leak", baseGoroutines, ng)
	}

	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	fmt.Printf("metrics: rounds=%d canary_checks=%d worker_gc=%d gc_churn=%d num_gc=%d heap_alloc=%d heap_objects=%d goroutines=%d\n",
		roundsTotal, atomic.LoadInt64(&canaryChecks),
		atomic.LoadInt64(&gcCycles), atomic.LoadInt64(&gcChurnCycles),
		m.NumGC, m.HeapAlloc, m.HeapObjects, ng)
	fmt.Printf("STRESS PASS %s\n", scenario)
}
