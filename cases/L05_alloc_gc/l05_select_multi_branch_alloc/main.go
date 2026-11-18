// L05: select 多路接收循环中的逐条分配。三条已预填并关闭的带缓冲 channel
// 通过 select 接收，每个分支内分配独立 body 副本、tag map 与捕获 val 的闭包；
// 收齐后关闭的分支置 nil 退出。每 128 条显式 GC + 垃圾分配。另用 nil channel
// 的 select/default 分支做固定次数分配探测。默认 2 轮，TORTURE_ROUNDS 可放大。
package main

import (
	"fmt"
	"os"
	"runtime"
	"strconv"
	"sync/atomic"
)

const (
	smbPerChan = 500
	smbProbes  = 128
)

var (
	failed int32
	gcSink [][]byte
)

type smbMsg struct {
	src  int
	id   int
	body []byte
	val  int64
}

type smbAck struct {
	src  int
	id   int
	body []byte
	val  int64
	tag  map[string]int
	fn   func() int64
}

func ck(ok bool, msg string) {
	if !ok {
		atomic.AddInt32(&failed, 1)
		fmt.Println("FAIL:", msg)
	}
}

func ckf(ok bool, format string, args ...interface{}) {
	if !ok {
		atomic.AddInt32(&failed, 1)
		fmt.Printf("FAIL: "+format+"\n", args...)
	}
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
		return hi
	}
	return n
}

func finish(name string, seed uint64) {
	if n := atomic.LoadInt32(&failed); n != 0 {
		fmt.Printf("FAILED %s (%d failed checks, seed=%d)\n", name, n, seed)
		os.Exit(1)
	}
	fmt.Println("ALL PASS " + name)
}

func gcStress() {
	for i := 0; i < 20; i++ {
		b := make([]byte, 16<<10)
		b[0] = byte(i)
		gcSink = append(gcSink[:0], b)
	}
	gcSink = gcSink[:0]
	runtime.GC()
	runtime.GC()
}

func smbBody(src, id, j int, seed uint64) byte {
	return byte((seed*19 + uint64(src)*37 + uint64(id)*11 + uint64(j)*5) % 251)
}

func smbVal(src, id int, seed uint64) int64 {
	return int64(seed)*1000000 + int64(src)*100000 + int64(id)*7
}

func consume(m smbMsg) *smbAck {
	body := append([]byte(nil), m.body...)
	tag := map[string]int{"src": m.src, "id": m.id}
	val := m.val
	fn := func() int64 { return val }
	return &smbAck{src: m.src, id: m.id, body: body, val: m.val, tag: tag, fn: fn}
}

func smbCheck(a *smbAck, sent [3][]smbMsg) {
	if a == nil {
		ck(false, "nil ack")
		return
	}
	ckf(a.src >= 0 && a.src < 3, "ack src=%d", a.src)
	if a.src < 0 || a.src >= 3 {
		return
	}
	ckf(a.id >= 0 && a.id < smbPerChan, "src=%d id=%d out of range", a.src, a.id)
	if a.id < 0 || a.id >= smbPerChan {
		return
	}
	ref := sent[a.src][a.id]
	ckf(a.val == ref.val, "src=%d id=%d val=%d want=%d", a.src, a.id, a.val, ref.val)
	ckf(len(a.body) == len(ref.body), "src=%d id=%d body len=%d want=%d", a.src, a.id, len(a.body), len(ref.body))
	if len(a.body) != len(ref.body) {
		return
	}
	for j := range a.body {
		if a.body[j] != ref.body[j] {
			ckf(false, "src=%d id=%d body byte=%d got=%d want=%d", a.src, a.id, j, a.body[j], ref.body[j])
			break
		}
	}
	ckf(&a.body[0] != &ref.body[0], "src=%d id=%d ack body aliases message body", a.src, a.id)
	if a.fn == nil {
		ckf(false, "src=%d id=%d nil closure", a.src, a.id)
	} else {
		ckf(a.fn() == ref.val, "src=%d id=%d closure=%d want=%d", a.src, a.id, a.fn(), ref.val)
	}
	ckf(a.tag != nil && a.tag["src"] == a.src && a.tag["id"] == a.id, "src=%d id=%d tag map corrupted", a.src, a.id)
}

