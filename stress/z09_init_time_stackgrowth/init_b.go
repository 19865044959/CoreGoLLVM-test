// Z09 init_2 / init_3。
//
// init_2：map[int]*node 循环写入 1000 项（mapassign 触发扩容），每 97 次插入调用一次
// noinline 深递归（栈增长与 mapassign 交错——go#41816 形态），每 211 次插入一次 GC；
// 结束后 runtime.GC() 并全量校验 map。
//
// init_3：包级变量初始化链 gA→gB→gC（依赖顺序由 Go 初始化依赖规则保证）+ GC +
// 跨 init 顺序依赖校验（init_1 → init_2 → init_3）。
package main

import "runtime"

const (
	init2N     = 1000
	init2Depth = 96
)

var (
	init2Map         map[int]*initNode
	init2MapChecksum int64
	init2Stage       int

	// 包级变量初始化链：gB 依赖 gA，gC 依赖 gB。
	gA = buildA()
	gB = buildB(gA)
	gC = buildC(gB)
)

func buildA() *initNode {
	return &initNode{id: gAID, v: initMagic(gAID), tag: "gA"}
}

func buildB(a *initNode) *initNode {
	if a == nil || a.id != gAID || a.v != initMagic(gAID) || a.tag != "gA" {
		failf("phase=globalvar: gA not initialized before gB (got %v)", a)
	}
	return &initNode{id: gBID, v: a.v ^ initMagic(gBID), tag: "gB"}
}

func buildC(b *initNode) *initNode {
	if b == nil || b.id != gBID || b.tag != "gB" {
		failf("phase=globalvar: gB not initialized before gC (got %v)", b)
	}
	return &initNode{id: gCID, v: b.v ^ initMagic(gCID), tag: "gC"}
}

func checkGlobalChain(phase string) {
	if gA == nil || gB == nil || gC == nil {
		failf("phase=%s: global init chain gA/gB/gC has nil", phase)
	}
	if gA.id != gAID || gA.v != initMagic(gAID) || gA.tag != "gA" {
		failf("phase=%s: gA corrupted id=%d v=%#x tag=%q", phase, gA.id, gA.v, gA.tag)
	}
	if gB.id != gBID || gB.v != (gA.v^initMagic(gBID)) || gB.tag != "gB" {
		failf("phase=%s: gB corrupted id=%d v=%#x tag=%q (gA->gB dependency)", phase, gB.id, gB.v, gB.tag)
	}
	if gC.id != gCID || gC.v != (gB.v^initMagic(gCID)) || gC.tag != "gC" {
		failf("phase=%s: gC corrupted id=%d v=%#x tag=%q (gB->gC dependency)", phase, gC.id, gC.v, gC.tag)
	}
}

func init() {
	init2Runs++
	if init1Stage != 1 {
		failf("phase=init2: init_1 has not completed (stage=%d)", init1Stage)
	}

	m := make(map[int]*initNode) // 不预设容量：让 mapassign 自己触发扩容
	var sum int64
	for i := 0; i < init2N; i++ {
		id := init2IDBase + int64(i)
		n := &initNode{id: id, v: initMagic(id), tag: "init2"}
		m[i] = n
		sum += n.v
		if i%97 == 0 {
			// 栈增长（morestack）与 mapassign/扩容交错。
			got := deepRec(init2Depth, int64(i))
			if want := deepExpected(init2Depth, int64(i)); got != want {
				failf("phase=init2: deep recursion at i=%d sum=%d want=%d", i, got, want)
			}
		}
		if i%211 == 0 {
			runtime.GC()
		}
	}
	runtime.GC()

	if len(m) != init2N {
		failf("phase=init2: map len=%d want=%d", len(m), init2N)
	}
	for i := 0; i < init2N; i++ {
		n, ok := m[i]
		id := init2IDBase + int64(i)
		if !ok || n == nil || n.id != id || n.v != initMagic(id) || n.tag != "init2" {
			failf("phase=init2: map[%d] corrupted after grows+GC", i)
		}
	}

	init2Map = m
	init2MapChecksum = sum
	init2Stage = 2
	initOrder = append(initOrder, 2)
}

func init() {
	init3Runs++
	if init2Stage != 2 {
		failf("phase=init3: init_2 has not completed (stage=%d)", init2Stage)
	}
	runtime.GC()
	// gA/gB/gC 已在任何 init 之前由包级 var 初始化链建好，此处校验依赖顺序与内容。
	checkGlobalChain("init3")
	init3Stage = 1
	initOrder = append(initOrder, 3)
}
