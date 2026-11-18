// L18: 已 close 的老缓冲 channel 仍持有年轻组合值。
// 老 chan 先接收年轻消息，再 close，跨多轮 GC 后用 range 排空；
// 同时校验 close 后的无缓冲老 chan 返回零值且 ok=false。
package main

import (
	"fmt"
	"os"
	"runtime"
	"strconv"
	"strings"
	"sync/atomic"
)

const (
	cbN     = 32
	cbDataN = 24
)

type cbNode struct {
	id   int64
	data [cbDataN]byte
}

type cbMsg struct {
	id   int64
	name string
	p    *cbNode
	ifc  interface{}
}

var (
	failed int32
	gcSink [][]byte
	cbCh   chan cbMsg
	cbUnb  chan cbMsg
	cbOut  []cbMsg
)

func ck(ok bool, msg string) {
	if !ok {
		atomic.AddInt32(&failed, 1)
		fmt.Println("FAIL:", msg)
	}
}

func gcStress() {
	for i := 0; i < 16; i++ {
		b := make([]byte, 4096)
		b[0] = byte(i)
		gcSink = append(gcSink[:0], b)
	}
	gcSink = gcSink[:0]
	runtime.GC()
	runtime.GC()
}

func finish(name string) {
	if n := atomic.LoadInt32(&failed); n != 0 {
		fmt.Printf("FAILED %s (%d failed checks)\n", name, n)
		os.Exit(1)
	}
	fmt.Println("ALL PASS " + name)
}

func seedValue() int64 {
	s := os.Getenv("UAF_SEED")
	if s == "" {
		return 1
	}
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return 1
	}
	return n
}

func cbSalt(seed int64) int {
	s := int(seed % 97)
	if s < 0 {
		s = -s
	}
	return s
}

func cbByte(id int64, i, salt int) byte {
	return byte((int(id)*31 + i*17 + salt) % 251)
}

//go:noinline
func cbMake(id int64, seed int64) cbMsg {
	salt := cbSalt(seed)
	name := strings.Repeat("m", 6+int((uint64(id)+uint64(salt))%7)) + "-" + strconv.FormatInt(id, 10)
	n := &cbNode{id: id*10 + 7}
	for i := range n.data {
		n.data[i] = cbByte(n.id, i, salt)
	}
	var ifc interface{}
	if id%2 == 0 {
		q := &cbNode{id: id*10 + 1}
		for i := range q.data {
			q.data[i] = cbByte(q.id, i, salt)
		}
		ifc = q
	} else {
		ifc = name + "!"
	}
	return cbMsg{id: id, name: name, p: n, ifc: ifc}
}

//go:noinline
func cbFill(ch chan cbMsg, n int, seed int64) {
	for i := 0; i < n; i++ {
		ch <- cbMake(int64(i), seed)
	}
}

//go:noinline
func cbClose(ch chan cbMsg) { close(ch) }

//go:noinline
func cbDrain(ch chan cbMsg, out []cbMsg) []cbMsg {
	for m := range ch {
		out = append(out, m)
	}
	return out
}

func cbCheck(m cbMsg, id int64, seed int64, label string) bool {
	salt := cbSalt(seed)
	wantName := strings.Repeat("m", 6+int((uint64(id)+uint64(salt))%7)) + "-" + strconv.FormatInt(id, 10)
	if m.id != id || m.name != wantName {
		ck(false, fmt.Sprintf("%s: msg %d header corrupted (seed=%d)", label, id, seed))
		return false
	}
	if m.p == nil || m.p.id != id*10+7 {
		ck(false, fmt.Sprintf("%s: msg %d node pointer corrupted (seed=%d)", label, id, seed))
		return false
	}
	for i := range m.p.data {
		if m.p.data[i] != cbByte(m.p.id, i, salt) {
			ck(false, fmt.Sprintf("%s: msg %d node data corrupted at %d (seed=%d)", label, id, i, seed))
			return false
		}
	}
	if id%2 == 0 {
		q, ok := m.ifc.(*cbNode)
		if !ok || q.id != id*10+1 {
			ck(false, fmt.Sprintf("%s: msg %d interface pointer corrupted (seed=%d)", label, id, seed))
			return false
		}
		for i := range q.data {
			if q.data[i] != cbByte(q.id, i, salt) {
				ck(false, fmt.Sprintf("%s: msg %d interface data corrupted at %d (seed=%d)", label, id, i, seed))
				return false
			}
		}
	} else {
		s, ok := m.ifc.(string)
		if !ok || s != wantName+"!" {
			ck(false, fmt.Sprintf("%s: msg %d interface string corrupted (seed=%d)", label, id, seed))
			return false
		}
	}
	return true
}

func main() {
	seed := seedValue()

	cbCh = make(chan cbMsg, cbN)
	gcStress()
	gcStress() // 空缓冲 chan 晋升为老对象

	cbFill(cbCh, cbN, seed)
	gcStress()
	gcStress() // 年轻消息进入老 chan 缓冲

	cbClose(cbCh)
	gcStress()
	gcStress() // close 后缓冲值必须仍是根

	cbOut = cbDrain(cbCh, cbOut)
	if len(cbOut) != cbN {
		ck(false, fmt.Sprintf("drained %d msgs want %d (seed=%d)", len(cbOut), cbN, seed))
	} else {
		for i := range cbOut {
			if !cbCheck(cbOut[i], int64(i), seed, "closed-buffered") {
				break
			}
		}
	}

	m, ok := <-cbCh
	if ok || m.id != 0 || m.name != "" || m.p != nil || m.ifc != nil {
		ck(false, fmt.Sprintf("receive after close not zero, ok=%v (seed=%d)", ok, seed))
	}

	cbUnb = make(chan cbMsg)
	gcStress()
	gcStress()
	cbClose(cbUnb)
	m, ok = <-cbUnb
	if ok || m.id != 0 || m.name != "" || m.p != nil || m.ifc != nil {
		ck(false, fmt.Sprintf("unbuffered closed chan not zero, ok=%v (seed=%d)", ok, seed))
	}

	gcStress()
	for i := 0; i < len(cbOut); i++ {
		if !cbCheck(cbOut[i], int64(i), seed, "sink-after-gc") {
			break
		}
	}

	finish("l18_closed_chan_buffer_gc")
}
