// Y05: 接口/尾调用 × 返回槽（P0，covers L17/L06）。
// 目标：把「大值返回槽」压到 itab 派发 / wrapper / 尾调用的路径上（D20 GoTailCall）：
//   - getter 接口 { Get() Big } 多实现：值 receiver（Big 自身 264B 按值拷贝、
//     bigImpl 296B）与指针 receiver（ptrImpl），g.Get() 经 itab 派发
//     （wrapper 复制 receiver 后可能尾调用真实方法）；
//   - 方法值 b.Get 存变量反复调用（值 receiver 方法值闭包按值捕获 264B receiver）；
//   - 尾调用链 chainA→chainB→…→chainLeaf：每层尾位置调用，大值层层穿透，
//     链中 runtime.GC() 在「上层返回槽未写」窗口扫栈；
//   - dirtyRec 以深度 d=3+r%96 递归把各层栈帧刷满指向短命堆对象的指针，depth 0
//     处 GC 清扫成悬挂（脏栈）；ifaceRec/mvalRec/chain 同型同深度下降，未初始化
//     的返回槽落在旧 dirtyRec 帧的脏字上；
//   - victim 写前 GC（未初始化窗口）+ 写后 GC；每次调用后 consumeBig（noinline
//     GC helper）GC 前后各校验一遍返回值 canary；weak 强引用断言。
//
// 官方 Go 恒 PASS；崩溃/漏标仅随 gollvm 出现（wrapper 尾调用栈图/返回槽问题）。
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
	"weak"
)

const (
	scenario = "y05_iface_tailret_slot"

	bigSlots   = 8
	bigWords   = 16
	depthJitter = 96
	poisonN     = 48

	poisonIDBase = int64(1) << 50
)

type node struct {
	id  int64
	v   int64
	pad [2]uint64
}

// Big 按值返回/按值 receiver（264B：16 指针 + 16 words + seq，超寄存器阈值，
// 强制经调用方返回槽返回）。
type Big struct {
	a     [bigSlots]*node
	b     [bigSlots]*node
	words [bigWords]uint64
	seq   int64
}

// getter 接口：多实现（值/指针 receiver）。
type getter interface {
	Get() Big
}

// Get 定义在 Big 上：receiver 本身 264B 按值拷贝；经 itab 派发时 wrapper
// 从接口数据指针复制 receiver 并（可能尾调用）进入本方法。callee 内写前 GC
// 扫描调用方仍未写入的返回槽（缺陷窗口）。
//
//go:noinline
func (b Big) Get() Big {
	runtime.GC()
	atomic.AddInt64(&gcCycles, 1)
	r := b
	r.seq = b.seq + 1
	r.words[0] ^= uint64(b.seq)
	return r
}

// bigImpl：值 receiver 复合实现（296B receiver，接口数据区堆拷贝）。
type bigImpl struct {
	x   Big
	key uint64
	pad [3]uint64
}

//go:noinline
func (b bigImpl) Get() Big {
	runtime.GC()
	atomic.AddInt64(&gcCycles, 1)
	r := b.x
	r.seq = int64(b.key)
	r.words[bigWords-1] ^= b.key
	return r
}

// ptrImpl：指针 receiver 实现（itab 数据字直接存指针，无 receiver 拷贝）。
type ptrImpl struct {
	x   Big
	tag uint64
}

//go:noinline
func (p *ptrImpl) Get() Big {
	if p == nil {
		failf("phase=y05.ptrImpl.Get: nil receiver")
	}
	runtime.GC()
	atomic.AddInt64(&gcCycles, 1)
	r := p.x
	r.seq = r.seq*2 + int64(p.tag)
	return r
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
		failf("phase=%s: nil *node want id=%d (UAF/帧扫描漏标)", phase, wantID)
	}
	if n.id != wantID {
		failf("phase=%s: id=%d want=%d (返回值被改写/复用)", phase, n.id, wantID)
	}
	if n.v != nodeMagic(seed, n.id) {
		failf("phase=%s id=%d: canary v=%#x want=%#x (UAF/帧扫描漏标)", phase, n.id, n.v, nodeMagic(seed, n.id))
	}
	atomic.AddInt64(&canaryChecks, 1)
}

