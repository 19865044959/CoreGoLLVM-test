// L17: 嵌套 interface（closer 嵌入 toucher）的 defer。覆盖 any→接口断言、
// defer 注册后重绑定接口变量、方法表达式 defer、nil receiver 接口与 recover
// 链；每个方法/defer 内 GC 并校验旧 receiver。
package main

import (
	"fmt"
	"os"
	"runtime"
	"strconv"
	"sync/atomic"
)

type item struct {
	v    int64
	name string
	next *item
}

type closer interface {
	Close(flag int) int64
	Name() string
}

type toucher interface {
	closer
	Touch() int64
}

type resImpl struct {
	head  *item
	list  []*item
	tag   string
	calls int64
}

type nilRes struct{}

var (
	failed   int32
	uafSeed  int64 = 1
	nilCalls int64
)

func ck(ok bool, msg string) {
	if !ok {
		atomic.AddInt32(&failed, 1)
		fmt.Println("FAIL:", msg)
	}
}

func gcStress() {
	var sink [][]byte
	for i := 0; i < 3; i++ {
		b := make([]byte, 1024)
		b[0] = byte(i)
		sink = append(sink, b)
	}
	runtime.GC()
	runtime.GC()
	runtime.KeepAlive(sink)
}

func seedFromEnv() int64 {
	if s := os.Getenv("UAF_SEED"); s != "" {
		if v, err := strconv.ParseInt(s, 10, 64); err == nil && v != 0 {
			return v
		}
	}
	return 1
}

func tortureRounds(def, capRounds int) int {
	n := def
	if s := os.Getenv("TORTURE_ROUNDS"); s != "" {
		if v, err := strconv.Atoi(s); err == nil && v > 0 {
			n *= v
			if n > capRounds {
				n = capRounds
			}
		}
	}
	return n
}

func finish(name string) {
	if n := atomic.LoadInt32(&failed); n != 0 {
		fmt.Printf("FAILED %s (%d failed checks) seed=%d\n", name, n, uafSeed)
		os.Exit(1)
	}
	fmt.Println("ALL PASS " + name)
}

//go:noinline
func (r *resImpl) Close(flag int) int64 {
	gcStress()
	if r == nil {
		ck(false, fmt.Sprintf("resImpl.Close: nil receiver seed=%d", uafSeed))
		return 0
	}
	if r.head == nil || r.head.v != 11 || r.head.next == nil || r.head.next.v != 12 {
		ck(false, fmt.Sprintf("resImpl.Close: head corrupted flag=%d seed=%d", flag, uafSeed))
	}
	if len(r.list) < 2 || r.list[0] == nil || r.list[0].v != 21 ||
		r.list[1] == nil || r.list[1].v != 22 || r.tag != "res" {
		ck(false, fmt.Sprintf("resImpl.Close: list/tag corrupted flag=%d seed=%d", flag, uafSeed))
	}
	r.calls++
	r.list = append(r.list, &item{v: 100 + int64(flag), name: "closed"})
	return r.head.v + r.list[0].v + r.list[1].v + int64(flag)
}

func (r *resImpl) Name() string { return r.tag }

//go:noinline
func (r *resImpl) Touch() int64 {
	gcStress()
	if r == nil || r.head == nil || r.head.v != 11 || r.head.next == nil || r.head.next.v != 12 {
		ck(false, fmt.Sprintf("resImpl.Touch: head corrupted seed=%d", uafSeed))
		return 0
	}
	return r.head.v + r.head.next.v
}

//go:noinline
func (n *nilRes) Close(flag int) int64 {
	gcStress()
	atomic.AddInt64(&nilCalls, 1)
	ck(n == nil, fmt.Sprintf("nilRes.Close: receiver not nil seed=%d", uafSeed))
	return int64(flag)
}

func (n *nilRes) Name() string { return "nilres" }

//go:noinline
func (n *nilRes) Touch() int64 {
	gcStress()
	return 0
}

