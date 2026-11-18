// L19: UDP netpoll 的 deadline 与关闭唤醒 churn。每轮：读 deadline 已过期必须
// 立即返回 timeout；清空后完成 16 个定长数据报的收发；写 deadline 已过期必须
// 立即失败；随后一个 goroutine 阻塞在 ReadFromUDP 上，关闭 socket 必须把它唤醒
// 为 ErrClosed。每轮数据报都解码为指针消息并在显式 GC 后校验。
// TORTURE_ROUNDS 放大轮数，默认 2 轮。
package main

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
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
	name string
	head *node
}

var failed int32

const (
	frameLen  = 512
	wireMagic = 0x4c313955 // "U91L" little-endian
	perRound  = 16
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
	return int64(id)*97 + seed*53
}

func msgName(id int) string {
	return fmt.Sprintf("udp-%04d", id)
}

func pattern(id int) byte {
	return byte(id*13 + 7)
}

func putFrame(b []byte, id int, seed int64) {
	base := msgBase(id, seed)
	binary.LittleEndian.PutUint32(b[0:4], wireMagic)
	binary.LittleEndian.PutUint32(b[4:8], uint32(id))
	binary.LittleEndian.PutUint64(b[8:16], uint64(base))
	copy(b[16:32], msgName(id))
	binary.LittleEndian.PutUint64(b[32:40], uint64(base*13+int64(id)))
	for i := 40; i < frameLen; i++ {
		b[i] = pattern(id)
	}
}

func getFrame(b []byte) (*message, error) {
	if binary.LittleEndian.Uint32(b[0:4]) != wireMagic {
		return nil, errors.New("bad magic")
	}
	id := int(binary.LittleEndian.Uint32(b[4:8]))
	base := int64(binary.LittleEndian.Uint64(b[8:16]))
	if binary.LittleEndian.Uint64(b[32:40]) != uint64(base*13+int64(id)) {
		return nil, errors.New("bad checksum")
	}
	for i := 40; i < frameLen; i++ {
		if b[i] != pattern(id) {
			return nil, fmt.Errorf("bad pattern at %d", i)
		}
	}
	return &message{
		id:   id,
		base: base,
		name: string(bytes.TrimRight(b[16:32], "\x00")),
		head: &node{v: base, next: &node{v: base + 1, next: &node{v: base + 2}}},
	}, nil
}

func messageOK(m *message, id int, seed int64) bool {
	if m == nil || m.id != id || m.name != msgName(id) || m.base != msgBase(id, seed) {
		return false
	}
	b := msgBase(id, seed)
	h := m.head
	if h == nil || h.v != b || h.next == nil || h.next.v != b+1 {
		return false
	}
	return h.next.next != nil && h.next.next.v == b+2 && h.next.next.next == nil
}

func round(seed int64) {
	laddr := &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 0}
	pc, err := net.ListenUDP("udp", laddr)
	if err != nil {
		ckf(false, "listen udp: %v", err)
		return
	}
	client, err := net.DialUDP("udp", nil, pc.LocalAddr().(*net.UDPAddr))
	if err != nil {
		ckf(false, "dial udp: %v", err)
		pc.Close()
		return
	}

	// 1) 已过期读 deadline：立即返回 timeout。
	if err := pc.SetReadDeadline(time.Now().Add(-time.Second)); err != nil {
		ckf(false, "set past read deadline: %v", err)
	}
	buf := make([]byte, frameLen)
	n, _, rerr := pc.ReadFromUDP(buf)
	ckf(n == 0 && errors.Is(rerr, os.ErrDeadlineExceeded), "past read deadline: n=%d err=%v", n, rerr)
	if err := pc.SetReadDeadline(time.Time{}); err != nil {
		ckf(false, "clear read deadline: %v", err)
	}

	// 2) 已过期写 deadline：立即返回 timeout。
	if err := client.SetWriteDeadline(time.Now().Add(-time.Second)); err != nil {
		ckf(false, "set past write deadline: %v", err)
	}
	putFrame(buf, -1, seed)
	wn, werr := client.Write(buf)
	ckf(errors.Is(werr, os.ErrDeadlineExceeded), "past write deadline: n=%d err=%v", wn, werr)
	if err := client.SetWriteDeadline(time.Time{}); err != nil {
		ckf(false, "clear write deadline: %v", err)
	}
	gcStress()

	// 3) deadline 清理后正常收发。
	held := make([]*message, 0, perRound)
	for i := 0; i < perRound; i++ {
		putFrame(buf, i, seed)
		if _, err := client.Write(buf); err != nil {
			ckf(false, "client write %d: %v", i, err)
			break
		}
		rn, _, err := pc.ReadFromUDP(buf)
		if err != nil {
			ckf(false, "server read %d: %v", i, err)
			break
		}
		if rn != frameLen {
			ckf(false, "server read %d: n=%d want=%d", i, rn, frameLen)
			break
		}
		m, err := getFrame(buf)
		if err != nil {
			ckf(false, "server decode %d: %v", i, err)
			break
		}
		if !messageOK(m, i, seed) {
			ckf(false, "server payload corrupted at %d", i)
			break
		}
		held = append(held, m)
		if i%8 == 7 {
			gcStress()
			for k, hm := range held {
				if !messageOK(hm, k, seed) {
					ckf(false, "server held %d corrupted after GC", k)
					break
				}
			}
		}
	}
	gcStress()
	ckf(len(held) == perRound, "held=%d want=%d", len(held), perRound)
	for i, m := range held {
		if !messageOK(m, i, seed) {
			ckf(false, "server held %d corrupted after final GC", i)
			break
		}
	}

	// 4) 阻塞读在 socket 关闭时必须被唤醒为 ErrClosed。
	started := make(chan struct{})
	readErr := make(chan error, 1)
	go func() {
		b := make([]byte, frameLen)
		close(started)
		_, _, err := pc.ReadFromUDP(b)
		readErr <- err
	}()
	<-started
	gcStress()
	pc.Close()
	select {
	case err := <-readErr:
		ckf(errors.Is(err, net.ErrClosed), "blocked ReadFromUDP after close: %v, want net.ErrClosed", err)
	case <-time.After(waitLimit):
		ck(false, "blocked ReadFromUDP not woken by close")
	}
	client.Close()
}

func main() {
	seed := envSeed()
	rounds := envInt("TORTURE_ROUNDS", 2, 1, 50)
	fmt.Printf("per_round=%d rounds=%d\n", perRound, rounds)
	for r := 0; r < rounds; r++ {
		round(seed + int64(r)*151)
	}
	finish("l19_udp_deadline_churn")
}
