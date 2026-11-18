// L17: struct 按值返回后立刻作为 defer 实参。返回的聚合先落 caller 帧，
// 再整体复制进 defer 记录；注册后局部变量被清零，defer 中的副本跨 GC 校验。
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

type boxed struct {
	v    int64
	name string
}

type subAgg struct {
	head  *item
	list  []*item
	label string
}

type bundle struct {
	id   int64
	a    *item
	arr  [3]*item
	sub  subAgg
	strs []string
	anyv any
}

var (
	failed  int32
	uafSeed int64 = 1
)

func ck(ok bool, msg string) {
	if !ok {
		atomic.AddInt32(&failed, 1)
		fmt.Println("FAIL:", msg)
	}
}

//go:noinline
func gcStress() {
	var sink [][]byte
	for i := 0; i < 8; i++ {
		b := make([]byte, 2048)
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

func finish(name string) {
	if n := atomic.LoadInt32(&failed); n != 0 {
		fmt.Printf("FAILED %s (%d failed checks) seed=%d\n", name, n, uafSeed)
		os.Exit(1)
	}
	fmt.Println("ALL PASS " + name)
}

//go:noinline
func mkBundle(base int64, ptrAny bool) bundle {
	anyv := any(boxed{v: base + 30, name: "any"})
	if ptrAny {
		anyv = &item{v: base + 30, name: "any"}
	}
	return bundle{
		id:   base,
		a:    &item{v: base + 1, name: "a"},
		arr:  [3]*item{{v: base + 10, name: "x0"}, {v: base + 11, name: "x1"}, {v: base + 12, name: "x2"}},
		sub:  subAgg{head: &item{v: base + 20, name: "h"}, list: []*item{{v: base + 21, name: "l0"}, {v: base + 22, name: "l1"}}, label: "sub"},
		strs: []string{"s0", "s1"},
		anyv: anyv,
	}
}

func bundleSum(base int64) int64 {
	return base + (base + 1) +
		(base + 10) + (base + 11) + (base + 12) +
		(base + 20) + (base + 21) + (base + 22) +
		int64(len("s0")+len("s1")) + (base + 30)
}

func verifySub(s subAgg, base int64, where string) int64 {
	if s.head == nil || s.head.v != base+20 || s.head.name != "h" {
		ck(false, fmt.Sprintf("%s: sub.head corrupted seed=%d", where, uafSeed))
		return 0
	}
	if len(s.list) != 2 || s.list[0] == nil || s.list[1] == nil {
		ck(false, fmt.Sprintf("%s: sub.list corrupted seed=%d", where, uafSeed))
		return 0
	}
	if s.list[0].v != base+21 || s.list[1].v != base+22 || s.label != "sub" {
		ck(false, fmt.Sprintf("%s: sub fields corrupted seed=%d", where, uafSeed))
		return 0
	}
	return s.head.v + s.list[0].v + s.list[1].v
}

func verifyBundle(b bundle, base int64, where string, ptrAny bool) int64 {
	if b.id != base {
		ck(false, fmt.Sprintf("%s: id=%d want=%d seed=%d", where, b.id, base, uafSeed))
		return 0
	}
	if b.a == nil || b.a.v != base+1 || b.a.name != "a" || b.a.next != nil {
		ck(false, fmt.Sprintf("%s: a corrupted seed=%d", where, uafSeed))
		return 0
	}
	sum := b.id + b.a.v
	for i := 0; i < 3; i++ {
		if b.arr[i] == nil || b.arr[i].v != base+10+int64(i) {
			ck(false, fmt.Sprintf("%s: arr[%d] corrupted seed=%d", where, i, uafSeed))
			return 0
		}
		sum += b.arr[i].v
	}
	sum += verifySub(b.sub, base, where)
	if len(b.strs) != 2 || b.strs[0] != "s0" || b.strs[1] != "s1" {
		ck(false, fmt.Sprintf("%s: strs corrupted seed=%d", where, uafSeed))
		return 0
	}
	sum += int64(len(b.strs[0]) + len(b.strs[1]))
	if ptrAny {
		p, ok := b.anyv.(*item)
		if !ok || p == nil || p.v != base+30 || p.name != "any" {
			ck(false, fmt.Sprintf("%s: anyv(*item) corrupted seed=%d", where, uafSeed))
			return 0
		}
		sum += p.v
	} else {
		x, ok := b.anyv.(boxed)
		if !ok || x.v != base+30 || x.name != "any" {
			ck(false, fmt.Sprintf("%s: anyv(boxed) corrupted seed=%d", where, uafSeed))
			return 0
		}
		sum += x.v
	}
	return sum
}

//go:noinline
func takeBundle(b bundle, base int64, ptrAny bool, sum *int64, where string) {
	gcStress()
	s1 := verifyBundle(b, base, where+"-before", ptrAny)
	gcStress()
	s2 := verifyBundle(b, base, where+"-after", ptrAny)
	ck(s1 == s2, fmt.Sprintf("%s: defer-record copy changed across GC seed=%d", where, uafSeed))
	*sum += s2
}

//go:noinline
func run(base int64, ptrAny bool, where string) (sum int64) {
	b := mkBundle(base, ptrAny)
	if got := verifyBundle(b, base, where+"-before", ptrAny); got != bundleSum(base) {
		ck(false, fmt.Sprintf("%s: returned struct checksum=%d want=%d seed=%d", where, got, bundleSum(base), uafSeed))
	}
	// 聚合参数在 defer 语句处整体求值并复制进 defer 记录。
	defer takeBundle(b, base, ptrAny, &sum, where)
	// 清空局部变量：defer 记录必须独立持有那份副本。
	b = bundle{}
	gcStress()
	ck(b.a == nil && b.sub.head == nil && b.anyv == nil,
		fmt.Sprintf("%s: local bundle not cleared seed=%d", where, uafSeed))
	return 0
}

//go:noinline
func useReturned(base int64, ptrAny bool) (int64, *item) {
	b := mkBundle(base, ptrAny)
	gcStress()
	sum := verifyBundle(b, base, "useReturned", ptrAny)
	tail := b.sub.list[1]
	gcStress()
	return sum, tail
}

//go:noinline
func splitReturn(base int64, ptrAny bool) (bundle, subAgg) {
	b := mkBundle(base, ptrAny)
	s := b.sub
	gcStress()
	return b, s
}

func main() {
	uafSeed = seedFromEnv()
	for r := 0; r < 4; r++ {
		base := int64(100*r) + uafSeed%17
		ptrAny := (int(r)+int(uafSeed))%2 == 0

		sum := run(base, ptrAny, fmt.Sprintf("run-%d", r))
		gcStress()
		ck(sum == bundleSum(base),
			fmt.Sprintf("round %d: deferred struct checksum=%d want=%d seed=%d", r, sum, bundleSum(base), uafSeed))

		s, tail := useReturned(base+50, ptrAny)
		gcStress()
		ck(s == bundleSum(base+50),
			fmt.Sprintf("round %d: useReturned checksum=%d want=%d seed=%d", r, s, bundleSum(base+50), uafSeed))
		ck(tail != nil && tail.v == base+72 && tail.name == "l1",
			fmt.Sprintf("round %d: returned field pointer corrupted seed=%d", r, uafSeed))

		b, sub := splitReturn(base+80, ptrAny)
		gcStress()
		ck(verifyBundle(b, base+80, "split-bundle", ptrAny) == bundleSum(base+80),
			fmt.Sprintf("round %d: split bundle corrupted seed=%d", r, uafSeed))
		ck(verifySub(sub, base+80, "split-sub") != 0,
			fmt.Sprintf("round %d: split sub corrupted seed=%d", r, uafSeed))
	}

	finish("l17_struct_return_defer_arg")
}
