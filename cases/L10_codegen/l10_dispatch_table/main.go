// L10: 生成风格 dispatch 表：稠密 []func 表（含空洞）+ 稀疏 map 表，
// 间接调用、闭包捕获、边界/短载荷/default 路径与 GC 后表稳定性。
// P2：TORTURE_ROUNDS 放大随机扫描轮次。
package main

import (
	"fmt"
	"math/rand"
	"os"
	"runtime"
	"sort"
	"strconv"
	"sync/atomic"
)

var (
	failed  int32
	gcSink  [][]byte
	curSeed int64
)

func ck(ok bool, msg string) {
	if !ok {
		atomic.AddInt32(&failed, 1)
		fmt.Println("FAIL:", msg)
	}
}

func ckSeed(ok bool, msg string) {
	if !ok {
		atomic.AddInt32(&failed, 1)
		fmt.Printf("FAIL: %s (seed=%d)\n", msg, curSeed)
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

func finish(name string) {
	if n := atomic.LoadInt32(&failed); n != 0 {
		fmt.Printf("FAILED %s (%d failed checks)\n", name, n)
		os.Exit(1)
	}
	fmt.Println("ALL PASS " + name)
}

func envInt(name string, def int) int {
	s := os.Getenv(name)
	if s == "" {
		return def
	}
	v, err := strconv.Atoi(s)
	if err != nil || v < 1 {
		return def
	}
	return v
}

// ---- 生成风格 dispatch 表 ----

type payload struct {
	Base int64
	Data []int32
}

type handler func(*payload) (int64, bool)

const (
	denseSize    = 2048
	sparseBase   = uint32(3000)
	sparseStride = uint32(7)
	sparseCount  = 64
)

var (
	dense      [denseSize]handler
	sparse     map[uint32]handler
	sparseKeys []uint32
	denseLive  int
)

func init() {
	for i := 0; i < denseSize; i++ {
		if i%3 == 0 {
			continue // 生成器留下的空洞，走 default
		}
		op := uint32(i) // 必须按值捕获；否则所有表项共享最后一个 op
		dense[i] = func(p *payload) (int64, bool) {
			if p == nil || len(p.Data) < 4 {
				return 0, false
			}
			return p.Base*int64(op%17+1) + int64(p.Data[op%4]) + int64(op), true
		}
		denseLive++
	}
	sparse = make(map[uint32]handler, sparseCount)
	sparseKeys = make([]uint32, 0, sparseCount)
	for i := 0; i < sparseCount; i++ {
		op := sparseBase + uint32(i)*sparseStride
		sparse[op] = func(p *payload) (int64, bool) {
			if p == nil || len(p.Data) < 2 {
				return 0, false
			}
			return p.Base*7 - int64(op) + int64(p.Data[op%2]), true
		}
		sparseKeys = append(sparseKeys, op)
	}
	sort.Slice(sparseKeys, func(i, j int) bool { return sparseKeys[i] < sparseKeys[j] })
}

func dispatch(op uint32, p *payload) (int64, bool) {
	if op < uint32(len(dense)) {
		h := dense[op]
		if h == nil {
			return 0, false
		}
		return h(p)
	}
	if h, ok := sparse[op]; ok {
		return h(p)
	}
	return 0, false
}

func expected(op uint32, p *payload) (int64, bool) {
	if p == nil {
		return 0, false
	}
	if op < denseSize {
		if op%3 == 0 || len(p.Data) < 4 {
			return 0, false
		}
		return p.Base*int64(op%17+1) + int64(p.Data[op%4]) + int64(op), true
	}
	if op >= sparseBase {
		delta := op - sparseBase
		if delta%sparseStride == 0 && delta/sparseStride < sparseCount {
			if len(p.Data) < 2 {
				return 0, false
			}
			return p.Base*7 - int64(op) + int64(p.Data[op%2]), true
		}
	}
	return 0, false
}

func scanSignature(p *payload) uint64 {
	var h uint64 = 1469598103934665603
	for op := uint32(0); op <= 3441; op++ {
		v, ok := dispatch(op, p)
		h ^= uint64(v)
		h *= 1099511628211
		if ok {
			h ^= 0x9e3779b97f4a7c15
			h *= 1099511628211
		}
	}
	return h
}

func main() {
	curSeed = 1
	if s := os.Getenv("UAF_SEED"); s != "" {
		if v, err := strconv.ParseInt(s, 10, 64); err == nil {
			curSeed = v
		}
	}
	rounds := envInt("TORTURE_ROUNDS", 1)
	if rounds > 200 {
		rounds = 200
	}

	// 1. 表结构：长度、空洞、稀疏键。
	ck(len(dense) == denseSize, "dense table size")
	ck(denseLive == 1365, "dense live entries")
	ck(dense[0] == nil && dense[3] == nil && dense[2046] == nil, "dense holes")
	ck(dense[1] != nil && dense[2] != nil && dense[2047] != nil, "dense registered entries")
	ck(len(sparse) == sparseCount && len(sparseKeys) == sparseCount, "sparse table size")
	for i, k := range sparseKeys {
		if k != sparseBase+uint32(i)*sparseStride {
			ck(false, "sparse key sequence")
			break
		}
		if sparse[k] == nil {
			ck(false, "sparse nil handler")
			break
		}
	}
	ck(sparseKeys[0] == 3000 && sparseKeys[sparseCount-1] == 3441, "sparse key bounds")

	// 2. 固定载荷：全量 op 与独立公式一致；成功数 = 稠密 1365 + 稀疏 64。
	p := &payload{Base: 12345, Data: []int32{10, -20, 30, -40}}
	success := 0
	for op := uint32(0); op <= 5000; op++ {
		gv, gok := dispatch(op, p)
		wv, wok := expected(op, p)
		if gok != wok || gv != wv {
			ck(false, fmt.Sprintf("op %d dispatch=%d/%v want=%d/%v", op, gv, gok, wv, wok))
			break
		}
		if gok {
			success++
		}
	}
	ck(success == denseLive+sparseCount, "dispatch success count")
	for op := uint32(0); op <= 3441; op++ {
		if _, ok := dispatch(op, p); ok {
			if op < denseSize {
				ck(op%3 != 0, "dense success on hole")
			} else if (op-sparseBase)%sparseStride != 0 || (op-sparseBase)/sparseStride >= sparseCount {
				ck(false, "sparse success on unregistered key")
			}
		}
	}

	// 3. 负值/极值算术。
	neg := &payload{Base: -7, Data: []int32{-1, 1<<31 - 1, -1 << 31, 0}}
	for _, op := range []uint32{1, 2, 4, 5, 2047, 3000, 3007, 3441} {
		gv, gok := dispatch(op, neg)
		wv, wok := expected(op, neg)
		if gv != wv || gok != wok {
			ck(false, "negative payload mismatch")
			break
		}
	}

	// 4. 边界：nil/短 Data/越界 op。
	ck(func() bool { _, ok := dispatch(1, nil); return !ok }(), "nil payload dense")
	ck(func() bool { _, ok := dispatch(3000, nil); return !ok }(), "nil payload sparse")
	for n := 0; n < 4; n++ {
		sp := &payload{Base: 5, Data: make([]int32, n)}
		if _, ok := dispatch(1, sp); ok {
			ck(false, fmt.Sprintf("dense op with len %d must fail", n))
		}
	}
	short2 := &payload{Base: 5, Data: []int32{1, 2}}
	if _, ok := dispatch(1, short2); ok {
		ck(false, "dense needs 4 elements")
	}
	if v, ok := dispatch(3000, short2); !ok || v != 5*7-3000+int64(short2.Data[3000%2]) {
		ck(false, "sparse needs 2 elements")
	}
	if _, ok := dispatch(1, &payload{Base: 1, Data: []int32{1, 2, 3, 4}}); !ok {
		ck(false, "dense boundary len=4")
	}
	for _, op := range []uint32{2048, 2999, 3442, 100000, ^uint32(0)} {
		if _, ok := dispatch(op, p); ok {
			ck(false, fmt.Sprintf("out of range op %d must fail", op))
		}
	}

	// 5. GC：保留表项函数值、载荷与签名。
	hDense := dense[1]
	hSparse := sparse[3000]
	ck(hDense != nil && hSparse != nil, "captured handler values")
	sigBefore := scanSignature(p)
	gcSink = append(gcSink, []byte("dispatch"), []byte{1, 2, 3})
	gcStress()
	v1, ok1 := hDense(p)
	w1, _ := expected(1, p)
	ck(ok1 && v1 == w1, "dense handler value survives GC")
	v2, ok2 := hSparse(p)
	w2, _ := expected(3000, p)
	ck(ok2 && v2 == w2, "sparse handler value survives GC")
	ck(scanSignature(p) == sigBefore, "dispatch signature stable across GC")
	ck(denseLive == 1365 && len(sparse) == sparseCount, "table shape stable across GC")

	// 6. 保留载荷在 GC 后仍可分发。
	keep := make([]*payload, 8)
	keepWant := make([]string, 8)
	for i := range keep {
		keep[i] = &payload{Base: int64(i*17 - 50), Data: []int32{int32(i), int32(-i), int32(i * 2), int32(-i * 3)}}
		keepWant[i] = fmt.Sprintf("%d/%v", keep[i].Base, keep[i].Data)
	}
	gcStress()
	for i, kp := range keep {
		v, ok := dispatch(uint32(1+i*7), kp)
		w, wok := expected(uint32(1+i*7), kp)
		ck(ok == wok && v == w, "retained payload dispatch after GC")
		ck(fmt.Sprintf("%d/%v", kp.Base, kp.Data) == keepWant[i], "retained payload content after GC")
	}

	// 7. 随机扫描：TORTURE_ROUNDS 放大轮次。
	r := rand.New(rand.NewSource(curSeed))
	total := 30 * rounds
	var hash uint64 = 1469598103934665603
	for round := 0; round < total; round++ {
		n := r.Intn(7)
		data := make([]int32, n)
		for i := range data {
			data[i] = int32(r.Intn(1<<20) - 1<<19)
		}
		rp := &payload{Base: int64(r.Intn(2000001) - 1000000), Data: data}
		for k := 0; k < 600; k++ {
			op := uint32(r.Intn(5001))
			gv, gok := dispatch(op, rp)
			wv, wok := expected(op, rp)
			if gv != wv || gok != wok {
				ckSeed(false, fmt.Sprintf("round %d op %d dispatch=%d/%v want=%d/%v", round, op, gv, gok, wv, wok))
				break
			}
			hash ^= uint64(gv)
			hash *= 1099511628211
			if gok {
				hash ^= uint64(op) + 0x9e3779b97f4a7c15
				hash *= 1099511628211
			}
		}
		if round%6 == 0 {
			gcStress()
			ckSeed(scanSignature(p) == sigBefore, fmt.Sprintf("round %d signature after GC", round))
			kp := keep[round%len(keep)]
			gv, gok := dispatch(1, kp)
			wv, wok := expected(1, kp)
			ckSeed(gok == wok && gv == wv, fmt.Sprintf("round %d retained payload mismatch", round))
		}
	}
	ck(hash != 0, "random sweep exercised")

	finish("l10_dispatch_table")
}
