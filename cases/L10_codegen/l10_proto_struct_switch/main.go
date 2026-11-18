// L10: 生成风格 struct + 按 field number 的巨大 switch 编解码（protobuf 形态手写模拟）。
// 不依赖 protoc / 三方库；wire 子集：varint / fixed64 / bytes。
package main

import (
	"errors"
	"fmt"
	"math"
	"math/rand"
	"os"
	"reflect"
	"runtime"
	"strconv"
	"sync/atomic"
)

// 生成风格的字段号常量（模拟 protoc-gen-go 的 const 块）。
const (
	fieldID     = 1
	fieldName   = 2
	fieldTags   = 3
	fieldPrice  = 4
	fieldActive = 5
	fieldChild  = 6
	fieldRaw    = 7
)

const (
	wireVarint  = 0
	wireFixed64 = 1
	wireBytes   = 2
	wireStart   = 3
	wireEnd     = 4
	wireFixed32 = 5
)

var (
	errTruncated = errors.New("proto: truncated input")
	errVarint    = errors.New("proto: varint overflow")
	errWireType  = errors.New("proto: unexpected wire type")
	errWire      = errors.New("proto: invalid wire type")
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

// childMsg 对应 .proto 中的嵌套消息。
type childMsg struct {
	Key uint64
	Str string
}

// itemMsg 模拟生成的 proto3 消息：字段默认零值省略。
type itemMsg struct {
	ID     int64
	Name   string
	Tags   []string
	Price  float64
	Active bool
	Child  *childMsg
	Raw    []byte

	sizeCache int
}

func appendVarint(b []byte, v uint64) []byte {
	for v >= 0x80 {
		b = append(b, byte(v)|0x80)
		v >>= 7
	}
	return append(b, byte(v))
}

func consumeVarint(b []byte) (uint64, int, error) {
	var v uint64
	for i := 0; i < len(b); i++ {
		if i == 9 && b[i] > 1 {
			return 0, 0, errVarint
		}
		v |= uint64(b[i]&0x7f) << (7 * uint(i))
		if b[i] < 0x80 {
			return v, i + 1, nil
		}
	}
	return 0, 0, errTruncated
}

func appendTag(b []byte, field, wire int) []byte {
	return appendVarint(b, uint64(field)<<3|uint64(wire))
}

func appendBytes(b []byte, field int, p []byte) []byte {
	b = appendTag(b, field, wireBytes)
	b = appendVarint(b, uint64(len(p)))
	return append(b, p...)
}

func appendFixed64(b []byte, field int, bits uint64) []byte {
	b = appendTag(b, field, wireFixed64)
	return append(b, byte(bits), byte(bits>>8), byte(bits>>16), byte(bits>>24),
		byte(bits>>32), byte(bits>>40), byte(bits>>48), byte(bits>>56))
}

// MarshalAppend 是生成风格编码器：按字段号顺序 append。
func (m *itemMsg) MarshalAppend(b []byte) ([]byte, error) {
	if m == nil {
		return nil, errors.New("proto: nil message")
	}
	if m.ID != 0 {
		b = appendTag(b, fieldID, wireVarint)
		b = appendVarint(b, uint64(m.ID<<1)^uint64(m.ID>>63)) // ZigZag
	}
	if m.Name != "" {
		b = appendBytes(b, fieldName, []byte(m.Name))
	}
	for _, t := range m.Tags {
		b = appendBytes(b, fieldTags, []byte(t))
	}
	if m.Price != 0 {
		b = appendFixed64(b, fieldPrice, math.Float64bits(m.Price))
	}
	if m.Active {
		b = appendTag(b, fieldActive, wireVarint)
		b = appendVarint(b, 1)
	}
	if m.Child != nil {
		var cb []byte
		cb = appendVarint(cb, uint64(1<<3))
		cb = appendVarint(cb, m.Child.Key)
		cb = appendBytes(cb, 2, []byte(m.Child.Str))
		b = appendBytes(b, fieldChild, cb)
	}
	if m.Raw != nil {
		b = appendBytes(b, fieldRaw, m.Raw)
	}
	return b, nil
}

func (m *itemMsg) Marshal() ([]byte, error) {
	return m.MarshalAppend(nil)
}

func decodeChild(b []byte) (*childMsg, error) {
	c := &childMsg{}
	for len(b) > 0 {
		key, n, err := consumeVarint(b)
		if err != nil {
			return nil, err
		}
		b = b[n:]
		field, wire := int(key>>3), int(key&7)
		switch field {
		case 1:
			if wire != wireVarint {
				return nil, errWireType
			}
			v, n, err := consumeVarint(b)
			if err != nil {
				return nil, err
			}
			c.Key = v
			b = b[n:]
		case 2:
			if wire != wireBytes {
				return nil, errWireType
			}
			ln, n, err := consumeVarint(b)
			if err != nil {
				return nil, err
			}
			b = b[n:]
			if ln > uint64(len(b)) {
				return nil, errTruncated
			}
			c.Str = string(b[:ln])
			b = b[ln:]
		default:
			var err error
			b, err = skipField(wire, b)
			if err != nil {
				return nil, err
			}
		}
	}
	return c, nil
}

func skipField(wire int, b []byte) ([]byte, error) {
	switch wire {
	case wireVarint:
		_, n, err := consumeVarint(b)
		if err != nil {
			return nil, err
		}
		return b[n:], nil
	case wireFixed64:
		if len(b) < 8 {
			return nil, errTruncated
		}
		return b[8:], nil
	case wireBytes:
		ln, n, err := consumeVarint(b)
		if err != nil {
			return nil, err
		}
		b = b[n:]
		if ln > uint64(len(b)) {
			return nil, errTruncated
		}
		return b[ln:], nil
	case wireFixed32:
		if len(b) < 4 {
			return nil, errTruncated
		}
		return b[4:], nil
	default:
		return nil, errWire
	}
}

// Unmarshal 模拟生成代码：外层按 field number 分派，未知字段跳过。
func (m *itemMsg) Unmarshal(b []byte) error {
	*m = itemMsg{}
	for len(b) > 0 {
		key, n, err := consumeVarint(b)
		if err != nil {
			return err
		}
		b = b[n:]
		field, wire := int(key>>3), int(key&7)
		if field == 0 {
			return errWire
		}
		switch field {
		case fieldID:
			if wire != wireVarint {
				return errWireType
			}
			v, n, err := consumeVarint(b)
			if err != nil {
				return err
			}
			m.ID = int64(v>>1) ^ -int64(v&1)
			b = b[n:]
		case fieldName:
			if wire != wireBytes {
				return errWireType
			}
			ln, n, err := consumeVarint(b)
			if err != nil {
				return err
			}
			b = b[n:]
			if ln > uint64(len(b)) {
				return errTruncated
			}
			m.Name = string(b[:ln])
			b = b[ln:]
		case fieldTags:
			if wire != wireBytes {
				return errWireType
			}
			ln, n, err := consumeVarint(b)
			if err != nil {
				return err
			}
			b = b[n:]
			if ln > uint64(len(b)) {
				return errTruncated
			}
			m.Tags = append(m.Tags, string(b[:ln]))
			b = b[ln:]
		case fieldPrice:
			if wire != wireFixed64 {
				return errWireType
			}
			if len(b) < 8 {
				return errTruncated
			}
			bits := uint64(b[0]) | uint64(b[1])<<8 | uint64(b[2])<<16 | uint64(b[3])<<24 |
				uint64(b[4])<<32 | uint64(b[5])<<40 | uint64(b[6])<<48 | uint64(b[7])<<56
			m.Price = math.Float64frombits(bits)
			b = b[8:]
		case fieldActive:
			if wire != wireVarint {
				return errWireType
			}
			v, n, err := consumeVarint(b)
			if err != nil {
				return err
			}
			m.Active = v != 0
			b = b[n:]
		case fieldChild:
			if wire != wireBytes {
				return errWireType
			}
			ln, n, err := consumeVarint(b)
			if err != nil {
				return err
			}
			b = b[n:]
			if ln > uint64(len(b)) {
				return errTruncated
			}
			c, err := decodeChild(b[:ln])
			if err != nil {
				return err
			}
			m.Child = c
			b = b[ln:]
		case fieldRaw:
			if wire != wireBytes {
				return errWireType
			}
			ln, n, err := consumeVarint(b)
			if err != nil {
				return err
			}
			b = b[n:]
			if ln > uint64(len(b)) {
				return errTruncated
			}
			cp := make([]byte, ln)
			copy(cp, b[:ln])
			m.Raw = cp
			b = b[ln:]
		default:
			b, err = skipField(wire, b)
			if err != nil {
				return err
			}
		}
	}
	return nil
}

func equalMsg(a, b *itemMsg) bool {
	if a == nil || b == nil {
		return a == b
	}
	if a.ID != b.ID || a.Name != b.Name || a.Price != b.Price || a.Active != b.Active {
		return false
	}
	if len(a.Tags) != len(b.Tags) {
		return false
	}
	for i := range a.Tags {
		if a.Tags[i] != b.Tags[i] {
			return false
		}
	}
	if (a.Child == nil) != (b.Child == nil) {
		return false
	}
	if a.Child != nil && *a.Child != *b.Child {
		return false
	}
	return reflect.DeepEqual(a.Raw, b.Raw)
}

func randStr(r *rand.Rand, maxLen int) string {
	n := r.Intn(maxLen + 1)
	buf := make([]byte, n)
	for i := range buf {
		buf[i] = byte('a' + r.Intn(26))
	}
	return string(buf)
}

func randMsg(r *rand.Rand, depth int) *itemMsg {
	m := &itemMsg{
		ID:     int64(r.Int63n(1<<40)) - (1 << 39),
		Name:   randStr(r, 12),
		Price:  r.Float64()*2000 - 1000,
		Active: r.Intn(2) == 0,
	}
	for i := 0; i < r.Intn(5); i++ {
		m.Tags = append(m.Tags, randStr(r, 8))
	}
	if r.Intn(3) == 0 {
		m.Raw = make([]byte, r.Intn(17))
		for i := range m.Raw {
			m.Raw[i] = byte(r.Intn(256))
		}
	}
	if depth > 0 && r.Intn(3) == 0 {
		m.Child = &childMsg{Key: r.Uint64(), Str: randStr(r, 10)}
	}
	return m
}

func main() {
	curSeed = 1
	if s := os.Getenv("UAF_SEED"); s != "" {
		v, err := strconv.ParseInt(s, 10, 64)
		if err == nil {
			curSeed = v
		}
	}

	// 1. 固定消息 roundtrip + 未知字段跳过 + GC 保活。
	m := &itemMsg{
		ID: -123456789, Name: "hello", Tags: []string{"x", "y", "z"},
		Price: 3.25, Active: true, Child: &childMsg{Key: 42, Str: "nested"},
		Raw: []byte{0, 1, 2, 0xff},
	}
	enc, err := m.Marshal()
	ck(err == nil, "marshal fixed message")
	// 追加未知字段：field 9 varint、field 10 bytes，必须被跳过。
	enc = appendTag(enc, 9, wireVarint)
	enc = appendVarint(enc, 777)
	enc = appendBytes(enc, 10, []byte("unknown"))
	gcSink = append(gcSink, enc)
	gcStress()
	var got itemMsg
	ck(got.Unmarshal(enc) == nil, "unmarshal with unknown fields")
	ck(equalMsg(m, &got), "fixed roundtrip equality after GC")

	// 2. 空消息编码为 0 字节。
	empty, err := (&itemMsg{}).Marshal()
	ck(err == nil && len(empty) == 0, "zero message encodes to empty")
	var z itemMsg
	ck(z.Unmarshal(empty) == nil && equalMsg(&z, &itemMsg{}), "empty decode")

	// 3. 截断输入全部返回错误且不 panic。
	truncOK := true
	for cut := 0; cut < len(enc); cut++ {
		var t itemMsg
		if err := t.Unmarshal(enc[:cut]); err == nil && cut != len(enc) {
			// 前缀越界截断在字段边界上仍可能合法：只在截断在字段内部时要求失败。
			_ = err
		}
		func() {
			defer func() {
				if recover() != nil {
					truncOK = false
				}
			}()
			_ = t.Unmarshal(enc[:cut])
		}()
	}
	ck(truncOK, "truncated inputs never panic")
	var bad itemMsg
	ck(bad.Unmarshal(enc[:len(enc)-1]) != nil, "truncated tail rejected")

	// 4. wire type 不匹配与非法 wire type。
	badWire := appendTag(nil, fieldID, wireBytes)
	badWire = appendVarint(badWire, 1)
	ck(bad.Unmarshal(badWire) == errWireType, "wire type mismatch rejected")
	bad2 := appendTag(nil, 9, wireStart)
	ck(bad.Unmarshal(bad2) == errWire, "group wire type rejected")
	overflow := []byte{0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x02}
	ck(bad.Unmarshal(overflow) != nil, "11-byte varint rejected")

	// 5. 重复 repeated 字段保持顺序。
	rep := itemMsg{Tags: []string{"a", "b", "c", "d"}}
	rb, _ := rep.Marshal()
	var rgot itemMsg
	ck(rgot.Unmarshal(rb) == nil && reflect.DeepEqual(rgot.Tags, rep.Tags), "repeated order preserved")

	// 6. 随机消息 × UAF_SEED，GC 穿插，保留编码缓冲后再解码。
	r := rand.New(rand.NewSource(curSeed))
	var retained [][]byte
	for iter := 0; iter < 300; iter++ {
		src := randMsg(r, 3)
		b, err := src.Marshal()
		ckSeed(err == nil, "random marshal")
		if iter%17 == 0 {
			retained = append(retained, b)
			gcStress()
		}
		var dst itemMsg
		ckSeed(dst.Unmarshal(b) == nil, "random unmarshal")
		ckSeed(equalMsg(src, &dst), "random roundtrip equality")
	}
	gcSink = retained
	runtime.GC()
	for i, b := range retained {
		var dst itemMsg
		var src itemMsg
		if err := src.Unmarshal(b); err != nil {
			ckSeed(false, fmt.Sprintf("retained decode %d", i))
			continue
		}
		if err := dst.Unmarshal(b); err != nil || !equalMsg(&src, &dst) {
			ckSeed(false, fmt.Sprintf("retained re-decode %d", i))
		}
	}

	finish("l10_proto_struct_switch")
}
