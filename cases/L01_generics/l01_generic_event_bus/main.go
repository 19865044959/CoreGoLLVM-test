// L01: 泛型事件总线：主题订阅/退订/发布，订阅者按注册顺序同步派发，
// 派发期间使用快照语义，跨 GC 校验事件负载指针与调用序列。
package main

import (
	"fmt"
	"os"
	"runtime"
	"strconv"
	"sync/atomic"
)

type rec struct {
	id   int64
	name string
	blob []byte
}

type handler[E any] struct {
	id int64
	fn func(E)
}

type Bus[E any] struct {
	subs   map[string][]*handler[E]
	nextID int64
}

func NewBus[E any]() *Bus[E] {
	return &Bus[E]{subs: make(map[string][]*handler[E])}
}

func (b *Bus[E]) Subscribe(topic string, fn func(E)) int64 {
	b.nextID++
	h := &handler[E]{id: b.nextID, fn: fn}
	b.subs[topic] = append(b.subs[topic], h)
	return h.id
}

func (b *Bus[E]) Unsubscribe(topic string, id int64) bool {
	list := b.subs[topic]
	for i, h := range list {
		if h.id == id {
			copy(list[i:], list[i+1:])
			list[len(list)-1] = nil
			b.subs[topic] = list[:len(list)-1]
			return true
		}
	}
	return false
}

// Publish 对当前订阅者取快照后顺序调用；派发过程中新增/退订只影响后续发布。
func (b *Bus[E]) Publish(topic string, ev E) int {
	snapshot := append([]*handler[E](nil), b.subs[topic]...)
	for _, h := range snapshot {
		h.fn(ev)
	}
	return len(snapshot)
}

func (b *Bus[E]) Count(topic string) int { return len(b.subs[topic]) }

var (
	failed  int32
	gcSink  [][]byte
	recSink []*rec
	uafSeed int64 = 1
)

func ck(ok bool, msg string) {
	if !ok {
		atomic.AddInt32(&failed, 1)
		fmt.Println("FAIL:", msg)
	}
}

func gcStress() {
	for i := 0; i < 8; i++ {
		b := make([]byte, 2048)
		b[0] = byte(i)
		gcSink = append(gcSink[:0], b)
	}
	gcSink = gcSink[:0]
	runtime.GC()
	runtime.GC()
}

func finish(name string) {
	if n := atomic.LoadInt32(&failed); n != 0 {
		fmt.Printf("FAILED %s seed=%d (%d failed checks)\n", name, uafSeed, n)
		os.Exit(1)
	}
	fmt.Println("ALL PASS " + name)
}

func envSeed() int64 {
	s := os.Getenv("UAF_SEED")
	if s == "" {
		return 1
	}
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil || n <= 0 {
		return 1
	}
	return n
}

func tortureRounds(def int) int {
	s := os.Getenv("TORTURE_ROUNDS")
	if s == "" {
		return def
	}
	n, err := strconv.Atoi(s)
	if err != nil || n <= 0 {
		return def
	}
	if n > 16 {
		n = 16
	}
	return n
}

