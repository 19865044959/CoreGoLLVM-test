// L17: select 的 send/case-body/closed/nil 各路径上注册 defer。defer 记录与
// 闭包捕获的 struct 指针在 select 返回后继续存活，跨 GC 校验。
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

type record struct {
	id   int64
	head *item
	list []*item
	tag  string
}

var (
	failed     int32
	uafSeed    int64 = 1
	closedHits int32
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

func tortureScale() int {
	n := 1
	if s := os.Getenv("TORTURE_ROUNDS"); s != "" {
		if v, err := strconv.Atoi(s); err == nil && v > 0 {
			n *= v
			if n > 48 {
				n = 48
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

func mkRecord(id int64) record {
	return record{
		id:   id,
		head: &item{v: id, name: "rh", next: &item{v: id + 1, name: "rn"}},
		list: []*item{{v: id + 2, name: "rl"}, {v: id + 3, name: "rl"}},
		tag:  "rec",
	}
}

func checkRecord(r record, where string, rnd int) {
	bad := r.head == nil || r.head.v != r.id || r.head.name != "rh" ||
		r.head.next == nil || r.head.next.v != r.id+1 || r.head.next.name != "rn" ||
		len(r.list) != 2 || r.tag != "rec"
	if !bad {
		for i, it := range r.list {
			if it == nil || it.v != r.id+2+int64(i) || it.name != "rl" {
				bad = true
				break
			}
		}
	}
	ck(!bad, fmt.Sprintf("round %d %s: record %d corrupted after GC seed=%d", rnd, where, r.id, uafSeed))
}

// sendOnce 在 select 前注册 defer；send case 总是就绪，nil channel 被禁用。
//
//go:noinline
func sendOnce(out chan<- record, nilCh <-chan record, r record, rnd int) (ok bool) {
	defer func() {
		gcStress()
		checkRecord(r, "send-defer", rnd)
	}()
	select {
	case out <- r:
		ok = true
	case <-nilCh:
		ck(false, fmt.Sprintf("round %d: nil channel selected seed=%d", rnd, uafSeed))
	default:
		ok = false
	}
	gcStress()
	return
}

// step 在两个就绪 channel 间随机选择；defer 注册在 case body 内。
//
//go:noinline
func step(inA, inB <-chan record, nilCh <-chan record, rnd int) (id int64, side int, ok bool) {
	var r record
	select {
	case r = <-inA:
		side = 1
		defer func() {
			gcStress()
			checkRecord(r, "caseA-defer", rnd)
		}()
	case r = <-inB:
		side = 2
		defer func() {
			gcStress()
			checkRecord(r, "caseB-defer", rnd)
		}()
	case <-nilCh:
		return 0, 0, false
	}
	gcStress()
	return r.id, side, true
}

// closedStep 只面对已关闭 channel 与 nil channel，验证 ok==false 路径的 defer。
//
//go:noinline
func closedStep(ch <-chan record, nilCh <-chan record, rnd int) (gotZero bool) {
	defer func() {
		gcStress()
		atomic.AddInt32(&closedHits, 1)
	}()
	select {
	case r, ok := <-ch:
		gotZero = !ok && r.head == nil && r.id == 0
		ck(gotZero, fmt.Sprintf("round %d: closed channel returned non-zero record seed=%d", rnd, uafSeed))
	case <-nilCh:
		ck(false, fmt.Sprintf("round %d: nil channel selected in closedStep seed=%d", rnd, uafSeed))
	}
	return
}

// preDefer 在 select 之前注册 defer，select 之后才给 r 赋值。
//
//go:noinline
func preDefer(in <-chan record, nilCh <-chan record, rnd int) (got int64, handled bool) {
	var r record
	defer func() {
		gcStress()
		if handled {
			checkRecord(r, "pre-defer", rnd)
		} else if r.head != nil {
			ck(false, fmt.Sprintf("round %d: unhandled record not zero seed=%d", rnd, uafSeed))
		}
	}()
	select {
	case r = <-in:
		handled = true
		got = r.id
	case <-nilCh:
	default:
	}
	gcStress()
	return got, handled
}

func main() {
	uafSeed = seedFromEnv()
	scale := tortureScale()

	nSend := 6 * scale
	nRecv := 4 * scale
	nPre := 4 * scale

	for rnd := 0; rnd < 2; rnd++ {
		base := int64(rnd*10000) + uafSeed%19
		var nilCh chan record

		// 阶段 1：select send case + defer。
		out := make(chan record, nSend+1)
		ids := make([]int64, 0, nSend)
		for i := 0; i < nSend; i++ {
			id := base + int64(i)*4
			ids = append(ids, id)
			ck(sendOnce(out, nilCh, mkRecord(id), rnd),
				fmt.Sprintf("round %d: sendOnce %d not ready seed=%d", rnd, i, uafSeed))
		}
		gcStress()
		for i, id := range ids {
			r := <-out
			gcStress()
			ck(r.id == id, fmt.Sprintf("round %d: sent record %d got id=%d seed=%d", rnd, i, r.id, uafSeed))
			checkRecord(r, "sent-drain", rnd)
		}
		ck(len(out) == 0, fmt.Sprintf("round %d: send channel not drained seed=%d", rnd, uafSeed))

		// 阶段 2：两个就绪 channel 的随机 select，defer 在 case body 内。
		inA := make(chan record, nRecv)
		inB := make(chan record, nRecv)
		wantSum := int64(0)
		for i := 0; i < nRecv; i++ {
			idA := base + 1000 + int64(i)*2
			idB := base + 2000 + int64(i)*2
			inA <- mkRecord(idA)
			inB <- mkRecord(idB)
			wantSum += idA + idB
		}
		gcStress()
		seenA, seenB := 0, 0
		sum := int64(0)
		for seenA+seenB < 2*nRecv {
			id, side, ok := step(inA, inB, nilCh, rnd)
			if !ok {
				ck(false, fmt.Sprintf("round %d: step failed seed=%d", rnd, uafSeed))
				break
			}
			if side == 1 {
				seenA++
			} else if side == 2 {
				seenB++
			} else {
				ck(false, fmt.Sprintf("round %d: bad side %d seed=%d", rnd, side, uafSeed))
			}
			sum += id
		}
		ck(seenA == nRecv && seenB == nRecv,
			fmt.Sprintf("round %d: seenA=%d seenB=%d want=%d seed=%d", rnd, seenA, seenB, nRecv, uafSeed))
		ck(sum == wantSum, fmt.Sprintf("round %d: select sum=%d want=%d seed=%d", rnd, sum, wantSum, uafSeed))

		// 阶段 3：已关闭 channel 的 case 与 defer。
		closed := make(chan record)
		close(closed)
		before := atomic.LoadInt32(&closedHits)
		for i := 0; i < 3; i++ {
			ck(closedStep(closed, nilCh, rnd),
				fmt.Sprintf("round %d: closedStep %d got non-zero seed=%d", rnd, i, uafSeed))
		}
		ck(atomic.LoadInt32(&closedHits)-before == 3,
			fmt.Sprintf("round %d: closedStep defer count mismatch seed=%d", rnd, uafSeed))

		// 阶段 4：select 前注册 defer，select 后赋值。
		pre := make(chan record, nPre)
		wantPre := int64(0)
		for i := 0; i < nPre; i++ {
			id := base + 3000 + int64(i)*3
			pre <- mkRecord(id)
			wantPre += id
		}
		gotPre := int64(0)
		handled := 0
		for i := 0; i < nPre; i++ {
			got, ok := preDefer(pre, nilCh, rnd)
			if ok {
				handled++
				gotPre += got
			}
		}
		ck(handled == nPre && gotPre == wantPre,
			fmt.Sprintf("round %d: preDefer handled=%d sum=%d want=%d seed=%d",
				rnd, handled, gotPre, wantPre, uafSeed))
		got, ok := preDefer(pre, nilCh, rnd)
		ck(!ok && got == 0, fmt.Sprintf("round %d: preDefer default path not taken seed=%d", rnd, uafSeed))

		gcStress()
	}

	finish("l17_select_defer_cases")
}
