// L19: UDP deadline 的反复设置/清空边界。每周期先在 socket 上排入一个数据报，
// 再设置已过期读 deadline：ReadFromUDP 必须立即 timeout 且不得消费已排队报文；
// 清空 deadline 后该报文必须完整读出。已过期写 deadline 的写入不得发出，随后
// 的 marker 报文必须是下一个被读到的报文。每轮末尾验证 blocked ReadFromUDP
// 被 Close 唤醒为 net.ErrClosed。TORTURE_ROUNDS 放大轮数。
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
	frameLen       = 256
	wireMagic      = 0x4c394455 // "UD9L" little-endian
	cyclesPerRound = 8
	waitLimit      = 10 * time.Second
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
	return int64(id)*89 + seed*43
}

func msgName(id int) string {
	return fmt.Sprintf("udpd-%04d", id)
}

func pattern(id int) byte {
	return byte(id*11 + 5)
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

func getFrame(b []byte, seed int64) (*message, error) {
	if binary.LittleEndian.Uint32(b[0:4]) != wireMagic {
		return nil, errors.New("bad magic")
	}
	id := int(binary.LittleEndian.Uint32(b[4:8]))
	base := int64(binary.LittleEndian.Uint64(b[8:16]))
	if base != msgBase(id, seed) {
		return nil, errors.New("bad base")
	}
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
	base := msgBase(id, seed)
	h := m.head
	if h == nil || h.v != base || h.next == nil || h.next.v != base+1 {
		return false
	}
	return h.next.next != nil && h.next.next.v == base+2 && h.next.next.next == nil
}

func round(seed int64, roundIdx int) {
	pc, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 0})
	if err != nil {
		ckf(false, "round %d listen udp: %v", roundIdx, err)
		return
	}
	client, err := net.DialUDP("udp", nil, pc.LocalAddr().(*net.UDPAddr))
	if err != nil {
		ckf(false, "round %d dial udp: %v", roundIdx, err)
		pc.Close()
		return
	}
	defer pc.Close()
	defer client.Close()

	buf := make([]byte, frameLen)
	rx := make([]byte, frameLen)
	held := make([]*message, 0, cyclesPerRound*2)

	for c := 0; c < cyclesPerRound; c++ {
		id := roundIdx*100 + c
		// 1) 先排入数据报。
		putFrame(buf, id, seed)
		if _, err := client.Write(buf); err != nil {
			ckf(false, "round %d cycle %d: pre-send: %v", roundIdx, c, err)
			break
		}
		// 2) 过期读 deadline 必须立即失败，且不能消费已排队的数据报。
		if err := pc.SetReadDeadline(time.Now().Add(-time.Second)); err != nil {
			ckf(false, "round %d cycle %d: set past read deadline: %v", roundIdx, c, err)
			break
		}
		n, _, rerr := pc.ReadFromUDP(rx)
		ckf(n == 0 && errors.Is(rerr, os.ErrDeadlineExceeded), "round %d cycle %d: past read deadline n=%d err=%v", roundIdx, c, n, rerr)
		if err := pc.SetReadDeadline(time.Time{}); err != nil {
			ckf(false, "round %d cycle %d: clear read deadline: %v", roundIdx, c, err)
			break
		}
		// 3) 排队报文必须仍然可读且内容完整。
		if err := pc.SetReadDeadline(time.Now().Add(waitLimit)); err != nil {
			ckf(false, "round %d cycle %d: guard read deadline: %v", roundIdx, c, err)
			break
		}
		n, _, rerr = pc.ReadFromUDP(rx)
		ckf(n == frameLen && rerr == nil, "round %d cycle %d: queued datagram n=%d err=%v", roundIdx, c, n, rerr)
		if rerr == nil {
			m, err := getFrame(rx, seed)
			if err != nil || !messageOK(m, id, seed) {
				ckf(false, "round %d cycle %d: queued payload corrupted: %v", roundIdx, c, err)
			} else {
				held = append(held, m)
			}
		}
		if err := pc.SetReadDeadline(time.Time{}); err != nil {
			ckf(false, "round %d cycle %d: clear guard deadline: %v", roundIdx, c, err)
			break
		}

		// 4) 过期写 deadline 不得发出报文。
		if err := client.SetWriteDeadline(time.Now().Add(-time.Second)); err != nil {
			ckf(false, "round %d cycle %d: set past write deadline: %v", roundIdx, c, err)
			break
		}
		putFrame(buf, 5000+id, seed)
		wn, werr := client.Write(buf)
		ckf(errors.Is(werr, os.ErrDeadlineExceeded), "round %d cycle %d: past write deadline n=%d err=%v", roundIdx, c, wn, werr)
		if err := client.SetWriteDeadline(time.Time{}); err != nil {
			ckf(false, "round %d cycle %d: clear write deadline: %v", roundIdx, c, err)
			break
		}
		// 5) marker 必须是失败写之后第一个到达的报文。
		marker := 6000 + id
		putFrame(buf, marker, seed)
		if _, err := client.Write(buf); err != nil {
			ckf(false, "round %d cycle %d: marker send: %v", roundIdx, c, err)
			break
		}
		if err := pc.SetReadDeadline(time.Now().Add(waitLimit)); err != nil {
			ckf(false, "round %d cycle %d: marker guard deadline: %v", roundIdx, c, err)
			break
		}
		n, _, rerr = pc.ReadFromUDP(rx)
		ckf(n == frameLen && rerr == nil, "round %d cycle %d: marker n=%d err=%v", roundIdx, c, n, rerr)
		if rerr == nil {
			m, err := getFrame(rx, seed)
			if err != nil || !messageOK(m, marker, seed) {
				ckf(false, "round %d cycle %d: marker mismatch (failed write delivered?): %v", roundIdx, c, err)
			} else {
				held = append(held, m)
			}
		}
		if err := pc.SetReadDeadline(time.Time{}); err != nil {
			ckf(false, "round %d cycle %d: clear marker deadline: %v", roundIdx, c, err)
			break
		}
		if c%2 == 1 {
			gcStress()
			for _, m := range held {
				if !chainOKMsg(m) {
					ckf(false, "round %d cycle %d: held payload chain corrupted after GC", roundIdx, c)
					break
				}
			}
		}
	}

	// 6) blocked ReadFromUDP 必须被 Close 唤醒为 net.ErrClosed。
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
		ckf(errors.Is(err, net.ErrClosed), "round %d blocked ReadFromUDP err=%v, want net.ErrClosed", roundIdx, err)
	case <-time.After(waitLimit):
		ckf(false, "round %d blocked ReadFromUDP not woken by close", roundIdx)
	}

	gcStress()
	for _, m := range held {
		if !chainOKMsg(m) {
			ckf(false, "round %d held payload chain corrupted at end", roundIdx)
			break
		}
	}
}

func chainOKMsg(m *message) bool {
	if m == nil {
		return false
	}
	base := m.base
	h := m.head
	if h == nil || h.v != base || h.next == nil || h.next.v != base+1 {
		return false
	}
	return h.next.next != nil && h.next.next.v == base+2 && h.next.next.next == nil
}

func main() {
	seed := envSeed()
	rounds := envInt("TORTURE_ROUNDS", 2, 1, 50)
	fmt.Printf("cycles_per_round=%d rounds=%d\n", cyclesPerRound, rounds)
	for r := 0; r < rounds; r++ {
		round(seed+int64(r)*149, r)
	}
	gcStress()
	finish("l19_udp_deadline_cycles")
}