func main() {
	uafSeed = envSeed()
	rounds := tortureRounds(2)

	for round := 0; round < rounds; round++ {
		bus := NewBus[*rec]()
		nHandlers := 3 + int(uafSeed%3)
		var calls []int64
		var got []*rec

		ids := make([]int64, nHandlers)
		for i := 0; i < nHandlers; i++ {
			idx := i
			ids[i] = bus.Subscribe("orders", func(p *rec) {
				calls = append(calls, int64(idx))
				got = append(got, p)
			})
		}
		userID := bus.Subscribe("users", func(p *rec) {
			calls = append(calls, 100)
			got = append(got, p)
		})
		ck(userID > ids[nHandlers-1], "subscription ids not monotonic")
		ck(bus.Count("orders") == nHandlers && bus.Count("users") == 1, "subscriber count mismatch")

		nEvents := 120 + int(uafSeed%40)
		for e := 0; e < nEvents; e++ {
			p := &rec{
				id:   int64(round*10000 + e),
				name: fmt.Sprintf("e%d_%d", round, e),
				blob: make([]byte, 7+e%6),
			}
			p.blob[0] = byte(e)
			p.blob[len(p.blob)-1] = byte(round + 3)
			recSink = append(recSink, p)

			calls = calls[:0]
			got = got[:0]
			cnt := bus.Publish("orders", p)
			ck(cnt == nHandlers, fmt.Sprintf("round=%d event=%d order subscribers got=%d want=%d", round, e, cnt, nHandlers))
			ck(len(calls) == nHandlers, "dispatch call count mismatch")
			for i := 0; i < nHandlers && i < len(calls); i++ {
				ck(calls[i] == int64(i), "handlers not called in subscription order")
				ck(got[i] == p, "handler received wrong event pointer")
			}
			for _, g := range got {
				ck(g != nil && g.id == p.id && g.name == p.name, "event payload corrupted")
			}

			calls = calls[:0]
			got = got[:0]
			cnt = bus.Publish("users", p)
			ck(cnt == 1 && len(calls) == 1 && calls[0] == 100, "cross-topic delivery mismatch")
			ck(len(got) == 1 && got[0] == p, "users handler pointer mismatch")

			if e%32 == 31 {
				gcStress()
				ck(p.id == int64(round*10000+e), "event payload id corrupted by GC")
				ck(p.blob[0] == byte(e), "event payload blob corrupted by GC")
			}
		}

		// 退订偶数下标的订阅者，发布后只应命中剩余者。
		removed := 0
		for i := 0; i < nHandlers; i += 2 {
			ck(bus.Unsubscribe("orders", ids[i]), fmt.Sprintf("unsubscribe %d failed", i))
			removed++
		}
		ck(!bus.Unsubscribe("orders", 999999), "unsubscribe of unknown id succeeded")
		remaining := nHandlers - removed
		ck(bus.Count("orders") == remaining, "count after unsubscribe mismatch")

		p := &rec{id: 777, name: "after-unsub", blob: []byte{9, 8, 7}}
		recSink = append(recSink, p)
		calls = calls[:0]
		got = got[:0]
		ck(bus.Publish("orders", p) == remaining, "publish count after unsubscribe mismatch")
		ck(len(calls) == remaining, "unsubscribe did not take effect")
		j := 0
		for i := 0; i < nHandlers; i++ {
			if i%2 == 0 {
				continue
			}
			ck(j < len(calls) && calls[j] == int64(i), "remaining handler order corrupted")
			j++
		}
		ck(j == len(calls), "remaining handler count corrupted")
		gcStress()
		for _, g := range got {
			ck(g == p, "post-GC handler payload pointer changed")
		}
	}

	// 派发快照语义：首个 handler 在派发中退订另一个 handler，本轮仍应全部执行。
	sb := NewBus[*rec]()
	var secondID int64
	var firstCalls, secondCalls int
	firstID := sb.Subscribe("x", func(p *rec) {
		firstCalls++
		sb.Unsubscribe("x", secondID)
	})
	secondID = sb.Subscribe("x", func(p *rec) { secondCalls++ })
	p := &rec{id: 1, name: "snap"}
	recSink = append(recSink, p)
	ck(sb.Publish("x", p) == 2, "snapshot publish must still call the handler unsubscribed mid-dispatch")
	ck(firstCalls == 1 && secondCalls == 1, "snapshot dispatch semantics corrupted")
	_ = firstID
	ck(sb.Count("x") == 1, "count after self-unsubscribe corrupted")
	ck(sb.Publish("x", p) == 1 && secondCalls == 1, "unsubscribed handler called on next publish")

	// 第二次实例化 Bus[int64]：值型事件。
	ib := NewBus[int64]()
	var sum int64
	var seen int
	ib.Subscribe("n", func(v int64) { seen++; sum += v })
	ib.Subscribe("n", func(v int64) { seen++; sum += v * 2 })
	for i := int64(0); i < 20; i++ {
		ck(ib.Publish("n", i) == 2, "int64 bus publish count mismatch")
	}
	ck(seen == 40, "int64 bus handler calls corrupted")
	var want int64
	for i := int64(0); i < 20; i++ {
		want += i + i*2
	}
	ck(sum == want, "int64 bus payload accumulation corrupted")
	gcStress()

	finish("l01_generic_event_bus")
}
