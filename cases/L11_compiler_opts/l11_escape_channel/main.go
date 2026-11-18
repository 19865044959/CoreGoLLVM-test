// L11: 经 channel 逃逸——发送到缓冲/非缓冲 channel 的对象、接口装箱值、
// 闭包、map 与流水线节点必须在显式 GC 后仍然有效，接收顺序与内容不变。
package main

import (
	"fmt"
	"os"
	"runtime"
	"strconv"
	"sync"
	"sync/atomic"
)

var (
	failed int32
	gcSink [][]byte
	seed   uint64 = 1
)

func ck(ok bool, msg string) {
	if !ok {
		atomic.AddInt32(&failed, 1)
		fmt.Printf("FAIL: %s (UAF_SEED=%d)\n", msg, seed)
	}
}

func gcStress() {
	for i := 0; i < 16; i++ {
		b := make([]byte, 4096)
		b[0] = byte(i)
		b[len(b)-1] = byte(255 - i)
		gcSink = append(gcSink[:0], b)
	}
	gcSink = gcSink[:0]
	runtime.GC()
	runtime.GC()
}

func loadSeed() uint64 {
	s := os.Getenv("UAF_SEED")
	if s == "" {
		return 1
	}
	n, err := strconv.ParseUint(s, 10, 64)
	if err != nil {
		return 1
	}
	return n
}

func tortureMultiplier() int {
	s := os.Getenv("TORTURE_ROUNDS")
	if s == "" {
		return 1
	}
	n, err := strconv.Atoi(s)
	if err != nil || n <= 0 {
		return 1
	}
	if n > 50 {
		n = 50
	}
	return n
}

func finish(name string) {
	if n := atomic.LoadInt32(&failed); n != 0 {
		fmt.Printf("FAILED %s (%d failed checks, seed=%d)\n", name, n, seed)
		os.Exit(1)
	}
	fmt.Println("ALL PASS " + name)
}

type ecNode struct {
	id   int64
	tag  string
	data []byte
}

func makeECNode(id int64, salt byte, size int) *ecNode {
	d := make([]byte, size)
	for i := range d {
		d[i] = byte(id)*7 + salt + byte(i)
	}
	return &ecNode{id: id, tag: "node", data: d}
}

func ecNodeOK(n *ecNode, id int64, salt byte, size int) bool {
	if n == nil || n.id != id || n.tag != "node" || len(n.data) != size {
		return false
	}
	for i := range n.data {
		if n.data[i] != byte(id)*7+salt+byte(i) {
			return false
		}
	}
	return true
}

func bufferedPtrEscape(n int, salt byte, sizeBase int) []*ecNode {
	ch := make(chan *ecNode, n)
	for i := 0; i < n; i++ {
		ch <- makeECNode(int64(i), salt, sizeBase+i%5)
	}
	ck(len(ch) == n, "buffered channel length before GC mismatch")
	ck(cap(ch) == n, "buffered channel capacity mismatch")
	gcStress()
	out := make([]*ecNode, 0, n)
	for i := 0; i < n; i++ {
		v := <-ch
		ck(ecNodeOK(v, int64(i), salt, sizeBase+i%5), "buffered channel node corrupted after GC")
		out = append(out, v)
	}
	ck(len(ch) == 0, "buffered channel not drained")
	close(ch)
	return out
}

func interfaceChannel(n int, salt byte) (int, int) {
	ch := make(chan any, n)
	for i := 0; i < n; i++ {
		size := 8 + i%3
		if i%2 == 0 {
			ch <- makeECNode(int64(i), salt, size)
		} else {
			ch <- *makeECNode(int64(i), salt, size)
		}
	}
	gcStress()
	ptrs, vals := 0, 0
	for i := 0; i < n; i++ {
		switch v := (<-ch).(type) {
		case *ecNode:
			ck(ecNodeOK(v, int64(i), salt, 8+i%3), "interface channel pointer node corrupted after GC")
			ptrs++
		case ecNode:
			ck(v.id == int64(i) && v.tag == "node" && len(v.data) == 8+i%3, "interface channel value node corrupted after GC")
			vals++
		default:
			ck(false, "interface channel unexpected dynamic type")
		}
	}
	return ptrs, vals
}

