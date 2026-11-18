// L07: atomic 指针环形队列的回绕边界。固定容量槽位数组，单生产者/单消费者通过
// atomic.Pointer[payload] 发布与清空槽位，覆盖满拒绝、回绕复用、消费即清槽与 GC。
package main

import (
	"fmt"
	"os"
	"runtime"
	"strconv"
	"sync/atomic"
)

var (
	failed int32
	gcSink [][]byte
	seed   int64
)

func ck(ok bool, msg string) {
	if !ok {
		atomic.AddInt32(&failed, 1)
		fmt.Printf("FAIL: %s (seed=%d)\n", msg, seed)
	}
}

func gcStress() {
	for r := 0; r < 2; r++ {
		for i := 0; i < 12; i++ {
			b := make([]byte, 6144+i*31)
			b[0] = byte(i)
			b[len(b)-1] = byte(225 - i)
			gcSink = append(gcSink[:0], b)
		}
		gcSink = gcSink[:0]
		runtime.GC()
	}
}

func finish(name string) {
	if n := atomic.LoadInt32(&failed); n != 0 {
		fmt.Printf("FAILED %s (%d failed checks)\n", name, n)
		os.Exit(1)
	}
	fmt.Println("ALL PASS " + name)
}

func tortureRounds(def int) int {
	if s := os.Getenv("TORTURE_ROUNDS"); s != "" {
		if v, err := strconv.Atoi(s); err == nil && v > 0 {
			if v > 16 {
				v = 16
			}
			return v
		}
	}
	return def
}

type aqLeaf struct {
	v   int64
	tag string
}

type aqPayload struct {
	seq  int64
	tag  string
	data []byte
	leaf *aqLeaf
}

type aqRing struct {
	slots []atomic.Pointer[aqPayload]
	head  atomic.Int64
	tail  atomic.Int64
}

func newAQRing(c int) *aqRing {
	return &aqRing{slots: make([]atomic.Pointer[aqPayload], c)}
}

func (q *aqRing) tryPush(p *aqPayload) bool {
	for {
		h := q.head.Load()
		t := q.tail.Load()
		if t-h >= int64(len(q.slots)) {
			return false
		}
		idx := int(t % int64(len(q.slots)))
		if q.slots[idx].CompareAndSwap(nil, p) {
			q.tail.Add(1)
			return true
		}
		runtime.Gosched()
	}
}

func (q *aqRing) pop() *aqPayload {
	h := q.head.Load()
	if h >= q.tail.Load() {
		return nil
	}
	idx := int(h % int64(len(q.slots)))
	p := q.slots[idx].Load()
	q.slots[idx].Store(nil)
	q.head.Add(1)
	return p
}

func (q *aqRing) allSlotsNil(stage string) {
	for i := range q.slots {
		ck(q.slots[i].Load() == nil, stage+": slot not cleared i="+strconv.Itoa(i))
	}
}

func newAQPayload(seq int64, s int64) *aqPayload {
	n := 5 + int(seq%4)
	data := make([]byte, n)
	for k := range data {
		data[k] = byte((seq*37 + int64(k*11) + s) & 0xff)
	}
	return &aqPayload{
		seq:  seq,
		tag:  "q-" + strconv.FormatInt(seq, 10),
		data: data,
		leaf: &aqLeaf{v: seq*23 - s, tag: "ql-" + strconv.FormatInt(seq, 10)},
	}
}

