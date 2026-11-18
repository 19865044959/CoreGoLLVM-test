// L05: select 热循环内分配：生产者向两个带缓冲 channel 发送 1200 条带链表指针
// 的消息，主协程在 select 中逐条接收并为每条分配 ack（拷贝 body、保留 msg 指针），
// 循环内每 64 条制造垃圾并显式 GC；结束后反向遍历链表并跨 GC 校验。
package main

import (
	"fmt"
	"os"
	"runtime"
	"strconv"
	"sync"
	"sync/atomic"
)

const (
	selTotal = 1200
	selGC    = 64
)

var (
	failed int32
	gcSink [][]byte
)

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

func finish(name string, seed uint64) {
	if n := atomic.LoadInt32(&failed); n != 0 {
		fmt.Printf("FAILED %s (%d failed checks, seed=%d)\n", name, n, seed)
		os.Exit(1)
	}
	fmt.Println("ALL PASS " + name)
}

func garbageGC() {
	for i := 0; i < 16; i++ {
		b := make([]byte, 16<<10)
		b[0] = byte(i)
		gcSink = append(gcSink[:0], b)
	}
	gcSink = gcSink[:0]
	runtime.GC()
	runtime.GC()
}

func msgByte(id, off int, seed uint64) byte {
	return byte((seed*23 + uint64(id)*17 + uint64(off)*7 + 5) % 251)
}

func msgLen(id int) int {
	return 16 + id%81
}

type selMsg struct {
	id   int
	src  int
	body []byte
	prev *selMsg
}

type selAck struct {
	id   int
	sum  uint64
	body []byte
	msg  *selMsg
}

func msgSum(m *selMsg) uint64 {
	h := uint64(1469598103934665603)
	for i := range m.body {
		h ^= uint64(m.body[i])
		h *= 1099511628211
	}
	h ^= uint64(m.id) * 131
	return h
}

func produce(seed uint64, chA, chB chan *selMsg, wg *sync.WaitGroup) {
	defer wg.Done()
	var last0, last1 *selMsg
	for i := 0; i < selTotal; i++ {
		n := msgLen(i)
		body := make([]byte, n)
		for j := range body {
			body[j] = msgByte(i, j, seed)
		}
		m := &selMsg{id: i, src: i % 2, body: body}
		if m.src == 0 {
			m.prev = last0
			last0 = m
			chA <- m
		} else {
			m.prev = last1
			last1 = m
			chB <- m
		}
	}
	close(chA)
	close(chB)
}

func handle(m *selMsg, seed uint64) *selAck {
	// 每条消息在 select 分支内分配 ack 与独立 body 副本。
	body := append([]byte(nil), m.body...)
	a := &selAck{id: m.id, sum: msgSum(m), body: body, msg: m}
	a.sum ^= 0x9e3779b97f4a7c15
	return a
}

func main() {
	seed := loadSeed()
	chA := make(chan *selMsg, selTotal)
	chB := make(chan *selMsg, selTotal)
	var wg sync.WaitGroup
	wg.Add(1)
	go produce(seed, chA, chB, &wg)

	results := make([]*selAck, selTotal)
	var last0, last1 *selMsg
	recvd, countA, countB := 0, 0, 0
	ca, cb := chA, chB
	for ca != nil || cb != nil {
		select {
		case m, ok := <-ca:
			if !ok {
				ca = nil
				continue
			}
			results[m.id] = handle(m, seed)
			if m.src == 0 {
				countA++
				last0 = m
			} else {
				countB++
				last1 = m
			}
			recvd++
			if recvd%selGC == 0 {
				garbageGC()
			}
		case m, ok := <-cb:
			if !ok {
				cb = nil
				continue
			}
			results[m.id] = handle(m, seed)
			if m.src == 0 {
				countA++
				last0 = m
			} else {
				countB++
				last1 = m
			}
			recvd++
			if recvd%selGC == 0 {
				garbageGC()
			}
		}
	}
	wg.Wait()

	ckf(recvd == selTotal, "received=%d want=%d", recvd, selTotal)
	ckf(countA == selTotal/2 && countB == selTotal/2, "counts A=%d B=%d", countA, countB)

	garbageGC()
	for i := 0; i < selTotal; i++ {
		a := results[i]
		if a == nil {
			ckf(false, "ack id=%d missing", i)
			continue
		}
		ckf(a.id == i, "ack id=%d has id=%d", i, a.id)
		ckf(len(a.body) == msgLen(i), "ack id=%d body len=%d want=%d", i, len(a.body), msgLen(i))
		for j := 0; j < len(a.body); j += 13 {
			if a.body[j] != msgByte(i, j, seed) {
				ckf(false, "ack id=%d body byte at=%d", i, j)
				break
			}
		}
		ckf(a.msg != nil && a.msg.id == i, "ack id=%d lost msg pointer", i)
		if a.msg != nil {
			ckf(msgSum(a.msg)^0x9e3779b97f4a7c15 == a.sum, "ack id=%d msg sum changed", i)
		}
	}

	// 反向遍历两条 prev 链，验证 select 乱序接收后链表仍完整、无环。
	for src, last := range []*selMsg{last0, last1} {
		n := 0
		var sum int64
		for m := last; m != nil; m = m.prev {
			if m.src != src || m.id%2 != src {
				ckf(false, "link src=%d node id=%d src=%d", src, m.id, m.src)
				break
			}
			n++
			sum += int64(m.id)
			if n > selTotal {
				ckf(false, "link src=%d cycle detected", src)
				break
			}
		}
		ckf(n == selTotal/2, "link src=%d len=%d want=%d", src, n, selTotal/2)
		if src == 0 {
			// 0,2,...,1198 的和
			ckf(sum == int64((selTotal/2-1)*(selTotal/2)), "link src=0 sum=%d", sum)
		} else {
			// 1,3,...,1199 的和 = 0+1+...+1199 - 偶数和
			ckf(sum == int64(selTotal*(selTotal-1)/2)-int64((selTotal/2-1)*(selTotal/2)), "link src=1 sum=%d", sum)
		}
	}

	// ack 持有的 body 是独立副本：改写原消息 body 不应影响 ack。
	if results[7] != nil && results[7].msg != nil && len(results[7].msg.body) > 0 && len(results[7].body) > 0 {
		orig := results[7].body[0]
		results[7].msg.body[0] ^= 0xff
		ckf(results[7].body[0] == orig, "ack body aliases msg body")
	}
	garbageGC()
	for i := 0; i < selTotal; i += 137 {
		if results[i] == nil || results[i].msg == nil {
			ckf(false, "ack id=%d lost after final GC", i)
		}
	}

	finish("l05_select_loop_alloc", seed)
}
