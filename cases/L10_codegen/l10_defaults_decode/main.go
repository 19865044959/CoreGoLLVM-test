// L10: 生成风格默认值解码：默认值表构建、缺省字段填充、显式零值/空 bytes
// 保持 present、嵌套消息不自动实例化、merge/reset 与默认值隔离、GC。
package main

import (
	"bytes"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"math/rand"
	"os"
	"runtime"
	"sort"
	"strconv"
	"strings"
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

// ---- wire ----

var (
	errTruncated   = errors.New("truncated")
	errOverflow    = errors.New("varint overflow")
	errFieldNumber = errors.New("invalid field number")
	errWireType    = errors.New("wrong wire type")
	errGroup       = errors.New("unexpected group")
)

func appendUvarint(b []byte, v uint64) []byte {
	for v >= 0x80 {
		b = append(b, byte(v)|0x80)
		v >>= 7
	}
	return append(b, byte(v))
}

func appendTag(b []byte, num int32, wt int) []byte {
	return appendUvarint(b, uint64(num)<<3|uint64(wt))
}

func appendBytesField(b []byte, num int32, v []byte) []byte {
	b = appendTag(b, num, 2)
	b = appendUvarint(b, uint64(len(v)))
	return append(b, v...)
}

func consumeUvarint(b []byte) (uint64, int, error) {
	var x uint64
	for i := 0; i < len(b); i++ {
		if i == 9 && b[i] > 1 {
			return 0, 0, errOverflow
		}
		x |= uint64(b[i]&0x7f) << (7 * uint(i))
		if b[i] < 0x80 {
			return x, i + 1, nil
		}
	}
	return 0, 0, errTruncated
}

func consumeTag(b []byte) (num int32, wt, n int, err error) {
	v, n, err := consumeUvarint(b)
	if err != nil {
		return 0, 0, 0, err
	}
	if v>>3 == 0 || v>>3 > 0x7fffffff {
		return 0, 0, 0, errFieldNumber
	}
	num = int32(v >> 3)
	wt = int(v & 7)
	if wt == 6 || wt == 7 {
		return 0, 0, 0, errWireType
	}
	return num, wt, n, nil
}

func skipValue(b []byte, num int32, wt int) (int, error) {
	switch wt {
	case 0:
		_, n, err := consumeUvarint(b)
		return n, err
	case 1:
		if len(b) < 8 {
			return 0, errTruncated
		}
		return 8, nil
	case 5:
		if len(b) < 4 {
			return 0, errTruncated
		}
		return 4, nil
	case 2:
		l, n, err := consumeUvarint(b)
		if err != nil {
			return 0, err
		}
		if l > uint64(len(b)-n) {
			return 0, errTruncated
		}
		return n + int(l), nil
	case 3, 4:
		return 0, errGroup
	default:
		return 0, errWireType
	}
}

func bytesPayload(v []byte) ([]byte, error) {
	l, n, err := consumeUvarint(v)
	if err != nil {
		return nil, err
	}
	if l > uint64(len(v)-n) {
		return nil, errTruncated
	}
	return v[n : n+int(l)], nil
}

// ---- 消息与默认值表 ----

type mood int32

const (
	moodUnknown mood = 0
	moodHappy   mood = 2
	moodSad     mood = 3
)

type child struct {
	n       int32
	hasN    bool
	unknown [][]byte
}

type defaultsMsg struct {
	b        bool
	i32      int32
	i64      int64
	f64      float64
	s        string
	by       []byte
	e        mood
	child    *child
	rep      []int32
	mp       map[string]int32
	presence uint16
}

func (m *defaultsMsg) has(num int32) bool { return m.presence&(1<<uint(num)) != 0 }
func (m *defaultsMsg) mark(num int32)     { m.presence |= 1 << uint(num) }

func (m *defaultsMsg) reset() { *m = defaultsMsg{} }

func (m *defaultsMsg) getBytes() []byte {
	return append([]byte(nil), m.by...)
}

type defaultEntry struct {
	num  int32
	kind string
	vb   bool
	vi   int64
	vf   float64
	vs   string
	vby  []byte
}

func parseDefaultSpec(line string) (defaultEntry, error) {
	var d defaultEntry
	parts := strings.Split(line, "|")
	if len(parts) != 3 {
		return d, fmt.Errorf("bad spec line %q", line)
	}
	n, err := strconv.ParseInt(strings.TrimSpace(parts[0]), 10, 32)
	if err != nil || n <= 0 {
		return d, fmt.Errorf("bad field number %q", parts[0])
	}
	d.num = int32(n)
	d.kind = strings.TrimSpace(parts[1])
	val := parts[2]
	switch d.kind {
	case "bool":
		b, err := strconv.ParseBool(val)
		if err != nil {
			return d, err
		}
		d.vb = b
	case "int32", "int64":
		v, err := strconv.ParseInt(val, 10, 64)
		if err != nil {
			return d, err
		}
		if d.kind == "int32" && (v < math.MinInt32 || v > math.MaxInt32) {
			return d, errors.New("int32 out of range")
		}
		d.vi = v
	case "double":
		f, err := strconv.ParseFloat(val, 64)
		if err != nil {
			return d, err
		}
		d.vf = f
	case "string":
		raw, err := hex.DecodeString(val)
		if err != nil {
			return d, err
		}
		d.vs = string(raw)
	case "bytes":
		raw, err := hex.DecodeString(val)
		if err != nil {
			return d, err
		}
		d.vby = raw
	case "enum":
		v, err := strconv.ParseInt(val, 10, 32)
		if err != nil {
			return d, err
		}
		if mood(v) != moodUnknown && mood(v) != moodHappy && mood(v) != moodSad {
			return d, fmt.Errorf("enum default %d not defined", v)
		}
		d.vi = v
	default:
		return d, fmt.Errorf("unknown default kind %q", d.kind)
	}
	return d, nil
}

func buildDefaults(spec []string) ([]defaultEntry, error) {
	var out []defaultEntry
	seen := make(map[int32]struct{}, len(spec))
	for _, line := range spec {
		d, err := parseDefaultSpec(line)
		if err != nil {
			return nil, err
		}
		if _, ok := seen[d.num]; ok {
			return nil, fmt.Errorf("duplicate default for field %d", d.num)
		}
		seen[d.num] = struct{}{}
		out = append(out, d)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].num < out[j].num })
	return out, nil
}

