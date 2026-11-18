// L10: 生成消息解码器的未知字段保留：按字段号保存原始 tag+value、
// group 原样跳过、重编码排序稳定、merge/reset 语义与输入 buffer 释放后 GC 存活。
package main

import (
	"bytes"
	"errors"
	"fmt"
	"math/rand"
	"os"
	"runtime"
	"sort"
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

// ---- 最小 protowire ----

var (
	errTruncated     = errors.New("truncated")
	errOverflow      = errors.New("varint overflow")
	errFieldNumber   = errors.New("invalid field number")
	errWireType      = errors.New("invalid wire type")
	errUnexpectedEnd = errors.New("unexpected end group")
	errGroupMismatch = errors.New("mismatched end group")
	errUnterminated  = errors.New("unterminated group")
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
	case 3:
		total := 0
		for {
			if total >= len(b) {
				return 0, errUnterminated
			}
			n2, wt2, tn, err := consumeTag(b[total:])
			if err != nil {
				return 0, err
			}
			if wt2 == 4 {
				if n2 != num {
					return 0, errGroupMismatch
				}
				return total + tn, nil
			}
			total += tn
			vn, err := skipValue(b[total:], n2, wt2)
			if err != nil {
				return 0, err
			}
			total += vn
		}
	case 4:
		return 0, errUnexpectedEnd
	default:
		return 0, errWireType
	}
}

// ---- 生成消息形态 ----

type child struct {
	x       int32
	unknown map[int32][][]byte
}

type msg struct {
	a       int32
	b       string
	c       []int32
	d       *child
	unknown map[int32][][]byte
}

func addUnknown(m map[int32][][]byte, num int32, raw []byte) map[int32][][]byte {
	if m == nil {
		m = make(map[int32][][]byte)
	}
	m[num] = append(m[num], append([]byte(nil), raw...))
	return m
}

// bytesPayload 从已确认 wire type=2 的值域中取出长度前缀后的载荷。
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

func decodeChild(b []byte) (*child, error) {
	ch := &child{}
	for len(b) > 0 {
		start := b
		num, wt, tn, err := consumeTag(b)
		if err != nil {
			return nil, err
		}
		b = b[tn:]
		vn, err := skipValue(b, num, wt)
		if err != nil {
			return nil, err
		}
		raw := append([]byte(nil), start[:tn+vn]...)
		val := b[:vn]
		b = b[vn:]
		if num == 1 && wt == 0 {
			v, _, err := consumeUvarint(val)
			if err != nil {
				return nil, err
			}
			ch.x = int32(v)
			continue
		}
		ch.unknown = addUnknown(ch.unknown, num, raw)
	}
	return ch, nil
}

func decodeMsg(b []byte) (*msg, error) {
	m := &msg{}
	for len(b) > 0 {
		start := b
		num, wt, tn, err := consumeTag(b)
		if err != nil {
			return nil, err
		}
		b = b[tn:]
		vn, err := skipValue(b, num, wt)
		if err != nil {
			return nil, err
		}
		raw := append([]byte(nil), start[:tn+vn]...)
		val := b[:vn]
		b = b[vn:]

		switch num {
		case 1:
			if wt != 0 {
				m.unknown = addUnknown(m.unknown, num, raw)
				continue
			}
			v, _, err := consumeUvarint(val)
			if err != nil {
				return nil, err
			}
			m.a = int32(v)
		case 2:
			if wt != 2 {
				m.unknown = addUnknown(m.unknown, num, raw)
				continue
			}
			payload, err := bytesPayload(val)
			if err != nil {
				return nil, err
			}
			m.b = string(append([]byte(nil), payload...))
		case 3:
			switch wt {
			case 0:
				v, _, err := consumeUvarint(val)
				if err != nil {
					return nil, err
				}
				m.c = append(m.c, int32(v))
			case 2:
				payload, err := bytesPayload(val)
				if err != nil {
					return nil, err
				}
				rest := payload
				for len(rest) > 0 {
					v, n, err := consumeUvarint(rest)
					if err != nil {
						return nil, err
					}
					rest = rest[n:]
					m.c = append(m.c, int32(v))
				}
			default:
				m.unknown = addUnknown(m.unknown, num, raw)
			}
		case 4:
			if wt != 2 {
				m.unknown = addUnknown(m.unknown, num, raw)
				continue
			}
			payload, err := bytesPayload(val)
			if err != nil {
				return nil, err
			}
			ch, err := decodeChild(payload)
			if err != nil {
				return nil, err
			}
			m.d = ch
		default:
			m.unknown = addUnknown(m.unknown, num, raw)
		}
	}
	return m, nil
}

