// L19: net.Pipe 多跳链路。writer -> 4 段 net.Pipe -> 3 个 relay -> reader，
// 96 个定长帧逐跳校验。reader 读到首帧后阻塞在 gate 上，使链路饱和、帧停留
// 在各 relay 栈上，此时主协程显式 GC，再放行并逐帧校验指针 payload。
package main

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"runtime"
	"strconv"
	"sync/atomic"
	"time"
)

type node struct {
	v    int64
	next *node
}

var failed int32

const (
	hopFrameSize = 48
	waitLimit    = 5 * time.Second
)

func ck(ok bool, msg string) {
	if !ok {
		atomic.AddInt32(&failed, 1)
		fmt.Println("FAIL:", msg)
	}
}

func ckf(ok bool, format string, args ...any) {
	if !ok {
		atomic.AddInt32(&failed, 1)
		fmt.Printf("FAIL: "+format+"\n", args...)
	}
}

func gcStress() {
	bufs := make([][]byte, 0, 16)
	for i := 0; i < 16; i++ {
		b := make([]byte, 4096)
		b[len(b)-1] = byte(i)
		bufs = append(bufs, b)
	}
	runtime.GC()
	runtime.GC()
	runtime.KeepAlive(bufs)
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

func makeChain(base int64) *node {
	return &node{v: base, next: &node{v: base + 1, next: &node{v: base + 2}}}
}

func chainOK(head *node, base int64) bool {
	if head == nil || head.v != base || head.next == nil || head.next.v != base+1 {
		return false
	}
	return head.next.next != nil && head.next.next.v == base+2 && head.next.next.next == nil
}

func encodeHopFrame(dst []byte, id int, base int64) {
	for i := range dst {
		dst[i] = 0
	}
	binary.BigEndian.PutUint32(dst[0:4], uint32(id))
	binary.BigEndian.PutUint64(dst[4:12], uint64(base))
	binary.BigEndian.PutUint64(dst[12:20], uint64(base)*3+3)
	copy(dst[20:36], hopFrameTag)
}

const hopFrameTag = "hop-frame-abcdef"

func decodeHopFrame(src []byte) (id int, base int64, ok bool) {
	id = int(binary.BigEndian.Uint32(src[0:4]))
	base = int64(binary.BigEndian.Uint64(src[4:12]))
	if binary.BigEndian.Uint64(src[12:20]) != uint64(base)*3+3 {
		return 0, 0, false
	}
	if string(src[20:36]) != hopFrameTag {
		return 0, 0, false
	}
	for _, b := range src[36:] {
		if b != 0 {
			return 0, 0, false
		}
	}
	return id, base, true
}

type relayResult struct {
	hop   int
	seen  int
	bad   int
	chain bool
	err   error
}

func runRelay(hop int, src, dst net.Conn, chainBase int64) relayResult {
	state := makeChain(chainBase)
	buf := make([]byte, hopFrameSize)
	seen, bad := 0, 0
	for {
		if _, err := io.ReadFull(src, buf); err != nil {
			dst.Close()
			res := relayResult{hop: hop, seen: seen, bad: bad, chain: chainOK(state, chainBase)}
			if errors.Is(err, io.EOF) {
				return res
			}
			res.err = err
			return res
		}
		if _, _, ok := decodeHopFrame(buf); !ok {
			bad++
		}
		if _, err := dst.Write(buf); err != nil {
			return relayResult{hop: hop, seen: seen, bad: bad, chain: chainOK(state, chainBase), err: err}
		}
		seen++
		if seen%16 == 0 {
			gcStress()
			if !chainOK(state, chainBase) {
				bad++
			}
		}
	}
}

type readResult struct {
	got  int
	bad  int
	err  error
	msgs []*node
}

func runReader(r net.Conn, total int, base int64, first chan<- struct{}, release <-chan struct{}) readResult {
	buf := make([]byte, hopFrameSize)
	msgs := make([]*node, 0, total)
	bad := 0
	for i := 0; i < total; i++ {
		if _, err := io.ReadFull(r, buf); err != nil {
			return readResult{got: i, bad: bad, err: err, msgs: msgs}
		}
		id, b, ok := decodeHopFrame(buf)
		want := base + int64(i)*5
		if !ok || id != i || b != want {
			bad++
		}
		msgs = append(msgs, makeChain(b))
		if i == 0 {
			first <- struct{}{}
			<-release
		}
		if i%16 == 15 {
			gcStress()
		}
	}
	gcStress()
	for i, m := range msgs {
		if !chainOK(m, base+int64(i)*5) {
			bad++
		}
	}
	return readResult{got: len(msgs), bad: bad, msgs: msgs}
}

func main() {
	seed := envSeed()
	const hops = 3
	const total = 96
	base := seed*10000 + 7

	pipes := make([][2]net.Conn, hops+1)
	for i := range pipes {
		a, b := net.Pipe()
		pipes[i] = [2]net.Conn{a, b}
	}
	writer := pipes[0][0]
	reader := pipes[hops][1]

	relayRes := make(chan relayResult, hops)
	for i := 0; i < hops; i++ {
		src := pipes[i][1]
		dst := pipes[i+1][0]
		cb := base + 5000 + int64(i)*100
		go func(hop int, src, dst net.Conn, cb int64) {
			relayRes <- runRelay(hop, src, dst, cb)
		}(i, src, dst, cb)
	}

	writerErr := make(chan error, 1)
	go func() {
		buf := make([]byte, hopFrameSize)
		for i := 0; i < total; i++ {
			encodeHopFrame(buf, i, base+int64(i)*5)
			if _, err := writer.Write(buf); err != nil {
				writerErr <- err
				return
			}
			if i%24 == 23 {
				gcStress()
			}
		}
		writer.Close()
		writerErr <- nil
	}()

	first := make(chan struct{}, 1)
	release := make(chan struct{})
	readRes := make(chan readResult, 1)
	go func() {
		readRes <- runReader(reader, total, base, first, release)
	}()

	select {
	case <-first:
	case <-time.After(waitLimit):
		ck(false, "reader did not receive first frame")
	}
	// reader 阻塞在 release 上，链路中后续帧停留在 relay 缓冲区/栈上。
	gcStress()
	close(release)

	var rr readResult
	select {
	case rr = <-readRes:
	case <-time.After(waitLimit):
		ck(false, "reader did not finish")
	}
	select {
	case err := <-writerErr:
		ckf(err == nil, "writer error: %v", err)
	case <-time.After(waitLimit):
		ck(false, "writer did not finish")
	}
	for i := 0; i < hops; i++ {
		select {
		case hr := <-relayRes:
			ckf(hr.err == nil, "relay %d error: %v", hr.hop, hr.err)
			ckf(hr.seen == total, "relay %d seen=%d want=%d", hr.hop, hr.seen, total)
			ckf(hr.bad == 0, "relay %d bad frames=%d", hr.hop, hr.bad)
			ckf(hr.chain, "relay %d payload chain corrupted (seed=%d)", hr.hop, seed)
		case <-time.After(waitLimit):
			ck(false, "relay did not finish")
		}
	}
	ckf(rr.err == nil, "reader error: %v", rr.err)
	ckf(rr.got == total, "reader got=%d want=%d", rr.got, total)
	ckf(rr.bad == 0, "reader bad frames=%d (seed=%d)", rr.bad, seed)

	gcStress()
	badMsgs := 0
	for i, m := range rr.msgs {
		if !chainOK(m, base+int64(i)*5) {
			badMsgs++
		}
	}
	ckf(badMsgs == 0, "reader payload chains corrupted: bad=%d (seed=%d)", badMsgs, seed)

	for _, p := range pipes {
		p[0].Close()
		p[1].Close()
	}
	fmt.Printf("hops=%d pipes=%d frames=%d\n", hops, len(pipes), total)
	finish("l19_netpipe_chain")
}