func (m *defaultsMsg) applyDefaults(tbl []defaultEntry) {
	for _, d := range tbl {
		if m.has(d.num) {
			continue
		}
		switch d.kind {
		case "bool":
			m.b = d.vb
		case "int32":
			m.i32 = int32(d.vi)
		case "int64":
			m.i64 = d.vi
		case "double":
			m.f64 = d.vf
		case "string":
			m.s = d.vs
		case "bytes":
			m.by = append([]byte(nil), d.vby...)
		case "enum":
			m.e = mood(int32(d.vi))
		}
	}
}

// ---- 编解码 ----

func decodeChild(payload []byte) (*child, error) {
	ch := &child{}
	for len(payload) > 0 {
		num, wt, tn, err := consumeTag(payload)
		if err != nil {
			return nil, err
		}
		payload = payload[tn:]
		vn, err := skipValue(payload, num, wt)
		if err != nil {
			return nil, err
		}
		val := payload[:vn]
		payload = payload[vn:]
		if num == 1 {
			if wt != 0 {
				return nil, errWireType
			}
			x, _, err := consumeUvarint(val)
			if err != nil {
				return nil, err
			}
			ch.n = int32(x)
			ch.hasN = true
			continue
		}
		ch.unknown = append(ch.unknown, append([]byte(nil), val...))
	}
	if !ch.hasN {
		ch.n = 42 // 嵌套消息自身的默认值
	}
	return ch, nil
}

