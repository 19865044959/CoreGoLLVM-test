// L19: 多个 goroutine 分别阻塞在 net.Pipe/os.Pipe 的 Read 上；主协程先 GC
// 再写入含指针 payload 的帧唤醒它们，随后再 GC 并校验数据一条不丢。
// TORTURE_ROUNDS 控制轮数，默认 1 轮。
package main

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"runtime"
	"strconv"
	"sync"
	"sync/atomic"
	"time"
)

type node struct {
	v    int64
	next *node
}

type message struct {
	id   int
	base int64
	name string
	head *node
}

var failed int32

const (
	frameLen  = 48
	wireMagic = 0x4c313942 // "B91L" little-endian
	pairs     = 8
	netPairs  = 4
	waitLimit = 10 * time.Second
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

func envInt(name string, def, lo, hi int) int {
	s := os.Getenv(name)
	if s == "" {
		return def
	}
	n, err := strconv.Atoi(s)
	if err != nil || n < lo {
		return def
	}
	if n > hi {
		n = hi
	}
	return n
}

func msgBase(id int, seed int64) int64 {
	return int64(id)*59 + seed*31
}

func msgName(id int) string {
	return fmt.Sprintf("block-%02d", id)
}

func putFrame(b []byte, id int, seed int64) {
	base := msgBase(id, seed)
	binary.LittleEndian.PutUint32(b[0:4], wireMagic)
	binary.LittleEndian.PutUint32(b[4:8], uint32(id))
	binary.LittleEndian.PutUint64(b[8:16], uint64(base))
	copy(b[16:32], msgName(id))
}

func getFrame(b []byte) (*message, error) {
	if binary.LittleEndian.Uint32(b[0:4]) != wireMagic {
		return nil, errors.New("bad magic")
	}
	id := int(binary.LittleEndian.Uint32(b[4:8]))
	base := int64(binary.LittleEndian.Uint64(b[8:16]))
	name := string(bytes.TrimRight(b[16:32], "\x00"))
	return &message{
		id:   id,
		base: base,
		name: name,
		head: &node{v: base, next: &node{v: base + 1, next: &node{v: base + 2}}},
	}, nil
}

func messageOK(m *message, id int, seed int64) bool {
	if m == nil || m.id != id || m.name != msgName(id) || m.base != msgBase(id, seed) {
		return false
	}
	base := msgBase(id, seed)
	h := m.head
	if h == nil || h.v != base || h.next == nil || h.next.v != base+1 {
		return false
	}
	return h.next.next != nil && h.next.next.v == base+2 && h.next.next.next == nil
}

type pipePair struct {
	rd      io.Reader
	wr      io.Writer
	closeFn func()
}

type recvMsg struct {
	idx int
	m   *message
}

func makePairs(round int) ([]pipePair, bool) {
	ps := make([]pipePair, pairs)
	for i := 0; i < pairs; i++ {
		if i < netPairs {
			a, b := net.Pipe()
			ps[i] = pipePair{rd: b, wr: a, closeFn: func() { a.Close(); b.Close() }}
			continue
		}
		r, w, err := os.Pipe()
		if err != nil {
			ckf(false, "round %d os.Pipe %d: %v", round, i, err)
			return nil, false
		}
		ps[i] = pipePair{rd: r, wr: w, closeFn: func() { r.Close(); w.Close() }}
	}
	return ps, true
}

func runRound(round int, seed int64) {
	ps, ok := makePairs(round)
	if !ok {
		return
	}
	defer func() {
		for i := range ps {
			ps[i].closeFn()
		}
	}()

	recvCh := make(chan recvMsg, pairs)
	readyCh := make(chan int, pairs)
	errCh := make(chan error, pairs*4)
	release := make([]chan struct{}, pairs)

	var readers sync.WaitGroup
	for i := 0; i < pairs; i++ {
		release[i] = make(chan struct{})
		readers.Add(1)
		go func(idx int, rd io.Reader, rel <-chan struct{}) {
			defer readers.Done()
			// 通知主协程本 goroutine 即将进入阻塞 Read。
			readyCh <- idx
			buf := make([]byte, frameLen)
			if _, err := io.ReadFull(rd, buf); err != nil {
				errCh <- fmt.Errorf("round %d reader %d: %w", round, idx, err)
				return
			}
			m, err := getFrame(buf)
			if err != nil {
				errCh <- fmt.Errorf("round %d decode %d: %w", round, idx, err)
				return
			}
			recvCh <- recvMsg{idx: idx, m: m}
			<-rel
			if !messageOK(m, m.id, seed) {
				errCh <- fmt.Errorf("round %d reader %d payload corrupted after GC", round, idx)
			}
		}(i, ps[i].rd, release[i])
	}

	// 等所有 reader 就位后先 GC：此时大部分 goroutine 阻塞在 Read 上。
	for i := 0; i < pairs; i++ {
		select {
		case <-readyCh:
		case <-time.After(waitLimit):
			ckf(false, "round %d: reader %d did not become ready", round, i)
			return
		}
	}
	gcStress()

	// 主协程并发写入，唤醒所有阻塞读。
	var writers sync.WaitGroup
	for i := 0; i < pairs; i++ {
		writers.Add(1)
		go func(idx int) {
			defer writers.Done()
			buf := make([]byte, frameLen)
			putFrame(buf, idx, seed)
			if _, err := ps[idx].wr.Write(buf); err != nil {
				errCh <- fmt.Errorf("round %d writer %d: %w", round, idx, err)
			}
		}(i)
	}

	got := 0
	released := make([]bool, pairs)
	for got < pairs {
		select {
		case rm := <-recvCh:
			got++
			if rm.idx < 0 || rm.idx >= pairs {
				ckf(false, "round %d: reader index %d out of range", round, rm.idx)
				continue
			}
			// 数据已在 reader 栈/主协程上，GC 后校验指针 payload。
			gcStress()
			ckf(messageOK(rm.m, rm.idx, seed), "round %d reader %d payload corrupted after GC", round, rm.idx)
			release[rm.idx] <- struct{}{}
			released[rm.idx] = true
		case <-time.After(waitLimit):
			ckf(false, "round %d: received %d/%d frames", round, got, pairs)
			got = pairs
		}
	}
	for i := 0; i < pairs; i++ {
		if !released[i] {
			close(release[i])
		}
	}

	writersDone := make(chan struct{})
	go func() {
		writers.Wait()
		close(writersDone)
	}()
	select {
	case <-writersDone:
	case <-time.After(waitLimit):
		ckf(false, "round %d: writers did not finish", round)
	}
	readersDone := make(chan struct{})
	go func() {
		readers.Wait()
		close(readersDone)
	}()
	select {
	case <-readersDone:
	case <-time.After(waitLimit):
		ckf(false, "round %d: readers did not finish", round)
	}

	for {
		select {
		case err := <-errCh:
			ckf(false, "%v", err)
		default:
			goto drained
		}
	}
drained:
}

func main() {
	seed := envSeed()
	rounds := envInt("TORTURE_ROUNDS", 1, 1, 20)
	fmt.Printf("pairs=%d rounds=%d\n", pairs, rounds)
	for r := 0; r < rounds; r++ {
		runRound(r, seed+int64(r)*97)
	}
	finish("l19_goroutine_block_io")
}
