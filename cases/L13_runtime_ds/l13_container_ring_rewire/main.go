// L13: container/ring 的 Link/Unlink/Move/Do 重连语义，指针 Value 在 GC 后仍有效。
package main

import (
	"container/ring"
	"fmt"
	"os"
	"runtime"
	"strconv"
	"sync/atomic"
)

type rgItem struct {
	id   int64
	name string
	pad  []byte
}

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

func envInt(name string, def int) int {
	s := os.Getenv(name)
	if s == "" {
		return def
	}
	v, err := strconv.Atoi(s)
	if err != nil || v <= 0 {
		return def
	}
	return v
}

func newRG(id, salt int64) *rgItem {
	e := &rgItem{id: id, name: fmt.Sprintf("rg-%04d-%02d", id, salt%83), pad: make([]byte, 11)}
	for i := range e.pad {
		e.pad[i] = byte(id*23 + int64(i*5) + salt)
	}
	return e
}

func rgValid(e *rgItem, id, salt int64) bool {
	if e == nil || e.id != id || e.name != fmt.Sprintf("rg-%04d-%02d", id, salt%83) || len(e.pad) != 11 {
		return false
	}
	for i := range e.pad {
		if e.pad[i] != byte(id*23+int64(i*5)+salt) {
			return false
		}
	}
	return true
}

func rgWalk(r *ring.Ring) []*rgItem {
	var out []*rgItem
	if r == nil {
		return out
	}
	r.Do(func(v any) {
		if v == nil {
			out = append(out, nil)
			return
		}
		out = append(out, v.(*rgItem))
	})
	return out
}

func rgSeqEqual(a, b []*rgItem) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func main() {
	curSeed = envSeed()

	const n = 240
	items := make([]*rgItem, n)
	for i := range items {
		items[i] = newRG(int64(i), curSeed)
	}
	head := ring.New(n)
	cur := head
	for i := 0; i < n; i++ {
		cur.Value = items[i]
		cur = cur.Next()
	}
	ck(head.Len() == n, "ring: initial len")
	ck(rgSeqEqual(rgWalk(head), items), "ring: initial order")

	// Unlink 删除 head 之后的 17 个元素（41..57），head 保留。
	at := head
	for i := 0; i < 40; i++ {
		at = at.Next()
	}
	sub := at.Unlink(17)
	ck(head.Len() == n-17, "ring: len after unlink")
	if sub == nil || sub.Len() != 17 {
		ckSeed(false, "ring: removed subring len")
	} else {
		wantSub := append([]*rgItem(nil), items[41:58]...)
		ck(rgSeqEqual(rgWalk(sub), wantSub), "ring: removed subring content")
	}
	var wantMain []*rgItem
	wantMain = append(wantMain, items[:41]...)
	wantMain = append(wantMain, items[58:]...)
	ck(rgSeqEqual(rgWalk(head), wantMain), "ring: main order after unlink")

	// 把子环插回元素 10 之后。
	insAt := head
	for i := 0; i < 10; i++ {
		insAt = insAt.Next()
	}
	insAt.Link(sub)
	ck(head.Len() == n, "ring: len after link")
	var wantLinked []*rgItem
	wantLinked = append(wantLinked, items[:11]...)
	wantLinked = append(wantLinked, items[41:58]...)
	wantLinked = append(wantLinked, items[11:41]...)
	wantLinked = append(wantLinked, items[58:]...)
	ck(rgSeqEqual(rgWalk(head), wantLinked), "ring: order after link")

	// Move 改变 head，Do 序列应旋转。
	head = head.Move(13)
	var wantMoved []*rgItem
	wantMoved = append(wantMoved, wantLinked[13:]...)
	wantMoved = append(wantMoved, wantLinked[:13]...)
	ck(rgSeqEqual(rgWalk(head), wantMoved), "ring: order after move")
	ck(head.Prev().Next() == head, "ring: prev/next consistency")

	// 反复 unlink/re-link，校验元素集合完整且无重复。
	rounds := envInt("TORTURE_ROUNDS", 3)
	seen := make([]int, n)
	for round := 0; round < rounds; round++ {
		m := 1 + int((curSeed+int64(round)*11)%31)
		seg := head.Unlink(m)
		if seg == nil || seg.Len() != m {
			ckSeed(false, fmt.Sprintf("ring: churn subring len in round %d", round))
			break
		}
		ck(head.Len() == n-m, "ring: churn len after unlink")
		t := int((curSeed + int64(round)*7) % int64(head.Len()))
		pos := head
		for i := 0; i < t; i++ {
			pos = pos.Next()
		}
		pos.Link(seg)
		ck(head.Len() == n, "ring: churn len after link")
		walk := rgWalk(head)
		if len(walk) != n {
			ckSeed(false, fmt.Sprintf("ring: churn walk len in round %d", round))
			break
		}
		for i := range seen {
			seen[i] = 0
		}
		sum := 0
		bad := false
		for _, it := range walk {
			if it == nil || it.id < 0 || int(it.id) >= n || seen[it.id] != 0 {
				bad = true
				break
			}
			seen[it.id] = 1
			sum += int(it.id)
		}
		ckSeed(!bad, fmt.Sprintf("ring: duplicate or missing element in round %d", round))
		ck(sum == n*(n-1)/2, "ring: id sum after rewire")
		gcStress()
		for i, it := range items {
			if !rgValid(it, int64(i), curSeed) {
				ckSeed(false, fmt.Sprintf("ring: payload corrupted at %d after GC round %d", i, round))
				break
			}
		}
		runtime.KeepAlive(seg)
	}

	runtime.KeepAlive(head)
	runtime.KeepAlive(items)
	runtime.KeepAlive(sub)
	finish("l13_container_ring_rewire")
}
