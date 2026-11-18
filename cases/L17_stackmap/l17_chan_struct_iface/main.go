// L17: buffered channel 按值传递含 any/func/map 指针的 struct；发送侧在
// noinline helper 中 GC 后入队，消费侧 range + GC 逐条校验副本，发送方随后
// 改写本地副本不得影响接收副本。
package main

import (
	"fmt"
	"os"
	"runtime"
	"strconv"
	"sync"
	"sync/atomic"
)

type item struct {
	v    int64
	name string
	next *item
}

type inner struct {
	p *item
	s string
}

type envelope struct {
	id   int64
	head *item
	list []*item
	tag  string
	val  any
	fn   func() *item
	m    map[string]*item
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

func finish(name string) {
	if n := atomic.LoadInt32(&failed); n != 0 {
		fmt.Printf("FAILED %s (%d failed checks) seed=%d\n", name, n, uafSeed)
		os.Exit(1)
	}
	fmt.Println("ALL PASS " + name)
}

func makeEnvelope(id int64, base int64) envelope {
	row := base + id*10
	k := &item{v: row + 6, name: "env-map"}
	e := envelope{
		id:   id,
		head: &item{v: row + 1, name: "env-head", next: &item{v: row + 2, name: "env-tail"}},
		list: []*item{
			{v: row + 3, name: "env-list"},
			{v: row + 4, name: "env-list"},
			{v: row + 5, name: "env-list"},
		},
		tag: "env",
		m: map[string]*item{
			"k": k,
			"z": {v: row + 7, name: "env-map2"},
		},
		fn: func() *item { return k },
	}
	if id%2 == 0 {
		e.val = &item{v: row + 8, name: "env-any-ptr"}
	} else {
		e.val = inner{p: &item{v: row + 9, name: "env-any-val"}, s: "inner"}
	}
	return e
}

func verifyEnvelope(e envelope, base int64, where string) {
	row := base + e.id*10
	bad := e.head == nil || e.head.v != row+1 || e.head.name != "env-head" ||
		e.head.next == nil || e.head.next.v != row+2 ||
		len(e.list) != 3 || e.tag != "env" || e.fn == nil || e.m == nil
	if !bad {
		for i, it := range e.list {
			if it == nil || it.v != row+3+int64(i) || it.name != "env-list" {
				bad = true
				break
			}
		}
	}
	if !bad {
		if k := e.fn(); k == nil || k.v != row+6 || k.name != "env-map" {
			bad = true
		}
		if e.m["k"] == nil || e.m["k"].v != row+6 {
			bad = true
		}
		if e.m["z"] == nil || e.m["z"].v != row+7 || e.m["z"].name != "env-map2" {
			bad = true
		}
	}
	if !bad {
		switch x := e.val.(type) {
		case *item:
			if x == nil || x.v != row+8 || x.name != "env-any-ptr" {
				bad = true
			}
		case inner:
			if x.p == nil || x.p.v != row+9 || x.s != "inner" {
				bad = true
			}
		default:
			bad = true
		}
	}
	ck(!bad, fmt.Sprintf("%s: envelope %d corrupted after GC seed=%d", where, e.id, uafSeed))
}

// deliver 在 noinline 栈帧里持有一份 envelope 副本，send 前后各 GC 一次。
//
//go:noinline
func deliver(ch chan<- envelope, e envelope, want int64) {
	defer func() {
		gcStress()
		ck(e.head != nil && e.head.v == want && len(e.list) == 3 && e.tag == "env" && e.fn != nil,
			fmt.Sprintf("deliver defer: envelope copy corrupted seed=%d", uafSeed))
	}()
	gcStress()
	ch <- e
	gcStress()
}

func main() {
	uafSeed = seedFromEnv()

	const rounds = 3
	total := 10 + int(uafSeed%5)
	base := int64(uafSeed%13) * 10

	for rnd := 0; rnd < rounds; rnd++ {
		ch := make(chan envelope, 4)
		var wg sync.WaitGroup
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < total; i++ {
				id := int64(i)
				e := makeEnvelope(id, base+int64(rnd)*1000)
				deliver(ch, e, base+int64(rnd)*1000+id*10+1)
				// 发送后改写本地副本的指针字段；接收方副本必须不受影响。
				e.head = nil
				e.list = nil
				e.fn = nil
				e.m = nil
				e.val = "mutated"
				e.tag = "mutated"
				if e.id != id {
					ck(false, fmt.Sprintf("round %d: sender id corrupted after send seed=%d", rnd, uafSeed))
				}
			}
			close(ch)
		}()

		got := 0
		for e := range ch {
			gcStress()
			verifyEnvelope(e, base+int64(rnd)*1000, "recv")
			got++
		}
		wg.Wait()
		ck(got == total, fmt.Sprintf("round %d: received %d/%d seed=%d", rnd, got, total, uafSeed))

		// 关闭后再做一次 GC，接收副本已出作用域，确认无残留引用崩溃。
		gcStress()
	}

	finish("l17_chan_struct_iface")
}
