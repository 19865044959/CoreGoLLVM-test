// L18: 前端消息分帧读取——半条消息返回错误，补齐后重读成功，数据不串不丢。
package main

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"runtime"
	"sync/atomic"
)

var (
	failed      int32
	errNeedMore = errors.New("frontend: need more data")
	errBadLen   = errors.New("frontend: invalid message length")
)

func ck(ok bool, msg string) {
	if !ok {
		atomic.AddInt32(&failed, 1)
		fmt.Println("FAIL:", msg)
	}
}

func gcStress() {
	var sink [][]byte
	for i := 0; i < 10; i++ {
		b := make([]byte, 4096)
		b[0] = byte(i)
		sink = append(sink, b)
	}
	runtime.GC()
	runtime.GC()
	runtime.KeepAlive(sink)
}

type message struct {
	typ  byte
	body []byte
}

type frontend struct {
	buf []byte // 未消费的完整/部分消息字节
}

func (f *frontend) push(p []byte) {
	f.buf = append(f.buf, p...)
}

func (f *frontend) buffered() int { return len(f.buf) }

// Receive 在数据不足时不消费任何字节；成功后返回独立的 body 拷贝。
func (f *frontend) Receive() (*message, error) {
	if len(f.buf) < 5 {
		return nil, errNeedMore
	}
	typ := f.buf[0]
	l := binary.BigEndian.Uint32(f.buf[1:5])
	if l < 4 {
		return nil, errBadLen
	}
	total := 1 + int(l)
	if len(f.buf) < total {
		return nil, errNeedMore
	}
	body := make([]byte, int(l)-4)
	copy(body, f.buf[5:total])
	f.buf = f.buf[total:]
	return &message{typ: typ, body: body}, nil
}

func makeWire(typ byte, body []byte) []byte {
	b := make([]byte, 5+len(body))
	b[0] = typ
	binary.BigEndian.PutUint32(b[1:5], uint32(4+len(body)))
	copy(b[5:], body)
	return b
}

func verifyMsg(m *message, typ byte, body []byte, where string) {
	ok := m != nil && m.typ == typ && bytes.Equal(m.body, body)
	ck(ok, where)
}

func bodyPattern(seed, n int) []byte {
	b := make([]byte, n)
	for i := range b {
		b[i] = byte((seed*29 + i*11) % 251)
	}
	return b
}

// splitRound: 在每一个可能的位置把一条消息切成两半，验证半条消息错误 + 补齐后成功。
func splitRound(typ byte, body []byte) {
	wire := makeWire(typ, body)
	for split := 1; split < len(wire); split++ {
		var f frontend
		f.push(wire[:split])
		m, err := f.Receive()
		ck(m == nil && err == errNeedMore, fmt.Sprintf("split=%d type=%c: expected need-more", split, typ))
		// 无新数据时再次读取：同样的错误，且不得消费半条消息。
		m, err = f.Receive()
		ck(m == nil && err == errNeedMore, fmt.Sprintf("split=%d type=%c: repeat read consumed data", split, typ))
		ck(f.buffered() == split, fmt.Sprintf("split=%d type=%c: buffered bytes changed", split, typ))

		gcStress() // partial bytes 仍存活于 frontend.buf
		f.push(wire[split:])
		m, err = f.Receive()
		ck(err == nil, fmt.Sprintf("split=%d type=%c: completed read failed", split, typ))
		verifyMsg(m, typ, body, fmt.Sprintf("split=%d type=%c: completed payload mismatch", split, typ))
		ck(f.buffered() == 0, fmt.Sprintf("split=%d type=%c: trailing bytes", split, typ))

		// 紧接着再放一条不同消息，确认没有残留串包。
		nextBody := bodyPattern(split, 13)
		f.push(makeWire('X', nextBody))
		m, err = f.Receive()
		ck(err == nil, fmt.Sprintf("split=%d: next message read failed", split))
		verifyMsg(m, 'X', nextBody, fmt.Sprintf("split=%d: next message mixed", split))
		gcStress()
	}
}

func pipelineRound() {
	bodyA := bodyPattern(1, 23)
	bodyB := bodyPattern(2, 61)
	wireB := makeWire('B', bodyB)

	for i := 0; i < 8; i++ {
		var f frontend
		f.push(makeWire('A', bodyA))
		f.push(wireB[:len(wireB)/2])
		ma, err := f.Receive()
		ck(err == nil, "pipeline: A read failed")
		verifyMsg(ma, 'A', bodyA, "pipeline: A payload mismatch")
		if ma == nil {
			return
		}
		// 保留 ma.body，读取后续消息并 GC；独立拷贝语义下内容必须不变。
		mb, err := f.Receive()
		ck(mb == nil && err == errNeedMore, "pipeline: B half read should need more")
		ck(f.buffered() > 0, "pipeline: half B lost")
		gcStress()
		f.push(wireB[len(wireB)/2:])
		mb, err = f.Receive()
		ck(err == nil, "pipeline: B completed read failed")
		verifyMsg(mb, 'B', bodyB, "pipeline: B payload mismatch")
		ck(f.buffered() == 0, "pipeline: unread bytes after B")
		gcStress()
		verifyMsg(ma, 'A', bodyA, "pipeline: retained A body corrupted")

		// 空 body 消息（length=4）。
		f.push(makeWire('Z', nil))
		mz, err := f.Receive()
		ck(err == nil && mz != nil && mz.typ == 'Z' && len(mz.body) == 0, "pipeline: empty body message")
	}
}

func main() {
	// 覆盖：半 header、半 body、header 齐全但 body 为空、所有切分点。
	splitRound('A', []byte("hello"))
	splitRound('B', bodyPattern(3, 32))
	splitRound('C', nil)

	// 多消息流水线 + 半条消息 + GC + body 保留。
	pipelineRound()

	// 非法长度（<4）：返回确定错误，不 panic、不分配大缓冲。
	{
		var f frontend
		f.push([]byte{'E', 0, 0, 0, 3})
		m, err := f.Receive()
		ck(m == nil && err == errBadLen, "bad length not rejected")
	}
	// 极大长度但数据不足：need-more，不 OOM。
	{
		var f frontend
		f.push([]byte{'H', 0xFF, 0xFF, 0xFF, 0xF0})
		m, err := f.Receive()
		ck(m == nil && err == errNeedMore, "huge length with short data not need-more")
	}

	if n := atomic.LoadInt32(&failed); n != 0 {
		fmt.Printf("FAILED l18_partial_message_recovery (%d failed checks)\n", n)
		os.Exit(1)
	}
	fmt.Println("ALL PASS l18_partial_message_recovery")
}
