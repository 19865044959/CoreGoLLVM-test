// L19: UDP 回环。127.0.0.1:0 上起 UDP echo 服务端，客户端逐个发送 512 字节
// 定长帧（id/base/seed/checksum/pattern），服务端解码校验后回显 base+1。
// 客户端每 16 个报文显式 GC 并校验已收报文的指针链；服务端同样持有指针链。
package main

import (
	"encoding/binary"
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

var failed int32

const (
	udpFrameSize = 512
	udpHeader    = 28
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

func envRounds(def int) int {
	s := os.Getenv("TORTURE_ROUNDS")
	if s == "" {
		return def
	}
	v, err := strconv.Atoi(s)
	if err != nil || v < 1 {
		return def
	}
	if v > 20 {
		v = 20
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

func fillUDPFrame(dst []byte, id int, base, seed int64) {
	binary.BigEndian.PutUint32(dst[0:4], uint32(id))
	binary.BigEndian.PutUint64(dst[4:12], uint64(base))
	binary.BigEndian.PutUint64(dst[12:20], uint64(seed))
	binary.BigEndian.PutUint64(dst[20:28], uint64(base)*3+3)
	for i := udpHeader; i < len(dst); i++ {
		dst[i] = byte((int64(id)*17 + int64(i)*5 + seed) & 0xff)
	}
}

func parseUDPFrame(src []byte) (id int, base, seed int64, ok bool) {
	if len(src) != udpFrameSize {
		return 0, 0, 0, false
	}
	id = int(binary.BigEndian.Uint32(src[0:4]))
	base = int64(binary.BigEndian.Uint64(src[4:12]))
	seed = int64(binary.BigEndian.Uint64(src[12:20]))
	if binary.BigEndian.Uint64(src[20:28]) != uint64(base)*3+3 {
		return 0, 0, 0, false
	}
	for i := udpHeader; i < len(src); i++ {
		if src[i] != byte((int64(id)*17+int64(i)*5+seed)&0xff) {
			return 0, 0, 0, false
		}
	}
	return id, base, seed, true
}

type udpServerResult struct {
	seen int
	bad  int
	err  error
}

func runUDPServer(conn *net.UDPConn, total int, seed int64, chains []*node, bases []int64, res chan<- udpServerResult) {
	buf := make([]byte, 2048)
	seen, bad := 0, 0
	for seen < total {
		n, addr, err := conn.ReadFromUDP(buf)
		if err != nil {
			res <- udpServerResult{seen: seen, bad: bad, err: err}
			return
		}
		id, base, sd, ok := parseUDPFrame(buf[:n])
		if !ok || id != seen || sd != seed {
			bad++
		}
		wantBase := base + 1
		chains[seen] = makeChain(wantBase)
		bases[seen] = wantBase
		out := make([]byte, n)
		fillUDPFrame(out, id, wantBase, seed)
		if _, err := conn.WriteToUDP(out, addr); err != nil {
			res <- udpServerResult{seen: seen + 1, bad: bad, err: err}
			return
		}
		seen++
		if seen%16 == 0 {
			gcStress()
			for i := 0; i < seen; i++ {
				if !chainOK(chains[i], bases[i]) {
					bad++
				}
			}
		}
	}
	gcStress()
	for i := 0; i < seen; i++ {
		if !chainOK(chains[i], bases[i]) {
			bad++
		}
	}
	res <- udpServerResult{seen: seen, bad: bad}
}

func main() {
	seed := envSeed()
	rounds := envRounds(1)
	const total = 64

	for r := 0; r < rounds; r++ {
		server, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 0})
		ckf(err == nil, "listen udp: %v (seed=%d round=%d)", err, seed, r)
		if err != nil {
			break
		}
		raddr := server.LocalAddr().(*net.UDPAddr)
		client, err := net.DialUDP("udp", nil, raddr)
		ckf(err == nil, "dial udp: %v (seed=%d round=%d)", err, seed, r)
		if err != nil {
			server.Close()
			break
		}

		sChains := make([]*node, total)
		sBases := make([]int64, total)
		sres := make(chan udpServerResult, 1)
		go runUDPServer(server, total, seed, sChains, sBases, sres)

		var bad int32
		msgs := make([]*node, 0, total)
		for i := 0; i < total; i++ {
			base := seed*100000 + int64(r)*10000 + int64(i)*3 + 1
			buf := make([]byte, udpFrameSize)
			fillUDPFrame(buf, i, base, seed)
			if _, err := client.Write(buf); err != nil {
				ckf(false, "udp write %d: %v (seed=%d round=%d)", i, err, seed, r)
				break
			}
			rbuf := make([]byte, udpFrameSize)
			client.SetReadDeadline(time.Now().Add(waitLimit))
			n, err := client.Read(rbuf)
			if err != nil {
				ckf(false, "udp read %d: %v (seed=%d round=%d)", i, err, seed, r)
				break
			}
			id, b, sd, ok := parseUDPFrame(rbuf[:n])
			if !ok || id != i || b != base+1 || sd != seed {
				atomic.StoreInt32(&bad, 1)
			}
			msgs = append(msgs, makeChain(b))
			if i%16 == 15 {
				gcStress()
				for j, m := range msgs {
					want := seed*100000 + int64(r)*10000 + int64(j)*3 + 2
					if !chainOK(m, want) {
						atomic.StoreInt32(&bad, 1)
					}
				}
			}
		}
		client.Close()

		var sr udpServerResult
		select {
		case sr = <-sres:
		case <-time.After(waitLimit):
			ckf(false, "udp server did not finish (seed=%d round=%d)", seed, r)
		}
		server.Close()

		ckf(sr.err == nil, "udp server error: %v (seed=%d round=%d)", sr.err, seed, r)
		ckf(sr.seen == total, "udp server seen=%d want=%d (seed=%d round=%d)", sr.seen, total, seed, r)
		ckf(sr.bad == 0, "udp server bad=%d (seed=%d round=%d)", sr.bad, seed, r)
		ckf(atomic.LoadInt32(&bad) == 0, "udp client payload corrupted (seed=%d round=%d)", seed, r)
		ckf(len(msgs) == total, "udp client got=%d want=%d (seed=%d round=%d)", len(msgs), total, seed, r)

		gcStress()
		badMsgs := 0
		for j, m := range msgs {
			want := seed*100000 + int64(r)*10000 + int64(j)*3 + 2
			if !chainOK(m, want) {
				badMsgs++
			}
		}
		ckf(badMsgs == 0, "udp client chains corrupted: bad=%d (seed=%d round=%d)", badMsgs, seed, r)
	}

	fmt.Printf("rounds=%d datagrams_per_round=%d frame_size=%d\n", rounds, total, udpFrameSize)
	finish("l19_udp_loopback")
}
