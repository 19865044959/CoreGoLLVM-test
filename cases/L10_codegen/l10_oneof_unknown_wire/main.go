// l10_oneof_unknown_wire: generated oneof wire codec that additionally keeps
// unknown fields (unrecognized numbers or known numbers with a mismatched wire
// type) as raw tag+value bytes, re-emits them in order, and merges/clears the
// union independently from the unknown payload.
package main

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"math/rand"
	"os"
	"runtime"
	"strconv"
)

var (
	failed int
	seed   = int64(1)
	gcSink [][]byte
)

func ck(ok bool, msg string) {
	if !ok {
		failed++
		fmt.Println("FAIL:", msg)
	}
}

func ckSeed(ok bool, msg string) {
	if !ok {
		failed++
		fmt.Printf("FAIL: %s (UAF_SEED=%d)\n", msg, seed)
	}
}

func gcStress() {
	for i := 0; i < 16; i++ {
		b := make([]byte, 4096)
		b[0] = byte(i)
		b[len(b)-1] = byte(i * 19)
		gcSink = append(gcSink, b)
	}
	for i := range gcSink {
		gcSink[i] = nil
	}
	gcSink = gcSink[:0]
	runtime.GC()
	runtime.GC()
}

func finish(name string) {
	if failed != 0 {
		fmt.Printf("FAILED %s (%d failed checks)\n", name, failed)
		os.Exit(1)
	}
	fmt.Println("ALL PASS " + name)
}