func makeBig(seed uint64, base int64) Big {
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

// checkReturned 逐字段校验 Get() 返回值：节点指针与期望 nodeBase 对齐，
// seq/words 按各实现变换的期望值核对。
//
//go:noinline
func checkReturned(seed uint64, nodeBase, wantSeq int64, wantWords [bigWords]uint64, rv Big, phase string) {
	for i := 0; i < bigSlots; i++ {
		checkNode(rv.a[i], nodeBase+int64(i), seed, phase+".a")
		checkNode(rv.b[i], nodeBase+int64(bigSlots+i), seed, phase+".b")
	}
	for i := range rv.words {
		if rv.words[i] != wantWords[i] {
			failf("phase=%s: words[%d]=%#x want=%#x (返回值被改写)", phase, i, rv.words[i], wantWords[i])
		}
	}
	if rv.seq != wantSeq {
		failf("phase=%s: seq=%d want=%d (返回值被改写)", phase, rv.seq, wantSeq)
	}
	runtime.KeepAlive(&rv)
}

// consumeBig：每次调用后的 noinline GC helper——GC 前后各校验一遍返回值 canary。
//
//go:noinline
func consumeBig(seed uint64, nodeBase, wantSeq int64, wantWords [bigWords]uint64, rv Big, phase string) int64 {
	checkReturned(seed, nodeBase, wantSeq, wantWords, rv, phase+".pre-gc")
	runtime.GC()
	atomic.AddInt64(&gcCycles, 1)
	checkReturned(seed, nodeBase, wantSeq, wantWords, rv, phase+".post-gc")
	var sum int64
	for i := 0; i < bigSlots; i++ {
		sum += rv.a[i].v + rv.b[i].v
	}
	return sum + rv.seq
}

// ---------- 脏栈制造 + 同构下降 ----------

// fillDirtyBig 把 Big 的全部指针字段刷成指向短命堆对象的指针（帧刷脏）。
func fillDirtyBig(c *Big, seed uint64, base int64) {
	for i := 0; i < bigSlots; i++ {
		c.a[i] = newNode(seed, base+int64(i))
		c.b[i] = newNode(seed, base+int64(bigSlots+i))
	}
	for i := range c.words {
		c.words[i] = uint64(base) + uint64(i)
	}
	c.seq = base
}

// checksumBig 读取全部指针字段：使刷脏写成为活代码（防 DSE），且 c 的最后使用点
// 先于递归调用——递归调用的 safepoint 处这些槽已死，depth 0 的 GC 把刷入的
// 短命对象清扫成悬挂，此后各层帧字节 = 悬挂指针（脏栈）。
//
//go:noinline
func checksumBig(c *Big) int64 {
	var s int64
	for i := 0; i < bigSlots; i++ {
		s += c.a[i].v + c.b[i].v
	}
	return s
}

// dirtyRec：以深度 d 递归把各层栈帧刷满指向短命堆对象的指针，depth 0 处
// runtime.GC() 清扫成悬挂。carrier 传给子帧，使子帧的入参区也是脏的。
//
//go:noinline
func dirtyRec(seed uint64, base int64, d int, carrier Big) int64 {
	c := carrier
	fillDirtyBig(&c, seed, base)
	sum := checksumBig(&c)
	if d == 0 {
		runtime.GC()
		atomic.AddInt64(&gcCycles, 1)
		return sum
	}
	r := dirtyRec(seed, base+1000, d-1, c)
	return sum + r
}

// victimIface：写前 GC 扫描未初始化的返回槽（脏字节窗口），经 itab 派发的
// g.Get() 晚写入，写后 GC 校验已写入指针必须被标记存活。
//
//go:noinline
func victimIface(seed uint64, base int64, g getter) (info Big) {
	runtime.GC()                    // 写前 GC：返回槽未初始化窗口被扫描（脏字节）
	atomic.AddInt64(&gcCycles, 1)
	poison := poisonAlloc(seed, poisonN) // 同 size class 短命对象：吞掉刚释放的 span
	info = g.Get()                       // 经 itab 派发（wrapper 尾调用）晚写入
	runtime.GC()                         // 写后 GC：已写入的合法指针必须仍被扫描
	atomic.AddInt64(&gcCycles, 1)
	atomic.AddInt64(&ifaceCalls, 1)
	runtime.KeepAlive(poison)
	return info
}

// victimMval：同 victimIface，但经方法值闭包调用（receiver 由闭包持有）。
//
//go:noinline
func victimMval(seed uint64, base int64, mv func() Big) (info Big) {
	runtime.GC()
	atomic.AddInt64(&gcCycles, 1)
	poison := poisonAlloc(seed, poisonN)
	info = mv()
	runtime.GC()
	atomic.AddInt64(&gcCycles, 1)
	atomic.AddInt64(&methodCalls, 1)
	runtime.KeepAlive(poison)
	return info
}

// ifaceRec：与 dirtyRec 同型同深度下降（相同 byval carrier 保证帧尺寸一致），
// 同样刷脏——保证下降链每层帧都落在旧 dirtyRec 帧的脏字节上。depth 0 处调用
// victimIface：其返回槽落在本帧（= 旧 dirtyRec 帧）的脏字上。
//
//go:noinline
func ifaceRec(seed uint64, base int64, d int, g getter, carrier Big) (info Big) {
	c := carrier
	fillDirtyBig(&c, seed, base)
	sum := checksumBig(&c)
	if d == 0 {
		info = victimIface(seed, base, g)
		_ = sum
		return info
	}
	info = ifaceRec(seed, base+1000, d-1, g, c)
	_ = sum
	return info
}

// mvalRec：同构下降的方法值版本。
//
//go:noinline
func mvalRec(seed uint64, base int64, d int, mv func() Big, carrier Big) (info Big) {
	c := carrier
	fillDirtyBig(&c, seed, base)
	sum := checksumBig(&c)
	if d == 0 {
		info = victimMval(seed, base, mv)
		_ = sum
		return info
	}
	info = mvalRec(seed, base+1000, d-1, mv, c)
	_ = sum
	return info
}

// ---------- 尾调用链（D20 GoTailCall 路径） ----------

// chainA/chainB 交替尾位置调用（最后一条语句是 call），返回大值层层穿透；
// 链中 GC 在「上层返回槽未写」窗口扫栈。carrier 逐层刷脏，使尾调用复用的
// 帧区域保持脏字节。
//
//go:noinline
func chainA(seed uint64, base int64, d int, carrier Big) Big {
	c := carrier
	fillDirtyBig(&c, seed, base)
	sum := checksumBig(&c)
	if d == 0 {
		return chainLeaf(seed, base, c)
	}
	_ = sum
	return chainB(seed, base+1000, d-1, c) // 尾位置调用
}

//go:noinline
func chainB(seed uint64, base int64, d int, carrier Big) Big {
	c := carrier
	fillDirtyBig(&c, seed, base)
	sum := checksumBig(&c)
	if d&7 == 0 || d < 3 {
		runtime.GC() // 链中 GC：上层返回槽未写窗口被扫描
		atomic.AddInt64(&gcCycles, 1)
	}
	if d == 0 {
		return chainLeaf(seed, base, c)
	}
	_ = sum
	return chainA(seed, base+1000, d-1, c) // 尾位置调用
}

// chainLeaf：链末（c 角色）——写前 GC 后构造返回值，落入最上层调用者的返回槽。
//
//go:noinline
func chainLeaf(seed uint64, base int64, carrier Big) Big {
	c := carrier
	fillDirtyBig(&c, seed, base)
	sum := checksumBig(&c)
	runtime.GC() // 写前 GC：最终返回槽仍为脏字节
	atomic.AddInt64(&gcCycles, 1)
	r := makeBig(seed, base)
	atomic.AddInt64(&chainCalls, 1)
	_ = sum
	return r
}

// ---------- poison ----------

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

	depthSum int64
	depthMin int
	depthMax int
}