// round 注册 5 个 defer 后 panic；recover defer 恢复后其余 defer 继续执行。
// 命名返回值在 panic 恢复后返回，用于 caller 跨 GC 校验。
func round(rnd int) (r *resImpl, other *resImpl) {
	r = &resImpl{
		head: &item{v: 11, name: "h", next: &item{v: 12, name: "t"}},
		list: []*item{{v: 21, name: "l"}, {v: 22, name: "l"}},
		tag:  "res",
	}
	var ti toucher = r
	boxed := any(ti)
	got, ok := boxed.(toucher)
	ck(ok, fmt.Sprintf("round %d: toucher assertion failed seed=%d", rnd, uafSeed))
	if !ok {
		return r, nil
	}
	ck(got.Touch() == 23, fmt.Sprintf("round %d: Touch got wrong seed=%d", rnd, uafSeed))

	old := got

	// defer 1：接口方法，receiver 在注册时求值。
	defer got.Close(7)

	other = &resImpl{
		head: &item{v: 91, name: "oh", next: &item{v: 92, name: "ot"}},
		list: []*item{{v: 81, name: "ol"}},
		tag:  "other",
	}
	ti = other // 重绑定接口变量，不得影响 defer 记录

	// defer 2：方法表达式 + 接口实参。
	fn := closer.Close
	defer fn(old, 5)

	// defer 3：闭包捕获旧接口。
	defer func() {
		gcStress()
		ck(old.Touch() == 23, fmt.Sprintf("round %d: closure-captured toucher corrupted seed=%d", rnd, uafSeed))
	}()

	// defer 4：recover（最先执行的 defer 之一）。
	defer func() {
		if rec := recover(); rec != nil {
			gcStress()
			ck(old.Name() == "res", fmt.Sprintf("round %d: recovered Name corrupted seed=%d", rnd, uafSeed))
			_ = old.Touch()
		}
	}()

	// defer 5：nil receiver 接口方法（最先执行）。
	var nc closer = (*nilRes)(nil)
	defer nc.Close(1)

	gcStress()
	panic("iface")
}

func main() {
	uafSeed = seedFromEnv()
	rounds := tortureRounds(16, 512)

	for rnd := 0; rnd < rounds; rnd++ {
		r, other := round(rnd)
		gcStress()

		ck(r != nil && r.head != nil && r.head.v == 11 && r.head.next != nil && r.head.next.v == 12,
			fmt.Sprintf("round %d: returned receiver head corrupted seed=%d", rnd, uafSeed))
		ck(r != nil && r.calls == 2, fmt.Sprintf("round %d: Close calls=%d seed=%d", rnd, r.calls, uafSeed))
		ck(r != nil && len(r.list) == 4, fmt.Sprintf("round %d: list len=%d seed=%d", rnd, len(r.list), uafSeed))
		if r != nil && len(r.list) == 4 {
			ck(r.list[2] != nil && r.list[2].v == 105,
				fmt.Sprintf("round %d: list[2] corrupted seed=%d", rnd, uafSeed))
			ck(r.list[3] != nil && r.list[3].v == 107,
				fmt.Sprintf("round %d: list[3] corrupted seed=%d", rnd, uafSeed))
		}
		ck(r != nil && r.tag == "res", fmt.Sprintf("round %d: tag corrupted seed=%d", rnd, uafSeed))
		ck(other != nil && other.head != nil && other.head.v == 91 && other.calls == 0,
			fmt.Sprintf("round %d: rebound other corrupted seed=%d", rnd, uafSeed))

		gcStress()
		ck(r.head.v == 11 && r.list[3].v == 107 && other.head.v == 91,
			fmt.Sprintf("round %d: after second GC seed=%d", rnd, uafSeed))
	}

	ck(atomic.LoadInt64(&nilCalls) == int64(rounds),
		fmt.Sprintf("nil receiver Close calls=%d want=%d seed=%d", nilCalls, rounds, uafSeed))

	finish("l17_iface_defer_nested")
}