func readSeed() int64 {
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

func envInt(name string, def int) int {
	s := os.Getenv(name)
	if s == "" {
		return def
	}
	v, err := strconv.Atoi(s)
	if err != nil || v < 0 {
		return def
	}
	return v
}

// ---------- wire ----------

func putUvarint(b []byte, v uint64) []byte {
	for v >= 0x80 {
		b = append(b, byte(v)|0x80)
		v >>= 7
	}
	return append(b, byte(v))
}

func readUvarint(b []byte) (uint64, int, error) {
	var v uint64
	for i := 0; i < 10; i++ {
		if i >= len(b) {
			return 0, 0, fmt.Errorf("truncated varint")
		}
		c := b[i]
		if i == 9 && c > 1 {
			return 0, 0, fmt.Errorf("varint overflow")
		}
		v |= uint64(c&0x7f) << (7 * uint(i))
		if c < 0x80 {
			return v, i + 1, nil
		}
	}
	return 0, 0, fmt.Errorf("varint overflow")
}

func putTag(b []byte, num int32, wt int) []byte { return putUvarint(b, uint64(num)<<3|uint64(wt)) }

func putLen(b []byte, num int32, payload []byte) []byte {
	b = putTag(b, num, 2)
	b = putUvarint(b, uint64(len(payload)))
	return append(b, payload...)
}

func readTag(b []byte) (num int32, wt, n int, err error) {
	v, n, err := readUvarint(b)
	if err != nil {
		return 0, 0, 0, err
	}
	num = int32(v >> 3)
	wt = int(v & 7)
	if num <= 0 {
		return 0, 0, 0, fmt.Errorf("invalid field number")
	}
	return num, wt, n, nil
}

func skipValue(data []byte, num int32, wt int) (int, error) {
	switch wt {
	case 0:
		_, n, err := readUvarint(data)
		return n, err
	case 1:
		if len(data) < 8 {
			return 0, fmt.Errorf("truncated fixed64")
		}
		return 8, nil
	case 2:
		l, n, err := readUvarint(data)
		if err != nil {
			return 0, err
		}
		if l > uint64(len(data)-n) {
			return 0, fmt.Errorf("truncated bytes")
		}
		return n + int(l), nil
	case 3:
		i := 0
		for {
			innerNum, innerWt, n, err := readTag(data[i:])
			if err != nil {
				return 0, err
			}
			i += n
			if innerWt == 4 {
				if innerNum != num {
					return 0, fmt.Errorf("mismatched end group")
				}
				return i, nil
			}
			m, err := skipValue(data[i:], innerNum, innerWt)
			if err != nil {
				return 0, err
			}
			i += m
		}
	case 4:
		return 0, fmt.Errorf("unexpected end group")
	case 5:
		if len(data) < 4 {
			return 0, fmt.Errorf("truncated fixed32")
		}
		return 4, nil
	default:
		return 0, fmt.Errorf("unsupported wire type %d", wt)
	}
}

// ---------- oneof model ----------

type node struct {
	v int64
	s string
}

type isChoice interface {
	fieldNum() int
	wireType() int
}

type (
	chText  string
	chNum   int64
	chBlob  []byte
	chNode  node
	chFlag  bool
	chCount uint32
)

func (chText) fieldNum() int  { return 1 }
func (chText) wireType() int  { return 2 }
func (chNum) fieldNum() int   { return 2 }
func (chNum) wireType() int   { return 0 }
func (chBlob) fieldNum() int  { return 3 }
func (chBlob) wireType() int  { return 2 }
func (*chNode) fieldNum() int { return 4 }
func (*chNode) wireType() int { return 2 }
func (chFlag) fieldNum() int  { return 5 }
func (chFlag) wireType() int  { return 0 }
func (chCount) fieldNum() int { return 6 }
func (chCount) wireType() int { return 5 }

func choiceField(c isChoice) int {
	if c == nil {
		return 0
	}
	switch x := c.(type) {
	case *chNode:
		if x == nil {
			return 0
		}
		return 4
	case chText:
		return 1
	case chNum:
		return 2
	case chBlob:
		return 3
	case chFlag:
		return 5
	case chCount:
		return 6
	}
	return 0
}

func cloneChoice(c isChoice) isChoice {
	switch x := c.(type) {
	case nil:
		return nil
	case *chNode:
		if x == nil {
			return (*chNode)(nil)
		}
		cp := *x
		return &cp
	case chBlob:
		return chBlob(copyBytes(x))
	case chText, chNum, chFlag, chCount:
		return x
	}
	return nil
}

func copyBytes(b []byte) []byte {
	if b == nil {
		return nil
	}
	out := make([]byte, len(b))
	copy(out, b)
	return out
}

func equalChoice(a, b isChoice) bool {
	if choiceField(a) != choiceField(b) {
		return false
	}
	switch x := a.(type) {
	case nil:
		return true
	case chText:
		y, ok := b.(chText)
		return ok && x == y
	case chNum:
		y, ok := b.(chNum)
		return ok && x == y
	case chBlob:
		y, ok := b.(chBlob)
		return ok && bytes.Equal(x, y)
	case *chNode:
		if x == nil {
			y, ok := b.(*chNode)
			return ok && y == nil
		}
		y, ok := b.(*chNode)
		return ok && y != nil && x.v == y.v && x.s == y.s
	case chFlag:
		y, ok := b.(chFlag)
		return ok && x == y
	case chCount:
		y, ok := b.(chCount)
		return ok && x == y
	}
	return false
}

type rawField struct {
	num int32
	wt  int
	raw []byte
}

type choiceMsg struct {
	which   isChoice
	unknown []rawField
}

func (m *choiceMsg) hasChoice() bool { return choiceField(m.which) != 0 }

func (m *choiceMsg) ClearChoice()  { m.which = nil }
func (m *choiceMsg) ClearUnknown() { m.unknown = nil }
func (m *choiceMsg) Reset()        { m.which = nil; m.unknown = nil }

// ---------- node codec ----------

func decodeNodePayload(p []byte) (*node, error) {
	n := &node{}
	i := 0
	for i < len(p) {
		num, wt, k, err := readTag(p[i:])
		if err != nil {
			return nil, err
		}
		i += k
		switch {
		case num == 1 && wt == 0:
			v, m, err := readUvarint(p[i:])
			if err != nil {
				return nil, err
			}
			i += m
			n.v = int64(v)
		case num == 2 && wt == 2:
			l, m, err := readUvarint(p[i:])
			if err != nil {
				return nil, err
			}
			i += m
			if l > uint64(len(p)-i) {
				return nil, fmt.Errorf("truncated node.s")
			}
			n.s = string(p[i : i+int(l)])
			i += int(l)
		default:
			m, err := skipValue(p[i:], num, wt)
			if err != nil {
				return nil, err
			}
			i += m
		}
	}
	return n, nil
}

func encodeNode(n *node) []byte {
	var b []byte
	b = putTag(b, 1, 0)
	b = putUvarint(b, uint64(n.v))
	if n.s != "" {
		b = putLen(b, 2, []byte(n.s))
	}
	return b
}

// ---------- message codec ----------

var choiceWire = map[int32]int{1: 2, 2: 0, 3: 2, 4: 2, 5: 0, 6: 5}

func decodeChoiceMsg(data []byte) (*choiceMsg, error) {
	m := &choiceMsg{}
	i := 0
	for i < len(data) {
		start := i
		num, wt, n, err := readTag(data[i:])
		if err != nil {
			return nil, err
		}
		i += n
		wantWt, known := choiceWire[num]
		if known && wantWt == wt {
			switch num {
			case 1:
				l, k, err := readUvarint(data[i:])
				if err != nil {
					return nil, err
				}
				i += k
				if l > uint64(len(data)-i) {
					return nil, fmt.Errorf("truncated text")
				}
				m.which = chText(string(data[i : i+int(l)]))
				i += int(l)
			case 2:
				v, k, err := readUvarint(data[i:])
				if err != nil {
					return nil, err
				}
				i += k
				m.which = chNum(int64(v))
			case 3:
				l, k, err := readUvarint(data[i:])
				if err != nil {
					return nil, err
				}
				i += k
				if l > uint64(len(data)-i) {
					return nil, fmt.Errorf("truncated blob")
				}
				blob := make([]byte, int(l))
				copy(blob, data[i:i+int(l)])
				i += int(l)
				m.which = chBlob(blob)
			case 4:
				l, k, err := readUvarint(data[i:])
				if err != nil {
					return nil, err
				}
				i += k
				if l > uint64(len(data)-i) {
					return nil, fmt.Errorf("truncated node")
				}
				nw, err := decodeNodePayload(data[i : i+int(l)])
				if err != nil {
					return nil, err
				}
				i += int(l)
				m.which = (*chNode)(nw)
			case 5:
				v, k, err := readUvarint(data[i:])
				if err != nil {
					return nil, err
				}
				i += k
				m.which = chFlag(v != 0)
			case 6:
				if len(data)-i < 4 {
					return nil, fmt.Errorf("truncated fixed32")
				}
				m.which = chCount(binary.LittleEndian.Uint32(data[i : i+4]))
				i += 4
			}
			continue
		}
		// Unknown number, or a known oneof number with the wrong wire type:
		// preserve the raw bytes and do not disturb the selected branch.
		k, err := skipValue(data[i:], num, wt)
		if err != nil {
			return nil, err
		}
		i += k
		raw := make([]byte, i-start)
		copy(raw, data[start:i])
		m.unknown = append(m.unknown, rawField{num: num, wt: wt, raw: raw})
	}
	return m, nil
}

func encodeChoiceMsg(m *choiceMsg) []byte {
	var b []byte
	switch x := m.which.(type) {
	case nil:
	case *chNode:
		if x != nil {
			b = putLen(b, 4, encodeNode((*node)(x)))
		}
	case chText:
		b = putLen(b, 1, []byte(x))
	case chNum:
		b = putTag(b, 2, 0)
		b = putUvarint(b, uint64(x))
	case chBlob:
		b = putLen(b, 3, x)
	case chFlag:
		b = putTag(b, 5, 0)
		if x {
			b = putUvarint(b, 1)
		} else {
			b = putUvarint(b, 0)
		}
	case chCount:
		b = putTag(b, 6, 5)
		var tmp [4]byte
		binary.LittleEndian.PutUint32(tmp[:], uint32(x))
		b = append(b, tmp[:]...)
	}
	for _, f := range m.unknown {
		b = append(b, f.raw...)
	}
	return b
}

func (m *choiceMsg) mergeFrom(src *choiceMsg) {
	if src == nil {
		return
	}
	if src.hasChoice() {
		m.which = cloneChoice(src.which)
	}
	for _, f := range src.unknown {
		m.unknown = append(m.unknown, rawField{num: f.num, wt: f.wt, raw: append([]byte(nil), f.raw...)})
	}
}

func equalUnknown(a, b []rawField) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].num != b[i].num || a[i].wt != b[i].wt || !bytes.Equal(a[i].raw, b[i].raw) {
			return false
		}
	}
	return true
}