//go:noinline
func (w *worker) round(r int64) {
	base := w.base0 + r*50_000
	d := 3 + int(r%depthJitter) // 深度抖动：任意栈布局下对齐
	w.depthSum += int64(d)
	if w.depthMin == 0 || d < w.depthMin {
		w.depthMin = d
	}
	if d > w.depthMax {
		w.depthMax = d
	}

	var wantWords [bigWords]uint64
	for i := range wantWords {
		wantWords[i] = wordConst(i)
	}
	plainWords := wantWords // 无变换期望（chain/mval-ptr 等不触碰 words 的路径共用）

	// 阶段 1：接口派发（3 实现轮换：值 receiver Big / 值 receiver bigImpl / 指针 receiver）
	dirtyRec(w.seed, base+10, d, Big{})
	var g getter
	var nodeBase, wantSeq int64
	switch r % 3 {
	case 0:
		g = makeBig(w.seed, base) // Big 自身实现：264B receiver 按值拷贝
		nodeBase = base
		wantSeq = base + 1
		wantWords[0] ^= uint64(base)
	case 1:
		g = bigImpl{x: makeBig(w.seed, base+20), key: uint64(base + 21)}
		nodeBase = base + 20
		wantSeq = base + 21
		wantWords[bigWords-1] ^= uint64(base + 21)
	default:
		g = &ptrImpl{x: makeBig(w.seed, base+40), tag: uint64(base + 41)}
		nodeBase = base + 40
		wantSeq = 2*(base+40) + base + 41
	}
	info := ifaceRec(w.seed, base+100, d, g, Big{})
	sum := consumeBig(w.seed, nodeBase, wantSeq, wantWords, info, "y05.iface")

	// 阶段 2：方法值反复调（值 receiver 方法值闭包按值捕获 264B receiver）
	dirtyRec(w.seed, base+150, d, Big{})
	bv := makeBig(w.seed, base+200)
	mv := bv.Get // 方法值存变量
	mvW := plainWords
	mvW[0] ^= uint64(base + 200)
	info = mvalRec(w.seed, base+160, d, mv, Big{})
	sum += consumeBig(w.seed, base+200, base+201, mvW, info, "y05.mval-val")
	for i := 0; i < 3; i++ { // 反复调
		info = victimMval(w.seed, base+200+int64(i), mv)
		sum += consumeBig(w.seed, base+200, base+201, mvW, info, "y05.mval-val")
	}

	// 阶段 3：指针 receiver 方法值
	dirtyRec(w.seed, base+250, d, Big{})
	pt := &ptrImpl{x: makeBig(w.seed, base+300), tag: uint64(base + 301)}
	pm := pt.Get
	info = victimMval(w.seed, base+260, pm)
	sum += consumeBig(w.seed, base+300, 2*(base+300)+base+301, plainWords, info, "y05.mval-ptr")

	// 阶段 4：尾调用链 a→b→…→leaf（层层穿透）
	dirtyRec(w.seed, base+350, d, Big{})
	leafBase := base + 400 + 1000*int64(d)
	info = chainA(w.seed, base+400, d, Big{})
	sum += consumeBig(w.seed, leafBase, leafBase, plainWords, info, "y05.chain")

	// 阶段 5：weak 强引用断言——结果仍由本地强引用，GC 后必须非 nil。
	wp := weak.Make(info.a[0])
	runtime.GC()
	atomic.AddInt64(&gcCycles, 1)
	if v := wp.Value(); v == nil {
		failf("phase=y05.weak worker=%d round=%d: strong ref held but collected (返回槽漏标/UAF)", w.id, r)
	} else {
		checkNode(v, leafBase, w.seed, "y05.weak")
	}
	atomic.AddInt64(&weakChecks, 1)
	_ = sum
	runtime.KeepAlive(info)

	w.rounds++
	w.next = r + 1
	w.checks += 16*6 + 6
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
	ifaceCalls   int64
	methodCalls  int64
	chainCalls   int64
	weakChecks   int64

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
		failf("phase=preflight: Big size=%d want=264 (返回槽形态假设不成立)", s)
	}

	fmt.Printf("scenario=%s phase=preflight go=%s goos=%s goarch=%s gomaxprocs=%d seed=%d seconds=%d workers=%d fixed_rounds=%d heartbeat=%ds big_size=%d depth_jitter=%d\n",
		scenario, runtime.Version(), runtime.GOOS, runtime.GOARCH, runtime.GOMAXPROCS(0), seed, seconds, workers, fixed, hb,
		reflect.TypeOf(Big{}).Size(), depthJitter)

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
	if ifc, mc, cc := atomic.LoadInt64(&ifaceCalls), atomic.LoadInt64(&methodCalls), atomic.LoadInt64(&chainCalls); ifc != roundsTotal || mc != roundsTotal*5 || cc != roundsTotal {
		failf("phase=final: iface_calls=%d method_calls=%d chain_calls=%d want=%d/%d/%d", ifc, mc, cc, roundsTotal, roundsTotal*5, roundsTotal)
	}

	ng := waitGoroutines(baseGoroutines, 5*time.Second)
	if ng > baseGoroutines+4 {
		failf("phase=final: goroutines baseline=%d after=%d: goroutine leak", baseGoroutines, ng)
	}

	var dmin, dmax, dsum int64
	for _, w := range ws {
		dsum += w.depthSum
		if dmin == 0 || int64(w.depthMin) < dmin {
			dmin = int64(w.depthMin)
		}
		if int64(w.depthMax) > dmax {
			dmax = int64(w.depthMax)
		}
	}
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	fmt.Printf("metrics: rounds=%d canary_checks=%d iface_calls=%d method_calls=%d chain_calls=%d weak_checks=%d worker_gc=%d gc_churn=%d depth_min=%d depth_max=%d depth_avg=%d num_gc=%d heap_alloc=%d heap_objects=%d goroutines=%d\n",
		roundsTotal, atomic.LoadInt64(&canaryChecks),
		atomic.LoadInt64(&ifaceCalls), atomic.LoadInt64(&methodCalls), atomic.LoadInt64(&chainCalls),
		atomic.LoadInt64(&weakChecks), atomic.LoadInt64(&gcCycles), atomic.LoadInt64(&gcChurnCycles),
		dmin, dmax, dsum/max(roundsTotal, 1), m.NumGC, m.HeapAlloc, m.HeapObjects, ng)
	fmt.Printf("STRESS PASS %s\n", scenario)
}
