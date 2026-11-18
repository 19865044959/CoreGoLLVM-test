// L19: net.Pipe 上的应用层半关闭。发送端写完 msgCount 条数据帧后补一个 FIN
// 帧，从此不再写但继续读取（写方向关闭、读方向仍开放）；接收端读到 FIN 后仍
// 可把汇总 ACK 写回，随后发送端 Close，接收端必须读到 io.EOF，且再写回得到
// io.ErrClosedPipe。全流程穿插 GC 并校验帧解码出的指针链。
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

type message struct {
	id   int
	base int64
	head *node
}

var failed int32

const (
	frameLen  = 40
	wireMagic = 0x4c313948 // "H91L" little-endian
	kindData  = 1
	kindFin   = 2
	kindAck   = 3
	msgCount  = 64
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

func baseOf(id int, seed int64) int64 {
	return int64(id)*89 + seed*41
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

func messageOK(m *message, id int, seed int64) bool {
	if m == nil || m.id != id || m.base != baseOf(id, seed) {
		return false
	}
	return chainOK(m.head, m.base)
}

func putFrame(b []byte, kind, id int, base, aux int64) {
	binary.LittleEndian.PutUint32(b[0:4], wireMagic)
	binary.LittleEndian.PutUint32(b[4:8], uint32(kind))
	binary.LittleEndian.PutUint32(b[8:12], uint32(id))
	binary.LittleEndian.PutUint64(b[12:20], uint64(base))
	binary.LittleEndian.PutUint64(b[20:28], uint64(aux))
}

func getFrame(b []byte) (kind, id int, base, aux int64, err error) {
	if binary.LittleEndian.Uint32(b[0:4]) != wireMagic {
		return 0, 0, 0, 0, errors.New("bad magic")
	}
	kind = int(binary.LittleEndian.Uint32(b[4:8]))
	id = int(binary.LittleEndian.Uint32(b[8:12]))
	base = int64(binary.LittleEndian.Uint64(b[12:20]))
	aux = int64(binary.LittleEndian.Uint64(b[20:28]))
	return kind, id, base, aux, nil
}

// sender：写完全部数据帧与 FIN 后只读不写（半关闭），收到 ACK 后 Close。
func sender(c net.Conn, seed int64, errCh chan<- error) {
	defer c.Close()
	buf := make([]byte, frameLen)
	var wantSum int64
	for i := 0; i < msgCount; i++ {
		base := baseOf(i, seed)
		putFrame(buf, kindData, i, base, 0)
		if _, err := c.Write(buf); err != nil {
			errCh <- fmt.Errorf("sender write %d: %w", i, err)
			return
		}
		wantSum += base*3 + 3
	}
	putFrame(buf, kindFin, msgCount, 0, 0)
	if _, err := c.Write(buf); err != nil {
		errCh <- fmt.Errorf("sender write fin: %w", err)
		return
	}
	// 写方向已逻辑关闭，读方向继续：必须收到接收端的 ACK。
	if _, err := io.ReadFull(c, buf); err != nil {
		errCh <- fmt.Errorf("sender read ack: %w", err)
		return
	}
	kind, count, _, sum, err := getFrame(buf)
	if err != nil || kind != kindAck || count != msgCount || sum != wantSum {
		errCh <- fmt.Errorf("sender ack mismatch kind=%d count=%d sum=%d want=%d err=%v",
			kind, count, sum, wantSum, err)
		return
	}
	errCh <- nil
}

// receiver：读到 FIN 后继续写 ACK（半关闭的另一半），随后等 EOF 并验证再写失败。
func receiver(c net.Conn, seed int64, errCh chan<- error) {
	defer c.Close()
	buf := make([]byte, frameLen)
	held := make([]*message, 0, msgCount)
	var sum int64
	for {
		if _, err := io.ReadFull(c, buf); err != nil {
			errCh <- fmt.Errorf("receiver read: %w", err)
			return
		}
		kind, id, base, _, err := getFrame(buf)
		if err != nil {
			errCh <- fmt.Errorf("receiver decode: %w", err)
			return
		}
		switch kind {
		case kindData:
			if id != len(held) || base != baseOf(id, seed) {
				errCh <- fmt.Errorf("receiver frame id=%d base=%d out of order", id, base)
				return
			}
			m := &message{id: id, base: base, head: makeChain(base)}
			held = append(held, m)
			sum += base*3 + 3
			if id%16 == 15 {
				gcStress()
				for k := len(held) - 16; k < len(held); k++ {
					if !messageOK(held[k], held[k].id, seed) {
						errCh <- fmt.Errorf("receiver payload %d corrupted after GC", held[k].id)
						return
					}
				}
			}
		case kindFin:
			if len(held) != msgCount || id != msgCount {
				errCh <- fmt.Errorf("receiver fin before all frames: got=%d", len(held))
				return
			}
			putFrame(buf, kindAck, len(held), 0, sum)
			if _, err := c.Write(buf); err != nil {
				errCh <- fmt.Errorf("receiver write ack after half-close: %w", err)
				return
			}
			// 对端 Close 后读方向结束：必须得到 io.EOF 而非超时。
			var one [1]byte
			n, rerr := c.Read(one[:])
			if n != 0 {
				errCh <- fmt.Errorf("receiver read after fin returned %d bytes", n)
				return
			}
			if !errors.Is(rerr, io.EOF) {
				errCh <- fmt.Errorf("receiver want EOF got %v", rerr)
				return
			}
			// 对端已关闭：本端写必须立即失败，不得静默成功。
			if _, werr := c.Write(one[:]); werr == nil {
				errCh <- errors.New("receiver write after peer close succeeded")
				return
			}
			errCh <- nil
			return
		default:
			errCh <- fmt.Errorf("receiver unknown kind %d", kind)
			return
		}
	}
}

func main() {
	seed := envSeed()
	c1, c2 := net.Pipe()
	errCh := make(chan error, 2)
	go sender(c1, seed, errCh)
	go receiver(c2, seed, errCh)

	for i := 0; i < 2; i++ {
		select {
		case err := <-errCh:
			ckf(err == nil, "pipe endpoint %d: %v", i, err)
		case <-time.After(waitLimit):
			ckf(false, "pipe endpoint %d did not finish", i)
		}
	}

	// 半关闭后的本地资源释放必须幂等。
	e1 := c1.Close()
	ck(e1 == nil || errors.Is(e1, io.ErrClosedPipe), "sender Close after half-close")
	e2 := c2.Close()
	ck(e2 == nil || errors.Is(e2, io.ErrClosedPipe), "receiver Close after half-close")
	gcStress()

	// 对照：发送端连 FIN 也不发就 Close，接收端必须直接 EOF（无数据、无半帧）。
	a, b := net.Pipe()
	ready := make(chan struct{})
	readErr := make(chan error, 1)
	go func() {
		ready <- struct{}{}
		var one [1]byte
		_, err := b.Read(one[:])
		readErr <- err
	}()
	<-ready
	gcStress()
	a.Close()
	select {
	case err := <-readErr:
		ckf(errors.Is(err, io.EOF), "bare close: want EOF got %v", err)
	case <-time.After(waitLimit):
		ck(false, "bare close did not unblock receiver")
	}
	b.Close()

	fmt.Printf("frames=%d fin=1 ack=1\n", msgCount)
	finish("l19_netpipe_halfclose")
}
