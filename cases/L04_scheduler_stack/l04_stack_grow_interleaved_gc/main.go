// L04: 栈增长 + 交错显式 GC。递归每帧声明 96 个指针槽并新建堆节点，栈从
// 8KB 多次 morestack 增长到约 0.7MB；每 128 层在“部分增长的栈”上显式 GC，
// GC 后继续向更深增长，回溯时逐帧校验所有槽位指针身份。
package main

import (
	"fmt"
	"os"
	"runtime"
	"strconv"
	"sync/atomic"
)

type sgNode struct {
	v    int64
	next *sgNode
}

const (
	sgSlots = 96
	sgStage = 128
)

var (
	failed int32
	gcSink [][]byte
	sgDeep []*sgNode // 每层新建的堆节点保留到递归结束
	sgGCs  int32
)

func ck(ok bool, msg string) {
	if !ok {
		atomic.AddInt32(&failed, 1)
		fmt.Println("FAIL:", msg)
	}
}

func ckf(ok bool, format string, args ...any) {
	if !ok {
		atomic.AddInt32(&failed, 1)
		fmt.Printf("FAIL: "+format+"\n", args...)
	}
}

func gcStress() {
	for i := 0; i < 8; i++ {
		b := make([]byte, 4096)
		b[0] = byte(i)
		gcSink = append(gcSink[:0], b)
	}
	gcSink = gcSink[:0]
	runtime.GC()
	runtime.GC()
}

func finish(name string, seed int64) {
	if n := atomic.LoadInt32(&failed); n != 0 {
		fmt.Printf("FAILED %s (UAF_SEED=%d, %d failed checks)\n", name, seed, n)
		os.Exit(1)
	}
	fmt.Println("ALL PASS " + name)
}

func envSeed() int64 {
	s := os.Getenv("UAF_SEED")
	if s == "" {
		return 1
	}
	v, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return 1
	}
	return v
}

func sgRec(depth, total int, shared []*sgNode, freshVal func(int) int64) int64 {
	var slots [sgSlots]*sgNode
	for i := 0; i < sgSlots; i++ {
		slots[i] = shared[(depth+i)%len(shared)]
	}
	fresh := &sgNode{v: freshVal(depth)}
	sgDeep[depth] = fresh
	slots[sgSlots-1] = fresh

	// 在部分增长的栈上显式 GC；此时 depth 层帧的 slots 全部存活。
	if depth > 0 && depth%sgStage == 0 {
		atomic.AddInt32(&sgGCs, 1)
		gcStress()
		for i := 0; i < sgSlots; i++ {
			want := shared[(depth+i)%len(shared)]
			if i == sgSlots-1 {
				want = fresh
			}
			if slots[i] != want {
				ckf(false, "depth %d slot %d displaced by mid-growth GC", depth, i)
				break
			}
		}
	}

	var sum int64
	for _, n := range slots {
		sum += n.v
	}
	if depth+1 < total {
		sum += sgRec(depth+1, total, shared, freshVal)
	}

	// 子帧返回后再校验一次，覆盖更深栈增长后的 GC/复制影响。
	for i := 0; i < sgSlots; i++ {
		want := shared[(depth+i)%len(shared)]
		if i == sgSlots-1 {
			want = fresh
		}
		if slots[i] != want {
			ckf(false, "depth %d slot %d displaced after child unwind", depth, i)
			break
		}
	}
	if sgDeep[depth] != fresh || fresh.v != freshVal(depth) {
		ckf(false, "depth %d fresh node identity/value lost", depth)
	}
	return sum
}

func main() {
	seed := envSeed()
	total := 768 + int(seed%128)
	mul := int64(1009 + seed%17)
	freshVal := func(depth int) int64 { return seed*100003 + int64(depth)*mul }

	shared := make([]*sgNode, 8)
	for i := range shared {
		shared[i] = &sgNode{v: seed*7919 + int64(i)*131}
	}
	sgDeep = make([]*sgNode, total)

	// 先在浅栈 GC 一次，再进入增长递归。
	gcStress()
	got := sgRec(0, total, shared, freshVal)

	// 独立计算期望和：每层 95 个共享槽 + 1 个 fresh 槽 + 下一层。
	var want int64
	for depth := 0; depth < total; depth++ {
		for i := 0; i < sgSlots-1; i++ {
			want += shared[(depth+i)%len(shared)].v
		}
		want += freshVal(depth)
	}
	ckf(got == want, "recursive sum=%d want=%d at depth=%d", got, want, total)
	ckf(atomic.LoadInt32(&sgGCs) >= 5, "mid-growth GC count=%d want>=5", atomic.LoadInt32(&sgGCs))

	gcStress()
	for depth := 0; depth < total; depth++ {
		n := sgDeep[depth]
		if n == nil || n.v != freshVal(depth) {
			ckf(false, "post-GC fresh node depth=%d missing or changed", depth)
			break
		}
	}
	for i := range shared {
		if shared[i] == nil || shared[i].v != seed*7919+int64(i)*131 {
			ckf(false, "shared node %d corrupted after final GC", i)
		}
	}

	finish("l04_stack_grow_interleaved_gc", seed)
}