func closureChannel(n int) {
	ch := make(chan func() int64, n)
	var want int64
	for i := 0; i < n; i++ {
		k := int64(i*3 + 1)
		ch <- func() int64 { return k*k + 7 }
		want += k*k + 7
	}
	gcStress()
	var got int64
	for i := 0; i < n; i++ {
		f := <-ch
		got += f()
	}
	ck(got == want, "closure channel sum mismatch after GC")
}

func mapChannel(rounds int) {
	ch := make(chan map[string]int64, rounds)
	for r := 0; r < rounds; r++ {
		ch <- map[string]int64{"r": int64(r)}
	}
	gcStress()
	for r := 0; r < rounds; r++ {
		m := <-ch
		ck(m["r"] == int64(r), "map through channel corrupted after GC")
		m["x"] = int64(r * 2)
		ck(m["x"] == int64(r*2), "map write after channel receive failed")
	}
}

func closedBuffered(n int, salt byte) {
	ch := make(chan *ecNode, n)
	for i := 0; i < n; i++ {
		ch <- makeECNode(int64(i+1000), salt, 4)
	}
	close(ch)
	gcStress()
	count := 0
	for v := range ch {
		ck(v.id == int64(count+1000), "closed buffered channel order mismatch")
		count++
	}
	ck(count == n, "closed buffered channel count mismatch")
}

func unbufferedProducer(n int, salt byte) []*ecNode {
	send := make(chan *ecNode)
	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < n; i++ {
			send <- makeECNode(int64(i+2000), salt, 6)
		}
		close(send)
	}()
	first := <-send
	gcStress()
	ck(first.id == 2000, "first unbuffered node corrupted by GC")
	got := []*ecNode{first}
	for v := range send {
		got = append(got, v)
	}
	<-done
	ck(len(got) == n, "unbuffered channel count mismatch")
	for i, v := range got {
		ck(v.id == int64(i+2000), "unbuffered channel order/ID mismatch")
	}
	return got
}

func pipeline(n int, salt byte) []*ecNode {
	in := make(chan *ecNode)
	mid := make(chan *ecNode)
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		defer close(in)
		for i := 0; i < n; i++ {
			in <- makeECNode(int64(i), salt, 9)
		}
	}()
	go func() {
		defer wg.Done()
		defer close(mid)
		for v := range in {
			v.id += 100
			v.data[0] ^= 0x5a
			mid <- v
		}
	}()
	out := make([]*ecNode, 0, n)
	for v := range mid {
		out = append(out, v)
		if len(out)%8 == 0 {
			runtime.GC()
		}
	}
	wg.Wait()
	ck(len(out) == n, "pipeline node count mismatch")
	for i, v := range out {
		ck(v.id == int64(i+100), "pipeline ID transform mismatch")
		want := (byte(int64(i))*7 + salt + byte(0)) ^ 0x5a
		ck(v.data[0] == want, "pipeline data transform mismatch")
	}
	return out
}

func selectReady(salt byte) {
	ready := make(chan *ecNode, 1)
	ready <- makeECNode(77, salt, 5)
	never := make(chan *ecNode)
	var got *ecNode
	select {
	case got = <-ready:
	case <-never:
	}
	gcStress()
	ck(got != nil && ecNodeOK(got, 77, salt, 5), "select channel receive corrupted after GC")
	close(never)
}

func main() {
	seed = loadSeed()
	rounds := 4 * tortureMultiplier()
	n := 48 + int(seed%16)
	sizeBase := 16 + int(seed%8)
	salt := byte(seed)
	var kept []*ecNode

	for r := 0; r < rounds; r++ {
		nodes := bufferedPtrEscape(n, salt+byte(r), sizeBase)
		kept = append(kept, nodes[0], nodes[n-1])

		ptrs, vals := interfaceChannel(n, salt+byte(r)*2)
		ck(ptrs == (n+1)/2, "interface channel pointer count mismatch")
		ck(vals == n/2, "interface channel value count mismatch")

		closureChannel(n)
		mapChannel(4)
		closedBuffered(n/2, salt)
		unb := unbufferedProducer(n/2, salt)
		kept = append(kept, unb[0])
		pl := pipeline(n/2, salt)
		kept = append(kept, pl[0], pl[len(pl)-1])
		selectReady(salt)
		gcStress()
	}

	gcStress()
	for _, v := range kept {
		ck(v != nil && v.tag == "node" && len(v.data) > 0, "retained channel node corrupted after final GC")
		if v != nil {
			ck(v.id >= 0, "retained channel node ID corrupted after final GC")
		}
	}

	finish("l11_escape_channel")
}