func decodeMsg(b []byte, m *defaultsMsg, merge bool, tbl []defaultEntry) error {
	if !merge {
		m.reset()
	}
	for len(b) > 0 {
		num, wt, tn, err := consumeTag(b)
		if err != nil {
			return err
		}
		b = b[tn:]
		vn, err := skipValue(b, num, wt)
		if err != nil {
			return err
		}
		val := b[:vn]
		b = b[vn:]
		switch num {
		case 1:
			if wt != 0 {
				return errWireType
			}
			x, _, err := consumeUvarint(val)
			if err != nil {
				return err
			}
			m.b = x != 0
			m.mark(1)
		case 2:
			if wt != 0 {
				return errWireType
			}
			x, _, err := consumeUvarint(val)
			if err != nil {
				return err
			}
			m.i32 = int32(x)
			m.mark(2)
		case 3:
			if wt != 0 {
				return errWireType
			}
			x, _, err := consumeUvarint(val)
			if err != nil {
				return err
			}
			m.i64 = int64(x)
			m.mark(3)
		case 4:
			if wt != 1 || len(val) != 8 {
				return errWireType
			}
			m.f64 = math.Float64frombits(binary.LittleEndian.Uint64(val))
			m.mark(4)
		case 5:
			if wt != 2 {
				return errWireType
			}
			p, err := bytesPayload(val)
			if err != nil {
				return err
			}
			m.s = string(p)
			m.mark(5)
		case 6:
			if wt != 2 {
				return errWireType
			}
			p, err := bytesPayload(val)
			if err != nil {
				return err
			}
			m.by = make([]byte, len(p))
			copy(m.by, p)
			m.mark(6)
		case 7:
			if wt != 0 {
				return errWireType
			}
			x, _, err := consumeUvarint(val)
			if err != nil {
				return err
			}
			m.e = mood(int32(x))
			m.mark(7)
		case 8:
			if wt != 2 {
				return errWireType
			}
			p, err := bytesPayload(val)
			if err != nil {
				return err
			}
			ch, err := decodeChild(p)
			if err != nil {
				return err
			}
			m.child = ch
			m.mark(8)
		case 9:
			switch wt {
			case 0:
				x, _, err := consumeUvarint(val)
				if err != nil {
					return err
				}
				m.rep = append(m.rep, int32(x))
			case 2:
				p, err := bytesPayload(val)
				if err != nil {
					return err
				}
				for len(p) > 0 {
					x, n, err := consumeUvarint(p)
					if err != nil {
						return err
					}
					p = p[n:]
					m.rep = append(m.rep, int32(x))
				}
			default:
				return errWireType
			}
		case 10:
			if wt != 2 {
				return errWireType
			}
			p, err := bytesPayload(val)
			if err != nil {
				return err
			}
			if m.mp == nil {
				m.mp = make(map[string]int32)
			}
			var k string
			var v int32
			for len(p) > 0 {
				n2, wt2, tn2, err := consumeTag(p)
				if err != nil {
					return err
				}
				p = p[tn2:]
				vn2, err := skipValue(p, n2, wt2)
				if err != nil {
					return err
				}
				val2 := p[:vn2]
				p = p[vn2:]
				switch n2 {
				case 1:
					if wt2 != 2 {
						return errWireType
					}
					kp, err := bytesPayload(val2)
					if err != nil {
						return err
					}
					k = string(kp)
				case 2:
					if wt2 != 0 {
						return errWireType
					}
					x, _, err := consumeUvarint(val2)
					if err != nil {
						return err
					}
					v = int32(x)
				}
			}
			m.mp[k] = v
		default:
			// 未知字段跳过。
		}
	}
	m.applyDefaults(tbl)
	return nil
}

