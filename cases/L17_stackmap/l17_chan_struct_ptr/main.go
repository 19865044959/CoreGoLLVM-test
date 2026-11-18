// L17: channel 传递含指针 struct 值。发送/接收两侧各持一份副本，GC 扫描 chan 缓冲与栈副本。
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

type packet struct {
	id    int64
	head  *item
	list  []*item
	arr   [2]*item
	label string
	data  []byte
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

func mkPacket(i int64, n int) packet {
	p := packet{
		id:    i,
		head:  &item{v: i*10 + 1, name: "head", next: &item{v: i*10 + 2, name: "tail"}},
		list:  make([]*item, 0, n),
		label: "packet",
		data:  make([]byte, 24+n),
	}
	for k := 0; k < n; k++ {
		p.list = append(p.list, &item{v: i*10 + 3 + int64(k), name: "list"})
	}
	p.arr[0] = &item{v: i*10 + 90, name: "arr0"}
	p.arr[1] = &item{v: i*10 + 91, name: "arr1"}
	for k := range p.data {
		p.data[k] = byte((int(i) + k) % 251)
	}
	return p
}

func verifyPacket(p packet, i int64, n int, where string) {
	bad := p.id != i || p.label != "packet" ||
		p.head == nil || p.head.v != i*10+1 || p.head.next == nil || p.head.next.v != i*10+2 ||
		len(p.list) != n ||
		p.arr[0] == nil || p.arr[0].v != i*10+90 ||
		p.arr[1] == nil || p.arr[1].v != i*10+91 ||
		len(p.data) != 24+n
	if !bad {
		for k := 0; k < n; k++ {
			if p.list[k] == nil || p.list[k].v != i*10+3+int64(k) || p.list[k].name != "list" {
				bad = true
				break
			}
		}
	}
	if !bad {
		for k := range p.data {
			if p.data[k] != byte((int(i)+k)%251) {
				bad = true
				break
			}
		}
	}
	ck(!bad, fmt.Sprintf("%s: packet %d corrupted after GC seed=%d", where, i, uafSeed))
}

//go:noinline
func sendLoop(ch chan<- packet, ack <-chan struct{}, rounds, n int) {
	for i := 0; i < rounds; i++ {
		p := mkPacket(int64(i), n)
		gcStress()
		ch <- p
		gcStress()
		verifyPacket(p, int64(i), n, "sender-after-send")
		<-ack
		gcStress()
		verifyPacket(p, int64(i), n, "sender-after-ack")
	}
}

func main() {
	uafSeed = seedFromEnv()
	const rounds = 16
	n := 3 + int(uafSeed%4)

	ch := make(chan packet, 4)
	ack := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		sendLoop(ch, ack, rounds, n)
	}()

	for i := 0; i < rounds; i++ {
		p := <-ch
		gcStress()
		verifyPacket(p, int64(i), n, "recv-after-recv")
		ack <- struct{}{}
		gcStress()
		verifyPacket(p, int64(i), n, "recv-after-ack")
	}
	wg.Wait()

	// 同一 packet 值经 interface channel 箱装后再跨 GC 解箱。
	ifaceCh := make(chan any, 2)
	boxed := mkPacket(100, n)
	ifaceCh <- boxed
	gcStress()
	unboxed, ok := (<-ifaceCh).(packet)
	ck(ok, fmt.Sprintf("iface channel: type assertion failed seed=%d", uafSeed))
	if ok {
		verifyPacket(unboxed, 100, n, "iface-unboxed")
	}
	gcStress()
	verifyPacket(boxed, 100, n, "iface-original")

	finish("l17_chan_struct_ptr")
}