func encodeChild(ch *child) []byte {
	var b []byte
	if ch.x != 0 {
		b = appendTag(b, 1, 0)
		b = appendUvarint(b, uint64(ch.x))
	}
	b = appendUnknowns(b, ch.unknown)
	return b
}

func appendUnknowns(b []byte, unknown map[int32][][]byte) []byte {
	nums := make([]int32, 0, len(unknown))
	for n := range unknown {
		nums = append(nums, n)
	}
	sort.Slice(nums, func(i, j int) bool { return nums[i] < nums[j] })
	for _, n := range nums {
		for _, raw := range unknown[n] {
			b = append(b, raw...)
		}
	}
	return b
}

func encodeMsg(m *msg) []byte {
	var b []byte
	if m.a != 0 {
		b = appendTag(b, 1, 0)
		b = appendUvarint(b, uint64(m.a))
	}
	if m.b != "" {
		b = appendBytesField(b, 2, []byte(m.b))
	}
	if len(m.c) > 0 {
		var packed []byte
		for _, v := range m.c {
			packed = appendUvarint(packed, uint64(v))
		}
		b = appendBytesField(b, 3, packed)
	}
	if m.d != nil {
		b = appendBytesField(b, 4, encodeChild(m.d))
	}
	b = appendUnknowns(b, m.unknown)
	return b
}

func cloneUnknown(src map[int32][][]byte) map[int32][][]byte {
	if src == nil {
		return nil
	}
	dst := make(map[int32][][]byte, len(src))
	for n, chunks := range src {
		cp := make([][]byte, len(chunks))
		for i, raw := range chunks {
			cp[i] = append([]byte(nil), raw...)
		}
		dst[n] = cp
	}
	return dst
}

func (m *msg) mergeFrom(o *msg) {
	if o.a != 0 {
		m.a = o.a
	}
	if o.b != "" {
		m.b = o.b
	}
	m.c = append(m.c, o.c...)
	if o.d != nil {
		m.d = &child{x: o.d.x, unknown: cloneUnknown(o.d.unknown)}
	}
	for n, chunks := range o.unknown {
		for _, raw := range chunks {
			m.unknown = addUnknown(m.unknown, n, raw)
		}
	}
}

func (m *msg) reset() {
	m.a, m.b, m.c, m.d, m.unknown = 0, "", nil, nil, nil
}

func unknownEqual(a, b map[int32][][]byte) bool {
	if len(a) != len(b) {
		return false
	}
	for n, ca := range a {
		cb, ok := b[n]
		if !ok || len(ca) != len(cb) {
			return false
		}
		for i := range ca {
			if !bytes.Equal(ca[i], cb[i]) {
				return false
			}
		}
	}
	return true
}

// ---- 测试辅助 ----

func fieldVarint(num int32, v uint64) []byte {
	b := appendTag(nil, num, 0)
	return appendUvarint(b, v)
}

func fieldBytes(num int32, v []byte) []byte {
	return appendBytesField(nil, num, v)
}

func rawGroup(num int32, inner []byte) []byte {
	b := appendTag(nil, num, 3)
	b = append(b, inner...)
	return appendTag(b, num, 4)
}