func encodeMsg(m *defaultsMsg) []byte {
	var b []byte
	if m.has(1) {
		v := uint64(0)
		if m.b {
			v = 1
		}
		b = appendTag(b, 1, 0)
		b = appendUvarint(b, v)
	}
	if m.has(2) {
		b = appendTag(b, 2, 0)
		b = appendUvarint(b, uint64(int64(m.i32)))
	}
	if m.has(3) {
		b = appendTag(b, 3, 0)
		b = appendUvarint(b, uint64(m.i64))
	}
	if m.has(4) {
		b = appendTag(b, 4, 1)
		var buf [8]byte
		binary.LittleEndian.PutUint64(buf[:], math.Float64bits(m.f64))
		b = append(b, buf[:]...)
	}
	if m.has(5) {
		b = appendBytesField(b, 5, []byte(m.s))
	}
	if m.has(6) {
		b = appendBytesField(b, 6, m.by)
	}
	if m.has(7) {
		b = appendTag(b, 7, 0)
		b = appendUvarint(b, uint64(int64(m.e)))
	}
	if m.has(8) && m.child != nil {
		var cb []byte
		if m.child.hasN {
			cb = appendTag(cb, 1, 0)
			cb = appendUvarint(cb, uint64(int64(m.child.n)))
		}
		b = appendBytesField(b, 8, cb)
	}
	if len(m.rep) > 0 {
		var packed []byte
		for _, v := range m.rep {
			packed = appendUvarint(packed, uint64(int64(v)))
		}
		b = appendBytesField(b, 9, packed)
	}
	if len(m.mp) > 0 {
		keys := make([]string, 0, len(m.mp))
		for k := range m.mp {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			var entry []byte
			entry = appendBytesField(entry, 1, []byte(k))
			entry = appendTag(entry, 2, 0)
			entry = appendUvarint(entry, uint64(int64(m.mp[k])))
			b = appendBytesField(b, 10, entry)
		}
	}
	return b
}

// ---- 测试 ----

func defaultTable() ([]defaultEntry, error) {
	return buildDefaults([]string{
		"1|bool|true",
		"2|int32|-7",
		"3|int64|1099511627776",
		"4|double|3.141592653589793",
		"5|string|68c3a90a00", // "hé\n\x00"
		"6|bytes|deadbeef000a",
		"7|enum|2",
	})
}

func expectBuildError(name string, spec []string) {
	_, err := buildDefaults(spec)
	ck(err != nil, "expected default table error: "+name)
}

func expectDecodeError(name string, b []byte, tbl []defaultEntry) {
	m := &defaultsMsg{}
	err := decodeMsg(b, m, false, tbl)
	ck(err != nil, "expected decode error: "+name)
}