func checkAQPayload(p *aqPayload, seq int64, s int64, stage string) {
	ck(p != nil, stage+": nil payload seq="+strconv.FormatInt(seq, 10))
	if p == nil {
		return
	}
	ck(p.seq == seq, stage+": seq got="+strconv.FormatInt(p.seq, 10))
	ck(p.tag == "q-"+strconv.FormatInt(seq, 10), stage+": tag seq="+strconv.FormatInt(seq, 10))
	if p.leaf == nil {
		ck(false, stage+": nil leaf seq="+strconv.FormatInt(seq, 10))
	} else {
		ck(p.leaf.v == seq*23-s && p.leaf.tag == "ql-"+strconv.FormatInt(seq, 10), stage+": leaf seq="+strconv.FormatInt(seq, 10))
	}
	ck(len(p.data) == 5+int(seq%4), stage+": data len seq="+strconv.FormatInt(seq, 10))
	for k := range p.data {
		ck(p.data[k] == byte((seq*37+int64(k*11)+s)&0xff), stage+": data seq="+strconv.FormatInt(seq, 10)+" k="+strconv.Itoa(k))
	}
}

func main() {
	if env := os.Getenv("UAF_SEED"); env != "" {
		if v, err := strconv.ParseInt(env, 10, 64); err == nil {
			seed = v
		}
	}
	if seed < 0 {
		seed = -seed
	}

	capa := 7 + int(seed%5)
	q := newAQRing(capa)
	model := make([]int64, 0, capa)
	seq := int64(0)
	push := func(stage string) {
		ck(len(model) < capa, stage+": model overflow")
		ck(q.tryPush(newAQPayload(seq, seed)), stage+": push seq="+strconv.FormatInt(seq, 10))
		model = append(model, seq)
		seq++
	}
	pop := func(stage string) {
		ck(len(model) > 0, stage+": model underflow")
		p := q.pop()
		ck(p != nil, stage+": pop nil")
		if p == nil {
			return
		}
		checkAQPayload(p, model[0], seed, stage)
		model = model[1:]
	}

	// 1) 填满、满拒绝、GC 后按 FIFO 排空；槽位必须随消费清空。
	for i := 0; i < capa; i++ {
		push("fill")
	}
	ck(!q.tryPush(newAQPayload(-1, seed)), "full ring rejects push")
	gcStress()
	for i := 0; i < capa; i++ {
		pop("fill drain")
	}
	ck(q.pop() == nil, "empty after drain")
	q.allSlotsNil("fill drain")

	// 2) 回绕交错：始终保持 len<cap，跨多圈覆盖槽位复用。
	for i := 0; i < capa*4; i++ {
		if len(model) == capa {
			pop("wrap full")
		}
		push("wrap push")
		if i%3 == 2 && len(model) > 0 {
			pop("wrap pop")
		}
	}
	for len(model) > 0 {
		pop("wrap drain")
	}
	ck(q.pop() == nil, "empty after wrap")
	gcStress()
	q.allSlotsNil("wrap drain")

	// 3) 并发 SPSC：生产者持续 push，消费者 FIFO 校验并在途中 GC。
	rounds := tortureRounds(2)
	total := int64(capa*(24+8*rounds)) + seed%int64(capa)
	baseCount := q.head.Load()
	done := make(chan struct{})
	go func() {
		for i := int64(0); i < total; i++ {
			p := newAQPayload(i, seed)
			for !q.tryPush(p) {
				runtime.Gosched()
			}
		}
		close(done)
	}()
	var got int64
	var spins int64
	for got < total {
		p := q.pop()
		if p == nil {
			select {
			case <-done:
				ck(false, "queue lost payload before producer done")
			default:
			}
			spins++
			if spins > int64(total)*1000+100000 {
				ck(false, "consumer stalled")
				break
			}
			runtime.Gosched()
			continue
		}
		checkAQPayload(p, got, seed, "concurrent")
		got++
		if got%128 == 0 {
			runtime.GC()
		}
	}
	ck(got == total, "concurrent received all got="+strconv.FormatInt(got, 10))
	ck(q.pop() == nil, "empty after concurrent")
	gcStress()
	q.allSlotsNil("concurrent drain")
	ck(q.head.Load() == baseCount+total && q.tail.Load() == baseCount+total, "head/tail counters")

	finish("l07_atomic_queue_ring_wrap")
}