func expectErr(name string, b []byte) {
	_, err := decodeMsg(b)
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
	rounds := 1
	if v := os.Getenv("TORTURE_ROUNDS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			rounds = n
			if rounds > 100 {
				rounds = 100
			}
		}
	}

	// 1. 规范顺序的已知+未知字段：解码后重编码逐字节相同。
	childEnc := appendTag(nil, 1, 0)
	childEnc = appendUvarint(childEnc, 7)
	golden := appendTag(nil, 1, 0)
	golden = appendUvarint(golden, 1)
	golden = appendBytesField(golden, 2, []byte("hi"))
	golden = appendBytesField(golden, 3, []byte{3, 4})
	golden = appendBytesField(golden, 4, childEnc)
	golden = append(golden, fieldVarint(5, 9)...)
	golden = append(golden, fieldBytes(6, []byte("xy"))...)

	m, err := decodeMsg(golden)
	ck(err == nil, "golden decode")
	ck(m != nil && m.a == 1 && m.b == "hi" && len(m.c) == 2 && m.c[0] == 3 && m.c[1] == 4, "golden known scalars")
	ck(m != nil && m.d != nil && m.d.x == 7, "golden nested")
	ck(m != nil && unknownEqual(m.unknown, map[int32][][]byte{
		5: {fieldVarint(5, 9)},
		6: {fieldBytes(6, []byte("xy"))},
	}), "golden unknown captured")
	ck(bytes.Equal(encodeMsg(m), golden), "golden re-encode byte-identical")

	// 2. 非规范 varint 原样保留，已知字段的非规范编码按值处理。
	nonCanon := []byte{0x48, 0x80, 0x00}
	m, err = decodeMsg(nonCanon)
	ck(err == nil && m.unknown[9] != nil && bytes.Equal(m.unknown[9][0], nonCanon), "non-canonical unknown preserved")
	ck(bytes.Equal(encodeMsg(m), nonCanon), "non-canonical unknown re-encoded")

	knownNonCanon := decodeMsgMust([]byte{0x08, 0x80, 0x00})
	ck(knownNonCanon.a == 0, "non-canonical known varint value")
	ck(len(encodeMsg(knownNonCanon)) == 0, "known zero omitted on encode")

	// 3. 同字段号多次出现保留顺序；已知字段 wire type 不匹配转 unknown。
	m, err = decodeMsg(append(fieldVarint(5, 1), fieldVarint(5, 2)...))
	ck(err == nil && len(m.unknown[5]) == 2, "repeated unknown chunks kept")
	ck(bytes.Equal(m.unknown[5][0], fieldVarint(5, 1)) && bytes.Equal(m.unknown[5][1], fieldVarint(5, 2)), "unknown chunk order")
	ck(bytes.Equal(encodeMsg(m), append(fieldVarint(5, 1), fieldVarint(5, 2)...)), "unknown chunk order re-encode")

	wrongWire := []byte{0x0a, 0x01, 'z'} // field 1, bytes
	m, err = decodeMsg(wrongWire)
	ck(err == nil && m.a == 0 && len(m.unknown[1]) == 1, "known field wrong wire stored unknown")
	ck(bytes.Equal(encodeMsg(m), wrongWire), "wrong-wire unknown re-encoded")

	// 4. packed/unpacked repeated、空 packed、嵌套未知字段。
	m, err = decodeMsg(append(fieldVarint(3, 5), fieldVarint(3, 6)...))
	ck(err == nil && len(m.c) == 2 && m.c[0] == 5 && m.c[1] == 6, "unpacked repeated")
	m, err = decodeMsg([]byte{0x1a, 0x00})
	ck(err == nil && len(m.c) == 0, "empty packed repeated")
	nested := appendTag(nil, 1, 0)
	nested = appendUvarint(nested, 7)
	nested = append(nested, fieldVarint(9, 5)...)
	m, err = decodeMsg(fieldBytes(4, nested))
	ck(err == nil && m.d != nil && m.d.x == 7 && len(m.d.unknown[9]) == 1, "nested unknown preserved")
	ck(bytes.Equal(encodeMsg(m), fieldBytes(4, nested)), "nested re-encode")

	// 5. group：未知 group 原样保留，嵌套 group 与结束标记匹配。
	group := rawGroup(7, fieldVarint(1, 1))
	m, err = decodeMsg(group)
	ck(err == nil && len(m.unknown[7]) == 1 && bytes.Equal(m.unknown[7][0], group), "group raw preserved")
	ck(bytes.Equal(encodeMsg(m), group), "group re-encode")
	nestedGroup := rawGroup(7, rawGroup(8, fieldBytes(2, []byte("q"))))
	m, err = decodeMsg(nestedGroup)
	ck(err == nil && bytes.Equal(encodeMsg(m), nestedGroup), "nested group re-encode")

	// 6. 错误路径。
	expectErr("field zero", []byte{0x00})
	expectErr("wire type 6", []byte{0x0e})
	expectErr("truncated varint", []byte{0x08, 0x80})
	expectErr("truncated bytes", []byte{0x12, 0x05, 'a'})
	expectErr("unmatched end group", []byte{0x3c})
	expectErr("unterminated group", []byte{0x3b, 0x08, 0x01})
	expectErr("group number mismatch", []byte{0x3b, 0x4c})
	expectErr("bad packed varint", []byte{0x1a, 0x01, 0x80})
	empty, err := decodeMsg(nil)
	ck(err == nil && empty.a == 0 && empty.unknown == nil, "empty message allowed")

	// 7. merge 追加未知片段；reset 清空。
	m1 := &msg{a: 1}
	m1.unknown = addUnknown(m1.unknown, 5, fieldVarint(5, 1))
	m2 := &msg{a: 2, c: []int32{9}}
	m2.unknown = addUnknown(m2.unknown, 5, fieldVarint(5, 2))
	m2.unknown = addUnknown(m2.unknown, 6, fieldVarint(6, 3))
	m1.mergeFrom(m2)
	ck(m1.a == 2 && len(m1.c) == 1 && m1.c[0] == 9, "merge known last wins")
	ck(len(m1.unknown[5]) == 2 && len(m1.unknown[6]) == 1, "merge appends unknown")
	ck(bytes.Equal(m1.unknown[5][1], fieldVarint(5, 2)), "merge unknown order")
	m1.reset()
	ck(m1.a == 0 && m1.b == "" && m1.c == nil && m1.d == nil && m1.unknown == nil, "reset clears all")

	// 8. 输入 buffer 释放/改写后，未知字段与字符串不别名。
	tmp := append([]byte(nil), golden...)
	m, err = decodeMsg(tmp)
	ck(err == nil, "alias decode")
	for i := range tmp {
		tmp[i] = 0xee
	}
	tmp = nil
	gcStress()
	ck(m != nil && m.b == "hi" && m.a == 1, "known values copied")
	ck(m != nil && bytes.Equal(m.unknown[5][0], fieldVarint(5, 9)), "unknown copied before buffer free")
	ck(bytes.Equal(encodeMsg(m), golden), "re-encode after buffer clobber")
	kept := m
	m = nil
	gcStress()
	ck(bytes.Equal(encodeMsg(kept), golden), "message survives GC")

	// 9. 随机 interleaving：已知与未知字段混排，解码/重编码幂等。
	r := rand.New(rand.NewSource(seed))
	type rpart struct {
		num     int32
		unknown bool
		raw     []byte
	}
	for round := 0; round < rounds*60; round++ {
		rm := &msg{}
		var parts []rpart
		if v := r.Intn(9) + 1; v != 0 {
			rm.a = int32(v)
			parts = append(parts, rpart{raw: fieldVarint(1, uint64(rm.a))})
		}
		if r.Intn(2) == 0 {
			rm.b = "s" + strconv.Itoa(r.Intn(100))
			parts = append(parts, rpart{raw: fieldBytes(2, []byte(rm.b))})
		}
		if n := r.Intn(4); n > 0 {
			for i := 0; i < n; i++ {
				v := int32(r.Intn(200) - 100)
				rm.c = append(rm.c, v)
			}
			var packed []byte
			for _, v := range rm.c {
				packed = appendUvarint(packed, uint64(v))
			}
			parts = append(parts, rpart{raw: fieldBytes(3, packed)})
		}
		if r.Intn(3) == 0 {
			rm.d = &child{x: int32(r.Intn(50) + 1)}
			inner := fieldVarint(1, uint64(rm.d.x))
			if r.Intn(2) == 0 {
				chunk := fieldVarint(9, uint64(r.Intn(10)))
				rm.d.unknown = addUnknown(rm.d.unknown, 9, chunk)
				inner = append(inner, chunk...)
			}
			parts = append(parts, rpart{raw: fieldBytes(4, inner)})
		}
		for i := 0; i < r.Intn(6); i++ {
			num := int32(5 + r.Intn(8))
			var raw []byte
			switch r.Intn(5) {
			case 0:
				raw = fieldVarint(num, uint64(r.Intn(1<<20)))
			case 1:
				n := r.Intn(8)
				buf := make([]byte, n)
				for j := range buf {
					buf[j] = byte(r.Intn(256))
				}
				raw = fieldBytes(num, buf)
			case 2:
				raw = appendTag(nil, num, 1)
				for j := 0; j < 8; j++ {
					raw = append(raw, byte(r.Intn(256)))
				}
			case 3:
				raw = appendTag(nil, num, 5)
				for j := 0; j < 4; j++ {
					raw = append(raw, byte(r.Intn(256)))
				}
			default:
				raw = rawGroup(num, fieldVarint(int32(1+r.Intn(5)), uint64(r.Intn(100))))
			}
			parts = append(parts, rpart{num: num, unknown: true, raw: raw})
		}
		r.Shuffle(len(parts), func(i, j int) { parts[i], parts[j] = parts[j], parts[i] })
		var wire []byte
		wantUnknown := make(map[int32][][]byte)
		for _, p := range parts {
			wire = append(wire, p.raw...)
			if p.unknown {
				wantUnknown[p.num] = append(wantUnknown[p.num], append([]byte(nil), p.raw...))
			}
		}
		got, err := decodeMsg(wire)
		ckSeed(err == nil, "random decode")
		if err != nil {
			continue
		}
		ckSeed(got.a == rm.a, "random known a")
		ckSeed(got.b == rm.b, "random known b")
		ckSeed(len(got.c) == len(rm.c), "random repeated len")
		if len(got.c) == len(rm.c) {
			for i := range rm.c {
				ckSeed(got.c[i] == rm.c[i], "random repeated value")
			}
		}
		ckSeed(unknownEqual(got.unknown, wantUnknown), "random unknown table")
		enc1 := encodeMsg(got)
		got2, err := decodeMsg(enc1)
		ckSeed(err == nil, "random re-decode")
		if err != nil {
			continue
		}
		ckSeed(unknownEqual(got2.unknown, wantUnknown), "random unknown after re-decode")
		ckSeed(bytes.Equal(encodeMsg(got2), enc1), "random idempotent re-encode")
		if round%25 == 24 {
			gcStress()
			ckSeed(bytes.Equal(encodeMsg(got), enc1), "random survives GC")
		}
	}

	finish("l10_unknown_fields")
}

func decodeMsgMust(b []byte) *msg {
	m, err := decodeMsg(b)
	if err != nil {
		ck(false, "decodeMsgMust: "+err.Error())
		return &msg{}
	}
	return m
}