func main() {
	seed := loadSeed()
	rounds := envInt("TORTURE_ROUNDS", 2, 1, 8)

	// 三路数据在所有轮次间复用（只读）。
	var sent [3][]smbMsg
	for src := 0; src < 3; src++ {
		sent[src] = make([]smbMsg, smbPerChan)
		for id := 0; id < smbPerChan; id++ {
			n := 16 + int((seed+uint64(src)*7+uint64(id)*13)%48)
			body := make([]byte, n)
			for j := range body {
				body[j] = smbBody(src, id, j, seed)
			}
			sent[src][id] = smbMsg{src: src, id: id, body: body, val: smbVal(src, id, seed)}
		}
	}

	// 默认分支的逐次分配：nil channel 永远不可读，必然走 default。
	probes := 0
	var never chan int
	probeSink := make([][]byte, 0, smbProbes)
	for i := 0; i < smbProbes; i++ {
		select {
		case <-never:
			ck(false, "nil channel became ready")
		default:
			probes++
			p := make([]byte, 32)
			p[0] = byte(i)
			probeSink = append(probeSink[:0], p)
		}
	}
	ckf(probes == smbProbes, "default probes=%d want=%d", probes, smbProbes)
	runtime.KeepAlive(probeSink)

	for round := 0; round < rounds; round++ {
		c0 := make(chan smbMsg, smbPerChan)
		c1 := make(chan smbMsg, smbPerChan)
		c2 := make(chan smbMsg, smbPerChan)
		for id := 0; id < smbPerChan; id++ {
			c0 <- sent[0][id]
			c1 <- sent[1][id]
			c2 <- sent[2][id]
		}
		close(c0)
		close(c1)
		close(c2)

		var acks [3][]*smbAck
		var counts [3]int
		received := 0
		active := 3
		for active > 0 {
			select {
			case m, ok := <-c0:
				if !ok {
					c0 = nil
					active--
					continue
				}
				acks[0] = append(acks[0], consume(m))
				counts[0]++
				received++
			case m, ok := <-c1:
				if !ok {
					c1 = nil
					active--
					continue
				}
				acks[1] = append(acks[1], consume(m))
				counts[1]++
				received++
			case m, ok := <-c2:
				if !ok {
					c2 = nil
					active--
					continue
				}
				acks[2] = append(acks[2], consume(m))
				counts[2]++
				received++
			}
			if received > 0 && received%128 == 0 {
				gcStress()
			}
		}

		ckf(received == 3*smbPerChan, "round=%d received=%d want=%d", round, received, 3*smbPerChan)
		for src := 0; src < 3; src++ {
			ckf(counts[src] == smbPerChan, "round=%d src=%d count=%d want=%d", round, src, counts[src], smbPerChan)
			ckf(len(acks[src]) == smbPerChan, "round=%d src=%d acks=%d want=%d", round, src, len(acks[src]), smbPerChan)
		}

		gcStress()
		for src := 0; src < 3; src++ {
			seen := make([]bool, smbPerChan)
			for _, a := range acks[src] {
				if a == nil || a.id < 0 || a.id >= smbPerChan {
					ckf(false, "round=%d src=%d invalid ack", round, src)
					continue
				}
				ckf(!seen[a.id], "round=%d src=%d duplicate ack id=%d", round, src, a.id)
				seen[a.id] = true
				smbCheck(a, sent)
			}
			for id := 0; id < smbPerChan; id++ {
				ckf(seen[id], "round=%d src=%d missing ack id=%d", round, src, id)
			}
		}

		// 改写一条 ack 的 body，不应影响原始消息数据。
		for src := 0; src < 3; src++ {
			if len(acks[src]) == 0 {
				continue
			}
			a := acks[src][0]
			orig := sent[src][a.id].body[0]
			a.body[0] ^= 0xff
			ckf(sent[src][a.id].body[0] == orig, "round=%d src=%d ack body shares message storage", round, src)
			a.body[0] ^= 0xff
		}

		acks[0], acks[1], acks[2] = nil, nil, nil
		gcStress()
	}

	finish("l05_select_multi_branch_alloc", seed)
}