func main() {
	seed := int64(1)
	if v := os.Getenv("UAF_SEED"); v != "" {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil {
			seed = n
		}
	}
	curSeed = seed

	tbl, err := defaultTable()
	ck(err == nil, "build default table")
	ck(len(tbl) == 7 && tbl[0].num == 1 && tbl[6].num == 7, "default table sorted")

	// 1. 空消息：全部取默认值但 Has=false。
	m := &defaultsMsg{}
	ck(decodeMsg(nil, m, false, tbl) == nil, "decode empty")
	ck(!m.has(1) && m.b && !m.has(2) && m.i32 == -7, "bool/int32 defaults")
	ck(!m.has(3) && m.i64 == 1<<40, "int64 default")
	ck(!m.has(4) && m.f64 == math.Pi, "double default")
	ck(!m.has(5) && m.s == "hé\n\x00" && len(m.s) == 5, "string default bytes")
	ck(!m.has(6) && bytes.Equal(m.by, []byte{0xde, 0xad, 0xbe, 0xef, 0x00, 0x0a}), "bytes default")
	ck(!m.has(7) && m.e == moodHappy, "enum default")
	ck(m.child == nil && m.rep == nil && m.mp == nil, "message/repeated/map have no defaults")

	// 2. 序列化只写 present 字段，默认值不落盘。
	ck(len(encodeMsg(m)) == 0, "defaults not marshaled when absent")

	// 3. 显式零值/空 bytes/空字符串保持 present，不被默认覆盖。
	z := &defaultsMsg{}
	z.b, z.i32, z.i64, z.f64, z.s, z.e = false, 0, 0, 0, "", moodUnknown
	z.by = nil
	for n := int32(1); n <= 7; n++ {
		z.mark(n)
	}
	z.by = []byte{}
	zb := encodeMsg(z)
	zdec := &defaultsMsg{}
	ck(decodeMsg(zb, zdec, false, tbl) == nil, "decode explicit zero")
	for n := int32(1); n <= 7; n++ {
		if !zdec.has(n) {
			ck(false, "explicit field must stay present")
		}
	}
	ck(!zdec.b && zdec.i32 == 0 && zdec.i64 == 0 && zdec.f64 == 0 && zdec.s == "" && zdec.e == moodUnknown, "explicit zero values")
	ck(zdec.by != nil && len(zdec.by) == 0, "explicit empty bytes non-nil")
	ck(len(zb) > 0, "explicit fields marshaled")

	// 4. 显式非零值与未知枚举号。
	m2 := &defaultsMsg{}
	m2.b, m2.i32, m2.i64, m2.f64 = true, 99, -1<<40, 2.5
	m2.s = "set"
	m2.by = []byte{1, 2, 3}
	m2.e = mood(99)
	for n := int32(1); n <= 7; n++ {
		m2.mark(n)
	}
	m2d := &defaultsMsg{}
	ck(decodeMsg(encodeMsg(m2), m2d, false, tbl) == nil, "decode explicit values")
	ck(m2d.has(2) && m2d.i32 == 99 && m2d.i64 == -1<<40 && m2d.f64 == 2.5, "explicit numeric values")
	ck(m2d.s == "set" && bytes.Equal(m2d.by, []byte{1, 2, 3}) && m2d.e == mood(99), "explicit string/bytes/enum")

	// 5. 嵌套消息：缺席为 nil；出现空 payload 时应用其内部默认值。
	c0 := &defaultsMsg{}
	ck(decodeMsg([]byte{0x42, 0x00}, c0, false, tbl) == nil, "decode empty child")
	ck(c0.has(8) && c0.child != nil && !c0.child.hasN && c0.child.n == 42, "nested default n=42")
	c1 := &defaultsMsg{}
	ck(decodeMsg([]byte{0x42, 0x02, 0x08, 0x07}, c1, false, tbl) == nil, "decode child n=7")
	ck(c1.child != nil && c1.child.hasN && c1.child.n == 7, "nested explicit n")
	cenc := encodeMsg(c1)
	c1d := &defaultsMsg{}
	ck(decodeMsg(cenc, c1d, false, tbl) == nil && c1d.child != nil && c1d.child.n == 7, "nested roundtrip")
	cenc0 := encodeMsg(c0)
	c0d := &defaultsMsg{}
	ck(decodeMsg(cenc0, c0d, false, tbl) == nil && c0d.child != nil && !c0d.child.hasN && c0d.child.n == 42, "nested default roundtrip")

	// 6. repeated/map 无默认值；merge 追加/覆盖。
	r0 := &defaultsMsg{}
	repWire := appendBytesField(nil, 9, []byte{1, 2, 3})
	ck(decodeMsg(repWire, r0, false, tbl) == nil && len(r0.rep) == 3 && r0.rep[2] == 3, "repeated decode")
	if r0.mp != nil {
		ck(false, "map must stay nil")
	}
	mergeBase := &defaultsMsg{}
	ck(decodeMsg(encodeMsg(m2), mergeBase, false, tbl) == nil, "merge base")
	partial := &defaultsMsg{}
	partial.mark(3)
	partial.i64 = 5
	partial.rep = []int32{9}
	partial.mp = map[string]int32{"x": 1}
	if err := decodeMsg(encodeMsg(partial), mergeBase, true, tbl); err != nil {
		ck(false, "merge decode: "+err.Error())
	}
	ck(mergeBase.has(2) && mergeBase.i32 == 99, "merge keeps existing presence")
	ck(mergeBase.has(3) && mergeBase.i64 == 5, "merge overwrites present field")
	ck(len(mergeBase.rep) == 1 && mergeBase.rep[0] == 9, "merge appends repeated")
	ck(mergeBase.mp["x"] == 1, "merge adds map key")
	// 再次 merge 空消息不应覆盖已有值。
	if err := decodeMsg(nil, mergeBase, true, tbl); err != nil {
		ck(false, "merge empty: "+err.Error())
	}
	ck(mergeBase.i32 == 99 && mergeBase.i64 == 5 && mergeBase.s == "set", "merge empty keeps values")
	// 非 merge 重置。
	ck(decodeMsg(nil, mergeBase, false, tbl) == nil, "replace empty")
	ck(!mergeBase.has(2) && mergeBase.i32 == -7 && mergeBase.rep == nil && mergeBase.mp == nil, "non-merge resets and reapplies defaults")

	// 7. reset 与应用默认值幂等。
	m3 := &defaultsMsg{}
	decodeMsg(nil, m3, false, tbl)
	m3.applyDefaults(tbl)
	ck(m3.i32 == -7 && m3.s == "hé\n\x00", "apply defaults idempotent")
	m3.reset()
	ck(m3.i32 == 0 && m3.s == "" && !m3.has(1), "reset clears values")
	m3.applyDefaults(tbl)
	ck(m3.i32 == -7 && m3.b, "apply defaults after reset")

	// 8. 默认 bytes 隔离：Get 副本与消息内容均不反向修改表。
	iso := &defaultsMsg{}
	decodeMsg(nil, iso, false, tbl)
	got := iso.getBytes()
	got[0] = 0xff
	ck(iso.by[0] == 0xde, "getBytes returns copy")
	iso.mark(6)
	iso.by[0] = 0x11
	iso.applyDefaults(tbl)
	ck(iso.by[0] == 0x11, "apply does not overwrite present")
	iso.reset()
	iso.applyDefaults(tbl)
	ck(iso.by[0] == 0xde, "default table unaffected by message mutation")
	if tbl[5].vby[0] != 0xde {
		ck(false, "default table bytes intact")
	}

	// 9. 默认值表构建错误。
	expectBuildError("duplicate", []string{"1|bool|true", "1|int32|1"})
	expectBuildError("unknown kind", []string{"1|uint8|1"})
	expectBuildError("bad number", []string{"x|bool|true"})
	expectBuildError("zero number", []string{"0|bool|true"})
	expectBuildError("bad int", []string{"1|int32|abc"})
	expectBuildError("int32 overflow", []string{"1|int32|999999999999"})
	expectBuildError("bad float", []string{"1|double|x"})
	expectBuildError("bad hex", []string{"1|bytes|zz"})
	expectBuildError("odd hex", []string{"1|bytes|abc"})
	expectBuildError("undefined enum", []string{"1|enum|99"})
	expectBuildError("missing fields", []string{"1|bool"})

	// 10. 解码错误。
	expectDecodeError("truncated varint", []byte{0x10, 0x80}, tbl)
	expectDecodeError("field1 wrong wire", []byte{0x0a, 0x01, 'x'}, tbl)
	expectDecodeError("field5 wrong wire", []byte{0x28, 0x01}, tbl)
	expectDecodeError("truncated bytes", []byte{0x2a, 0x03, 'a'}, tbl)
	expectDecodeError("truncated child", []byte{0x42, 0x03, 0x08}, tbl)
	expectDecodeError("field zero", []byte{0x00}, tbl)
	expectDecodeError("wire type 6", []byte{0x0e}, tbl)

	// 11. 输入 buffer 释放/改写与 GC：显式值不与输入别名，默认表保留。
	tmp := encodeMsg(m2)
	keep := &defaultsMsg{}
	ck(decodeMsg(tmp, keep, false, tbl) == nil, "alias decode")
	for i := range tmp {
		tmp[i] = 0xee
	}
	tmp = nil
	gcStress()
	ck(keep.s == "set" && keep.has(2) && keep.i32 == 99, "explicit values survive buffer clobber")
	r := rand.New(rand.NewSource(seed))

	// 12. 随机 presence 组合与默认值矩阵。
	for i := 0; i < 200; i++ {
		want := &defaultsMsg{}
		for num := int32(1); num <= 7; num++ {
			if r.Intn(2) == 0 {
				continue
			}
			want.mark(num)
			switch num {
			case 1:
				want.b = r.Intn(2) == 1
			case 2:
				want.i32 = int32(r.Intn(200) - 100)
			case 3:
				want.i64 = int64(r.Intn(1<<30) - 1<<29)
			case 4:
				want.f64 = float64(r.Intn(1000)) / 8
			case 5:
				want.s = []string{"", "x", "hé\n"}[r.Intn(3)]
			case 6:
				if r.Intn(3) == 0 {
					want.by = []byte{}
				} else {
					want.by = []byte{byte(r.Intn(256)), byte(r.Intn(256))}
				}
			case 7:
				want.e = mood([]int32{0, 2, 3, 99}[r.Intn(4)])
			}
		}
		if r.Intn(2) == 0 {
			want.mark(8)
			want.child = &child{}
			if r.Intn(2) == 0 {
				want.child.hasN = true
				want.child.n = int32(r.Intn(100))
			}
		}
		if n := r.Intn(4); n > 0 {
			for j := 0; j < n; j++ {
				want.rep = append(want.rep, int32(r.Intn(100)))
			}
		}
		if n := r.Intn(4); n > 0 {
			want.mp = make(map[string]int32, n)
			for j := 0; j < n; j++ {
				want.mp["k"+strconv.Itoa(r.Intn(10))] = int32(r.Intn(100))
			}
		}
		wire := encodeMsg(want)
		got := &defaultsMsg{}
		ckSeed(decodeMsg(wire, got, false, tbl) == nil, "random decode")
		for num := int32(1); num <= 8; num++ {
			ckSeed(got.has(num) == want.has(num), "random presence")
		}
		ckSeed(got.i32 == want.i32 || !want.has(2), "random int32 presence value")
		if !want.has(2) {
			ckSeed(got.i32 == -7, "random int32 default")
		}
		if !want.has(1) {
			ckSeed(got.b, "random bool default")
		}
		if !want.has(3) {
			ckSeed(got.i64 == 1<<40, "random int64 default")
		}
		if !want.has(4) {
			ckSeed(got.f64 == math.Pi, "random double default")
		}
		if !want.has(5) {
			ckSeed(got.s == "hé\n\x00", "random string default")
		}
		if !want.has(6) {
			ckSeed(bytes.Equal(got.by, []byte{0xde, 0xad, 0xbe, 0xef, 0x00, 0x0a}), "random bytes default")
		}
		if !want.has(7) {
			ckSeed(got.e == moodHappy, "random enum default")
		}
		if want.has(8) {
			ckSeed(got.child != nil, "random child present")
			if got.child != nil {
				n := int32(42)
				if want.child.hasN {
					n = want.child.n
				}
				ckSeed(got.child.n == n, "random child value")
				ckSeed(got.child.hasN == want.child.hasN, "random child presence")
			}
		} else {
			ckSeed(got.child == nil, "random child absent")
		}
		ckSeed(len(got.rep) == len(want.rep), "random repeated length")
		for j := range want.rep {
			ckSeed(got.rep[j] == want.rep[j], "random repeated value")
		}
		ckSeed(len(got.mp) == len(want.mp), "random map length")
		for k, v := range want.mp {
			ckSeed(got.mp[k] == v, "random map value")
		}
		if i%25 == 24 {
			gcStress()
			ckSeed(bytes.Equal(encodeMsg(got), wire), "random survives GC")
		}
	}

	finish("l10_defaults_decode")
}