func rawEquals(m *choiceMsg, data []byte) bool {
	return bytes.Equal(encodeChoiceMsg(m), data)
}

func main() {
	seed = readSeed()

	// 1. All six branches roundtrip.
	branches := []isChoice{
		chText("hello"),
		chNum(-42),
		chBlob([]byte{1, 2, 3}),
		&chNode{v: 7, s: "n"},
		chFlag(true),
		chCount(0xdeadbeef),
	}
	for i, br := range branches {
		m := &choiceMsg{which: br}
		w := encodeChoiceMsg(m)
		got, err := decodeChoiceMsg(w)
		ck(err == nil && equalChoice(got.which, br), fmt.Sprintf("branch %d roundtrip", i))
		ck(got.which != nil && choiceField(got.which) == choiceField(br), fmt.Sprintf("branch %d selection", i))
	}
	// Empty text/blob and zero values still represent a selected branch.
	for i, br := range []isChoice{chText(""), chBlob([]byte{}), chFlag(false), chCount(0)} {
		m := &choiceMsg{which: br}
		got, err := decodeChoiceMsg(encodeChoiceMsg(m))
		ck(err == nil && equalChoice(got.which, br) && got.hasChoice(), fmt.Sprintf("empty branch %d", i))
	}

	// 2. Typed-nil branch is treated as unset.
	mNil := &choiceMsg{which: (*chNode)(nil)}
	ck(!mNil.hasChoice(), "typed-nil node has no choice")
	ck(len(encodeChoiceMsg(mNil)) == 0, "typed-nil branch encodes nothing")
	// It also must not overwrite a merge destination.
	dst := &choiceMsg{which: chText("keep")}
	dst.mergeFrom(mNil)
	ck(equalChoice(dst.which, chText("keep")), "typed-nil merge does not overwrite")

	// 3. Golden bytes: text branch then unknown varint field 7.
	m := &choiceMsg{which: chText("hi")}
	m.unknown = []rawField{{num: 7, wt: 0, raw: []byte{0x38, 0x05}}}
	w := encodeChoiceMsg(m)
	ck(bytes.Equal(w, []byte{0x0a, 0x02, 'h', 'i', 0x38, 0x05}), "golden known + unknown bytes")

	// 4. Last known branch wins; other branch getters are absent.
	var lw []byte
	lw = putLen(lw, 1, []byte("first"))
	lw = putTag(lw, 2, 0)
	lw = putUvarint(lw, 5)
	lw = putLen(lw, 1, []byte("last"))
	last, err := decodeChoiceMsg(lw)
	ck(err == nil && equalChoice(last.which, chText("last")), "last known branch wins")
	ck(!equalChoice(last.which, chNum(5)), "superseded branch discarded")

	// 5. Unknown fields (numbers + groups) preserved byte-for-byte in order.
	var p []byte
	p = putLen(p, 1, []byte("known"))
	p = putTag(p, 7, 0)
	p = putUvarint(p, 300)
	p = putLen(p, 8, []byte("u"))
	p = putTag(p, 9, 1)
	p = append(p, 1, 2, 3, 4, 5, 6, 7, 8)
	p = putTag(p, 10, 3) // group
	p = putTag(p, 1, 0)
	p = putUvarint(p, 1)
	p = putTag(p, 10, 4)
	p = putTag(p, 11, 5)
	p = append(p, 9, 10, 11, 12)
	um, err := decodeChoiceMsg(p)
	ck(err == nil && equalChoice(um.which, chText("known")) && len(um.unknown) == 5, "unknown fields decoded")
	ck(bytes.Equal(encodeChoiceMsg(um), p), "unknown fields re-encode identically")
	for i := range p {
		p[i] = 0xEE
	}
	gcStress()
	ck(bytes.Equal(encodeChoiceMsg(um), []byte{0x0a, 0x05, 'k', 'n', 'o', 'w', 'n', 0x38, 0xac, 0x02, 0x42, 0x01, 'u', 0x49, 1, 2, 3, 4, 5, 6, 7, 8, 0x53, 0x08, 0x01, 0x54, 0x5d, 9, 10, 11, 12}), "unknown bytes independent of input buffer")

	// 6. Known number with wrong wire type: preserved and does not select/clear.
	var wrong []byte
	wrong = putTag(wrong, 1, 0) // field 1 is text (wire 2), sent as varint
	wrong = putUvarint(wrong, 9)
	wr, err := decodeChoiceMsg(wrong)
	ck(err == nil && !wr.hasChoice() && len(wr.unknown) == 1, "wrong-wire known field does not select")
	var withChoice []byte
	withChoice = putLen(withChoice, 1, []byte("stay"))
	withChoice = putTag(withChoice, 1, 0)
	withChoice = putUvarint(withChoice, 9)
	wc, err := decodeChoiceMsg(withChoice)
	ck(err == nil && equalChoice(wc.which, chText("stay")) && len(wc.unknown) == 1, "wrong-wire field does not clear branch")
	ck(rawEquals(wc, withChoice), "wrong-wire roundtrip stable")

	// 7. Merge semantics: branch replaces, unknown appends, deep copy.
	dm := &choiceMsg{which: chText("dst"), unknown: []rawField{{num: 20, wt: 0, raw: []byte{0xa0, 0x01, 0x01}}}}
	sm := &choiceMsg{which: chBlob([]byte{9, 9}), unknown: []rawField{{num: 21, wt: 0, raw: []byte{0xa8, 0x01, 0x02}}}}
	dm.mergeFrom(sm)
	ck(equalChoice(dm.which, chBlob([]byte{9, 9})) && len(dm.unknown) == 2, "merge branch and unknown")
	sm.which.(chBlob)[0] = 0
	sm.unknown[0].raw[2] = 0
	ck(equalChoice(dm.which, chBlob([]byte{9, 9})) && dm.unknown[1].raw[2] == 2, "merge deep-copies payloads")
	dm.mergeFrom(nil)
	ck(len(dm.unknown) == 2, "nil merge no-op")

	// 8. Clear choice keeps unknown; clear unknown keeps choice; reset clears both.
	cm := &choiceMsg{which: chText("t"), unknown: []rawField{{num: 7, wt: 0, raw: []byte{0x38, 0x01}}}}
	cm.ClearChoice()
	ck(!cm.hasChoice() && len(cm.unknown) == 1, "clear choice keeps unknown")
	cm.which = chNum(3)
	cm.ClearUnknown()
	ck(cm.hasChoice() && len(cm.unknown) == 0, "clear unknown keeps choice")
	cm.Reset()
	ck(!cm.hasChoice() && len(cm.unknown) == 0, "reset clears both")

	// 9. Error paths.
	bad := [][]byte{
		{0x0a, 0x05, 'x'},  // truncated text
		{0x12, 0x80},       // truncated varint
		{0x1a, 0x03, 0x01}, // truncated blob
		{0x22, 0x03, 0x08}, // truncated node varint
		{0x35, 0x01, 0x01}, // truncated fixed32
		{0x3b, 0x08, 0x01}, // unterminated group
		{0x3d, 1, 2, 3},    // truncated unknown fixed32
		{0x0e},             // wire type 6
		{0x0c},             // unexpected end group
	}
	for i, b := range bad {
		_, err := decodeChoiceMsg(b)
		ck(err != nil, fmt.Sprintf("bad payload %d rejected", i))
	}

	// 10. Seeded torture: random branch + unknown mix, GC and buffer poison.
	rounds := envInt("TORTURE_ROUNDS", 300)
	r := rand.New(rand.NewSource(seed))
	okRounds := 0
	for i := 0; i < rounds; i++ {
		msg := &choiceMsg{}
		switch r.Intn(6) {
		case 0:
			msg.which = chText(fmt.Sprintf("t%d", r.Intn(1000)))
		case 1:
			msg.which = chNum(r.Int63() - (1 << 40))
		case 2:
			b := make([]byte, r.Intn(4))
			for j := range b {
				b[j] = byte(r.Intn(256))
			}
			msg.which = chBlob(b)
		case 3:
			msg.which = &chNode{v: r.Int63() - (1 << 40), s: fmt.Sprintf("n%d", r.Intn(100))}
		case 4:
			msg.which = chFlag(r.Intn(2) == 0)
		default:
			msg.which = chCount(r.Uint32())
		}
		for j := 0; j < r.Intn(4); j++ {
			num := int32(7 + r.Intn(5))
			var raw []byte
			var wt int
			switch r.Intn(4) {
			case 0:
				wt = 0
				raw = putTag(raw, num, 0)
				raw = putUvarint(raw, r.Uint64())
			case 1:
				wt = 2
				raw = putLen(raw, num, []byte(fmt.Sprintf("u%d", r.Intn(50))))
			case 2:
				wt = 1
				raw = putTag(raw, num, 1)
				var tmp [8]byte
				binary.LittleEndian.PutUint64(tmp[:], r.Uint64())
				raw = append(raw, tmp[:]...)
			default:
				wt = 5
				raw = putTag(raw, num, 5)
				raw = append(raw, byte(r.Intn(256)), byte(r.Intn(256)), byte(r.Intn(256)), byte(r.Intn(256)))
			}
			msg.unknown = append(msg.unknown, rawField{num: num, wt: wt, raw: raw})
		}
		w := encodeChoiceMsg(msg)
		snapshot := append([]byte(nil), w...)
		got, err := decodeChoiceMsg(w)
		if err != nil {
			ckSeed(false, fmt.Sprintf("torture %d decode: %v", i, err))
			continue
		}
		if !equalChoice(got.which, msg.which) || !equalUnknown(got.unknown, msg.unknown) {
			ckSeed(false, fmt.Sprintf("torture %d state mismatch", i))
			continue
		}
		for j := range w {
			w[j] = 0x5A
		}
		if i%23 == 0 {
			gcStress()
		}
		if !bytes.Equal(encodeChoiceMsg(got), snapshot) {
			ckSeed(false, fmt.Sprintf("torture %d buffer alias", i))
			continue
		}
		okRounds++
	}
	ck(okRounds == rounds, "torture: all oneof/unknown rounds stable")

	finish("l10_oneof_unknown_wire")
}
