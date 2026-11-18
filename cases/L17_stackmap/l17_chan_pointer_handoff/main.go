// L17: 无缓冲 chan 交接指针；发送方交接后继续分配并 GC，双方在 GC 后校验对象。
package main

import (
	"fmt"
	"os"
	"runtime"
	"sync"
	"sync/atomic"
)

type item struct {
	v    int64
	name string
	next *item
}

type record struct {
	id    int64
	label string
	head  *item
	list  []*item
	blob  []byte
	strs  []string
}

const iters = 24

var failed int32

func ck(ok bool, msg string) {
	if !ok {
		atomic.AddInt32(&failed, 1)
		fmt.Println("FAIL:", msg)
	}
}

// gcStress 不共享全局槽，便于发送/接收两个 goroutine 并发调用。
func gcStress() {
	var sink [][]byte
	for i := 0; i < 8; i++ {
		b := make([]byte, 4096)
		b[0] = byte(i)
		sink = append(sink, b)
	}
	runtime.GC()
	runtime.GC()
	runtime.KeepAlive(sink)
}

func mkRecord(i int64) *record {
	r := &record{
		id:    i*16 + 7,
		label: "handoff",
		head:  &item{v: i*16 + 1, name: "head"},
		list: []*item{
			{v: i*16 + 2, name: "a"},
			{v: i*16 + 3, name: "b"},
			{v: i*16 + 4, name: "c"},
		},
		blob: make([]byte, 128),
		strs: []string{"alpha", "beta", "gamma"},
	}
	r.head.next = &item{v: i*16 + 5, name: "tail"}
	for j := range r.blob {
		r.blob[j] = byte((int(i) + j) % 251)
	}
	return r
}

func verify(r *record, i int64, where string) {
	if r == nil {
		ck(false, where+": nil record")
		return
	}
	bad := r.id != i*16+7 || r.label != "handoff" ||
		r.head == nil || r.head.v != i*16+1 || r.head.name != "head" ||
		r.head.next == nil || r.head.next.v != i*16+5 || r.head.next.name != "tail"
	if !bad {
		if len(r.list) != 3 {
			bad = true
		} else {
			for k, it := range r.list {
				if it == nil || it.v != i*16+2+int64(k) || it.name != string(rune('a'+k)) {
					bad = true
					break
				}
			}
		}
	}
	if !bad {
		if len(r.strs) != 3 || r.strs[0] != "alpha" || r.strs[1] != "beta" || r.strs[2] != "gamma" {
			bad = true
		}
	}
	if !bad {
		if len(r.blob) != 128 {
			bad = true
		} else {
			for j := range r.blob {
				if r.blob[j] != byte((int(i)+j)%251) {
					bad = true
					break
				}
			}
		}
	}
	ck(!bad, where+": record corrupted after GC")
}

// handoffSend 在无缓冲发送返回后仍持有 p，并在两个 GC 点后返回 p（返回后由调用方再次校验）。
//
//go:noinline
func handoffSend(ch chan<- *record, ack <-chan struct{}, i int64) *record {
	p := mkRecord(i)
	ch <- p    // 无缓冲交接：接收方拿到同一指针
	gcStress() // 发送方交接后继续分配/GC
	<-ack
	gcStress() // 接收方校验完成后再次 GC
	return p
}

//go:noinline
func handoffRecv(ch <-chan *record) *record {
	p := <-ch
	gcStress() // 接收方持有指针期间 GC
	return p
}

func main() {
	ch := make(chan *record) // 无缓冲
	ack := make(chan struct{})

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := int64(0); i < iters; i++ {
			p := handoffSend(ch, ack, i)
			verify(p, i, "sender")
			gcStress()
			verify(p, i, "sender-after-ack")
		}
	}()

	for i := int64(0); i < iters; i++ {
		p := handoffRecv(ch)
		ack <- struct{}{}
		verify(p, i, "receiver")
		gcStress()
		verify(p, i, "receiver-after-ack")
	}
	wg.Wait()

	finish := func() {
		if n := atomic.LoadInt32(&failed); n != 0 {
			fmt.Printf("FAILED l17_chan_pointer_handoff (%d failed checks)\n", n)
			os.Exit(1)
		}
		fmt.Println("ALL PASS l17_chan_pointer_handoff")
	}
	finish()
}
