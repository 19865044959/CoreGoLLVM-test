// L10: 生成风格 oneof 联合体：接口 + 多个包装类型 + 类型 switch 编解码。
// 同时覆盖 Get/Has/Clear、typed-nil、last-one-wins、GC 后载荷存活。
package main

import (
	"bytes"
	"errors"
	"fmt"
	"math/rand"
	"os"
	"runtime"
	"strconv"
	"sync/atomic"
)

var (
	failed  int32
	gcSink  [][]byte
	curSeed int64
)

func ck(ok bool, msg string) {
	if !ok {
		atomic.AddInt32(&failed, 1)
		fmt.Println("FAIL:", msg)
	}
}

func ckSeed(ok bool, msg string) {
	if !ok {
		atomic.AddInt32(&failed, 1)
		fmt.Printf("FAIL: %s (seed=%d)\n", msg, curSeed)
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

// 以下为生成风格 oneof 包装类型。
type isChoice interface{ isChoice() }

type choiceText struct{ Value string }
type choiceNum struct{ Value int64 }
type choiceBlob struct{ Value []byte }
type choiceNode struct{ Value *payload }

func (*choiceText) isChoice() {}
func (*choiceNum) isChoice()  {}
func (*choiceBlob) isChoice() {}
func (*choiceNode) isChoice() {}

type payload struct {
	Label string
	Data  []byte
}

type oneMsg struct {
	ID    uint32
	Which isChoice
}

func (m *oneMsg) GetText() string {
	if x, ok := m.Which.(*choiceText); ok && x != nil {
		return x.Value
	}
	return ""
}
func (m *oneMsg) GetNum() int64 {
	if x, ok := m.Which.(*choiceNum); ok && x != nil {
		return x.Value
	}
	return 0
}
func (m *oneMsg) GetBlob() []byte {
	if x, ok := m.Which.(*choiceBlob); ok && x != nil {
		return x.Value
	}
	return nil
}
func (m *oneMsg) GetNode() *payload {
	if x, ok := m.Which.(*choiceNode); ok && x != nil {
		return x.Value
	}
	return nil
}
func (m *oneMsg) HasChoice() bool {
	if m.Which == nil {
		return false
	}
	// typed-nil 视为未设置，与生成代码的 presence 语义一致。
	switch x := m.Which.(type) {
	case *choiceText:
		return x != nil
	case *choiceNum:
		return x != nil
	case *choiceBlob:
		return x != nil
	case *choiceNode:
		return x != nil
	}
	return false
}
func (m *oneMsg) ClearChoice() { m.Which = nil }

// WhichOneof 返回联合体分支名。
func (m *oneMsg) WhichOneof() string {
	switch x := m.Which.(type) {
	case nil:
		return ""
	case *choiceText:
		if x == nil {
			return ""
		}
		return "text"
	case *choiceNum:
		if x == nil {
			return ""
		}
		return "num"
	case *choiceBlob:
		if x == nil {
			return ""
		}
		return "blob"
	case *choiceNode:
		if x == nil {
			return ""
		}
		return "node"
	}
	return ""
}

func appendVarint(b []byte, v uint64) []byte {
	for v >= 0x80 {
		b = append(b, byte(v)|0x80)
		v >>= 7
	}
	return append(b, byte(v))
}

func readVarint(b []byte) (uint64, int, error) {
	var v uint64
	for i := 0; i < len(b); i++ {
		v |= uint64(b[i]&0x7f) << (7 * uint(i))
		if b[i] < 0x80 {
			return v, i + 1, nil
		}
	}
	return 0, 0, errors.New("truncated varint")
}

var errOneof = errors.New("oneof: malformed input")

// Marshal 生成风格：先编码普通字段，再按 oneof 实际类型编码对应字段号。
func (m *oneMsg) Marshal() ([]byte, error) {
	var b []byte
	if m.ID != 0 {
		b = appendVarint(b, uint64(1<<3))
		b = appendVarint(b, uint64(m.ID))
	}
	switch x := m.Which.(type) {
	case nil:
		// 未设置
	case *choiceText:
		if x != nil {
			b = appendVarint(b, uint64(2<<3|2))
			b = appendVarint(b, uint64(len(x.Value)))
			b = append(b, x.Value...)
		}
	case *choiceNum:
		if x != nil {
			b = appendVarint(b, uint64(3<<3))
			b = appendVarint(b, uint64(x.Value<<1)^uint64(x.Value>>63))
		}
	case *choiceBlob:
		if x != nil {
			b = appendVarint(b, uint64(4<<3|2))
			b = appendVarint(b, uint64(len(x.Value)))
			b = append(b, x.Value...)
		}
	case *choiceNode:
		if x != nil && x.Value != nil {
			var nb []byte
			nb = appendVarint(nb, uint64(len(x.Value.Label)))
			nb = append(nb, x.Value.Label...)
			nb = appendVarint(nb, uint64(len(x.Value.Data)))
			nb = append(nb, x.Value.Data...)
			b = appendVarint(b, uint64(5<<3|2))
			b = appendVarint(b, uint64(len(nb)))
			b = append(b, nb...)
		}
	}
	return b, nil
}

func decodeNode(b []byte) (*payload, error) {
	p := &payload{}
	ln, n, err := readVarint(b)
	if err != nil || ln > uint64(len(b)-n) {
		return nil, errOneof
	}
	b = b[n:]
	p.Label = string(b[:ln])
	b = b[ln:]
	if len(b) == 0 {
		p.Data = []byte{}
		return p, nil
	}
	ln, n, err = readVarint(b)
	if err != nil || ln > uint64(len(b)-n) {
		return nil, errOneof
	}
	b = b[n:]
	p.Data = append([]byte(nil), b[:ln]...)
	return p, nil
}

// Unmarshal 生成风格：同一个 oneof 多个字段出现时 last-one-wins。
func (m *oneMsg) Unmarshal(b []byte) error {
	*m = oneMsg{}
	for len(b) > 0 {
		key, n, err := readVarint(b)
		if err != nil {
			return errOneof
		}
		b = b[n:]
		field, wire := int(key>>3), int(key&7)
		switch field {
		case 1:
			if wire != 0 {
				return errOneof
			}
			v, n, err := readVarint(b)
			if err != nil {
				return errOneof
			}
			m.ID = uint32(v)
			b = b[n:]
		case 2, 4, 5:
			if wire != 2 {
				return errOneof
			}
			ln, n, err := readVarint(b)
			if err != nil || ln > uint64(len(b)-n) {
				return errOneof
			}
			val := b[n : n+int(ln)]
			b = b[n+int(ln):]
			switch field {
			case 2:
				m.Which = &choiceText{Value: string(val)}
			case 4:
				cp := make([]byte, len(val))
				copy(cp, val)
				m.Which = &choiceBlob{Value: cp}
			case 5:
				p, err := decodeNode(val)
				if err != nil {
					return err
				}
				m.Which = &choiceNode{Value: p}
			}
		case 3:
			if wire != 0 {
				return errOneof
			}
			v, n, err := readVarint(b)
			if err != nil {
				return errOneof
			}
			m.Which = &choiceNum{Value: int64(v>>1) ^ -int64(v&1)}
			b = b[n:]
		default:
			return errOneof
		}
	}
	return nil
}

func equalMsg(a, b *oneMsg) bool {
	if a.ID != b.ID || a.WhichOneof() != b.WhichOneof() {
		return false
	}
	switch x := a.Which.(type) {
	case *choiceText:
		return b.GetText() == x.Value
	case *choiceNum:
		return b.GetNum() == x.Value
	case *choiceBlob:
		return bytes.Equal(b.GetBlob(), x.Value)
	case *choiceNode:
		y := b.GetNode()
		return y != nil && y.Label == x.Value.Label && bytes.Equal(y.Data, x.Value.Data)
	}
	return true
}

func randPayload(r *rand.Rand) *payload {
	data := make([]byte, r.Intn(24))
	for i := range data {
		data[i] = byte(r.Intn(256))
	}
	return &payload{Label: fmt.Sprintf("n%d", r.Intn(1000)), Data: data}
}

func main() {
	curSeed = 1
	if s := os.Getenv("UAF_SEED"); s != "" {
		if v, err := strconv.ParseInt(s, 10, 64); err == nil {
			curSeed = v
		}
	}

	// 1. 每个分支的 Get 语义与类型 switch 分派。
	base := oneMsg{ID: 7, Which: &choiceText{Value: "hi"}}
	ck(base.GetText() == "hi" && base.GetNum() == 0 && base.GetBlob() == nil && base.GetNode() == nil, "text getters")
	ck(base.HasChoice() && base.WhichOneof() == "text", "text presence")

	blob := []byte{1, 2, 3, 4}
	node := &payload{Label: "deep", Data: []byte{9, 8, 7}}
	cases := []*oneMsg{
		{ID: 1, Which: &choiceNum{Value: -99}},
		{ID: 2, Which: &choiceBlob{Value: blob}},
		{ID: 3, Which: &choiceNode{Value: node}},
	}
	for i, m := range cases {
		enc, err := m.Marshal()
		ck(err == nil, fmt.Sprintf("marshal case %d", i))
		gcSink = append(gcSink, enc)
		gcStress()
		var got oneMsg
		ck(got.Unmarshal(enc) == nil, fmt.Sprintf("unmarshal case %d", i))
		ck(equalMsg(m, &got), fmt.Sprintf("roundtrip case %d", i))
	}
	// 载荷在 GC 后仍可从原对象读取。
	gcStress()
	ck(node.Label == "deep" && bytes.Equal(node.Data, []byte{9, 8, 7}), "node payload survives GC")
	ck(bytes.Equal(blob, []byte{1, 2, 3, 4}), "blob payload survives GC")

	// 2. 未设置与 Clear。
	var unset oneMsg
	ck(!unset.HasChoice() && unset.WhichOneof() == "", "unset oneof")
	uenc, _ := unset.Marshal()
	ck(len(uenc) == 0, "unset encodes empty")
	base.ClearChoice()
	ck(!base.HasChoice(), "clear oneof")

	// 3. typed-nil 包装：Get 安全、Has 为 false、编码为空。
	var typedNil *choiceText
	tm := oneMsg{Which: typedNil}
	ck(!tm.HasChoice(), "typed-nil has=false")
	ck(tm.GetText() == "" && tm.WhichOneof() == "", "typed-nil getters safe")
	tenc, err := tm.Marshal()
	ck(err == nil && len(tenc) == 0, "typed-nil encodes empty")

	// 4. last-one-wins：连续两个 oneof 字段，后者生效。
	var lw []byte
	lw = appendVarint(lw, uint64(2<<3|2))
	lw = appendVarint(lw, 1)
	lw = append(lw, 'A')
	lw = appendVarint(lw, uint64(3<<3))
	lw = appendVarint(lw, uint64(21<<1))
	var lwGot oneMsg
	ck(lwGot.Unmarshal(lw) == nil, "last-one-wins decode")
	ck(lwGot.WhichOneof() == "num" && lwGot.GetNum() == 21, "last-one-wins result")

	// 5. nil 与空 blob 的 presence 语义。
	emptyBlob := oneMsg{Which: &choiceBlob{Value: []byte{}}}
	eb, _ := emptyBlob.Marshal()
	var ebGot oneMsg
	ck(ebGot.Unmarshal(eb) == nil, "empty blob decode")
	ck(ebGot.WhichOneof() == "blob" && ebGot.GetBlob() != nil && len(ebGot.GetBlob()) == 0, "empty blob presence")

	// 6. 畸形输入：截断、错误 wire、未知字段号。
	good := oneMsg{ID: 9, Which: &choiceNode{Value: node}}
	gb, _ := good.Marshal()
	ck((&oneMsg{}).Unmarshal(gb[:len(gb)-1]) != nil, "truncated oneof rejected")
	badWire := appendVarint(nil, uint64(3<<3|2))
	badWire = appendVarint(badWire, 1)
	ck((&oneMsg{}).Unmarshal(badWire) != nil, "bad wire rejected")
	unknown := appendVarint(nil, uint64(9<<3))
	unknown = appendVarint(unknown, 1)
	ck((&oneMsg{}).Unmarshal(unknown) != nil, "unknown field rejected")

	// 7. 随机分支 × UAF_SEED，GC 穿插；节点/字节载荷跨 GC 校验。
	r := rand.New(rand.NewSource(curSeed))
	var kept []*oneMsg
	for iter := 0; iter < 800; iter++ {
		var m oneMsg
		m.ID = uint32(r.Intn(1 << 20))
		switch r.Intn(5) {
		case 0:
			m.Which = nil
		case 1:
			m.Which = &choiceText{Value: fmt.Sprintf("t-%d", r.Intn(1<<16))}
		case 2:
			m.Which = &choiceNum{Value: r.Int63n(1<<40) - (1 << 39)}
		case 3:
			data := make([]byte, r.Intn(40))
			for i := range data {
				data[i] = byte(r.Intn(256))
			}
			m.Which = &choiceBlob{Value: data}
		case 4:
			m.Which = &choiceNode{Value: randPayload(r)}
		}
		enc, err := m.Marshal()
		ckSeed(err == nil, "random marshal")
		if iter%29 == 0 {
			kept = append(kept, &m)
			gcSink = append(gcSink, enc)
			gcStress()
		}
		var got oneMsg
		ckSeed(got.Unmarshal(enc) == nil, "random unmarshal")
		ckSeed(equalMsg(&m, &got), "random roundtrip")
	}
	gcStress()
	for i, m := range kept {
		enc, err := m.Marshal()
		if err != nil {
			ckSeed(false, fmt.Sprintf("kept marshal %d", i))
			continue
		}
		var got oneMsg
		if err := got.Unmarshal(enc); err != nil || !equalMsg(m, &got) {
			ckSeed(false, fmt.Sprintf("kept roundtrip %d", i))
		}
	}

	finish("l10_oneof_union")
}
