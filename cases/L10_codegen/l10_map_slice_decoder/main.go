// L10: 生成消息的 map/slice 解码器：packed/unpacked repeated、map entry、
// 重复键 last-wins、缺失 key/value 默认值、nil vs empty、merge 语义与 GC。
package main

import (
	"bytes"
	"errors"
	"fmt"
	"math"
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

// ---- wire 基础 ----

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

func zigzagEncode32(v int32) uint64 {
	return uint64(uint32(v<<1) ^ uint32(v>>31))
}

func zigzagDecode32(v uint64) int32 {
	return int32(v>>1) ^ -int32(v&1)
}

// ---- 生成消息形态 ----

type msg struct {
	ints   []int32
	sints  []int32
	strs   []string
	blobs  [][]byte
	strMap map[string]int64
	intMap map[int32]string
}

func appendRepeatedInt32(b []byte, num int32, vals []int32, zigzag bool) []byte {
	if len(vals) == 0 {
		return b
	}
	var payload []byte
	for _, v := range vals {
		if zigzag {
			payload = appendUvarint(payload, zigzagEncode32(v))
		} else {
			payload = appendUvarint(payload, uint64(int64(v)))
		}
	}
	return appendBytesField(b, num, payload)
}

func encodeMsg(m *msg) []byte {
	var b []byte
	b = appendRepeatedInt32(b, 1, m.ints, false)
	b = appendRepeatedInt32(b, 2, m.sints, true)
	for _, s := range m.strs {
		b = appendBytesField(b, 3, []byte(s))
	}
	strKeys := make([]string, 0, len(m.strMap))
	for k := range m.strMap {
		strKeys = append(strKeys, k)
	}
	sort.Strings(strKeys)
	for _, k := range strKeys {
		var entry []byte
		entry = appendBytesField(entry, 1, []byte(k))
		entry = appendTag(entry, 2, 0)
		entry = appendUvarint(entry, uint64(m.strMap[k]))
		b = appendBytesField(b, 4, entry)
	}
	intKeys := make([]int32, 0, len(m.intMap))
	for k := range m.intMap {
		intKeys = append(intKeys, k)
	}
	sort.Slice(intKeys, func(i, j int) bool { return intKeys[i] < intKeys[j] })
	for _, k := range intKeys {
		var entry []byte
		entry = appendTag(entry, 1, 0)
		entry = appendUvarint(entry, uint64(int64(k)))
		entry = appendBytesField(entry, 2, []byte(m.intMap[k]))
		b = appendBytesField(b, 5, entry)
	}
	for _, blob := range m.blobs {
		b = appendBytesField(b, 6, blob)
	}
	return b
}

func decodeEntryStr(payload []byte, m map[string]int64) (map[string]int64, error) {
	if m == nil {
		m = make(map[string]int64)
	}
	var k string
	var v int64
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
		switch num {
		case 1:
			if wt != 2 {
				return nil, errWireType
			}
			p, err := bytesPayload(val)
			if err != nil {
				return nil, err
			}
			k = string(p)
		case 2:
			if wt != 0 {
				return nil, errWireType
			}
			x, _, err := consumeUvarint(val)
			if err != nil {
				return nil, err
			}
			v = int64(x)
		}
	}
	m[k] = v
	return m, nil
}

func decodeEntryInt(payload []byte, m map[int32]string) (map[int32]string, error) {
	if m == nil {
		m = make(map[int32]string)
	}
	var k int32
	var v string
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
		switch num {
		case 1:
			if wt != 0 {
				return nil, errWireType
			}
			x, _, err := consumeUvarint(val)
			if err != nil {
				return nil, err
			}
			k = int32(x)
		case 2:
			if wt != 2 {
				return nil, errWireType
			}
			p, err := bytesPayload(val)
			if err != nil {
				return nil, err
			}
			v = string(p)
		}
	}
	m[k] = v
	return m, nil
}

func decodeMsg(b []byte, m *msg, merge bool) error {
	if !merge {
		*m = msg{}
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
			switch wt {
			case 0:
				x, _, err := consumeUvarint(val)
				if err != nil {
					return err
				}
				m.ints = append(m.ints, int32(x))
			case 2:
				p, err := bytesPayload(val)
				if err != nil {
					return err
				}
				rest := p
				for len(rest) > 0 {
					x, n, err := consumeUvarint(rest)
					if err != nil {
						return err
					}
					rest = rest[n:]
					m.ints = append(m.ints, int32(x))
				}
			default:
				return errWireType
			}
		case 2:
			switch wt {
			case 0:
				x, _, err := consumeUvarint(val)
				if err != nil {
					return err
				}
				m.sints = append(m.sints, zigzagDecode32(x))
			case 2:
				p, err := bytesPayload(val)
				if err != nil {
					return err
				}
				rest := p
				for len(rest) > 0 {
					x, n, err := consumeUvarint(rest)
					if err != nil {
						return err
					}
					rest = rest[n:]
					m.sints = append(m.sints, zigzagDecode32(x))
				}
			default:
				return errWireType
			}
		case 3:
			if wt != 2 {
				return errWireType
			}
			p, err := bytesPayload(val)
			if err != nil {
				return err
			}
			m.strs = append(m.strs, string(p))
		case 4:
			if wt != 2 {
				return errWireType
			}
			p, err := bytesPayload(val)
			if err != nil {
				return err
			}
			m.strMap, err = decodeEntryStr(p, m.strMap)
			if err != nil {
				return err
			}
		case 5:
			if wt != 2 {
				return errWireType
			}
			p, err := bytesPayload(val)
			if err != nil {
				return err
			}
			m.intMap, err = decodeEntryInt(p, m.intMap)
			if err != nil {
				return err
			}
		case 6:
			if wt != 2 {
				return errWireType
			}
			p, err := bytesPayload(val)
			if err != nil {
				return err
			}
			cp := make([]byte, len(p))
			copy(cp, p)
			m.blobs = append(m.blobs, cp)
		default:
			// 顶层未知字段：跳过。
		}
	}
	return nil
}

func equalMsg(a, b *msg) bool {
	if len(a.ints) != len(b.ints) || len(a.sints) != len(b.sints) || len(a.strs) != len(b.strs) || len(a.blobs) != len(b.blobs) {
		return false
	}
	for i := range a.ints {
		if a.ints[i] != b.ints[i] {
			return false
		}
	}
	for i := range a.sints {
		if a.sints[i] != b.sints[i] {
			return false
		}
	}
	for i := range a.strs {
		if a.strs[i] != b.strs[i] {
			return false
		}
	}
	for i := range a.blobs {
		if !bytes.Equal(a.blobs[i], b.blobs[i]) {
			return false
		}
	}
	if len(a.strMap) != len(b.strMap) || len(a.intMap) != len(b.intMap) {
		return false
	}
	for k, v := range a.strMap {
		if bv, ok := b.strMap[k]; !ok || bv != v {
			return false
		}
	}
	for k, v := range a.intMap {
		if bv, ok := b.intMap[k]; !ok || bv != v {
			return false
		}
	}
	return true
}

func decode(b []byte, merge bool) (*msg, error) {
	m := &msg{}
	if err := decodeMsg(b, m, merge); err != nil {
		return nil, err
	}
	return m, nil
}

func expectError(name string, b []byte) {
	_, err := decode(b, false)
	ck(err != nil, "expected error: "+name)
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

	// 1. 全字段往返：packed、zigzag、空字符串/空 blob、排序 map。
	want := &msg{
		ints:   []int32{1, -2, math.MinInt32, math.MaxInt32},
		sints:  []int32{-1, 0, 1, math.MinInt32, math.MaxInt32},
		strs:   []string{"", "a", "hello"},
		blobs:  [][]byte{{}, {0xde, 0xad}, {}},
		strMap: map[string]int64{"": 7, "a": -5, "b": 0, "long-key": math.MinInt64},
		intMap: map[int32]string{-1: "neg", 0: "zero", 2: "", 100: "hundred"},
	}
	enc := encodeMsg(want)
	got, err := decode(enc, false)
	ck(err == nil, "full decode")
	ck(got != nil && equalMsg(got, want), "full roundtrip")
	ck(len(got.blobs) == 3 && got.blobs[0] != nil && len(got.blobs[0]) == 0, "zero-length blob is non-nil")
	ck(len(got.strs) == 3 && got.strs[0] == "", "zero-length string preserved")
	ck(got.strMap[""] == 7 && got.intMap[2] == "", "empty map key/value preserved")
	ck(bytes.Equal(encodeMsg(got), enc), "re-encode deterministic")

	// 2. nil vs empty 语义。
	empty, err := decode(nil, false)
	ck(err == nil && empty.ints == nil && empty.strMap == nil && empty.blobs == nil, "empty message nil containers")
	ck(len(encodeMsg(&msg{ints: []int32{}, strs: []string{}, blobs: [][]byte{}})) == 0, "empty non-nil containers encode nothing")
	packedEmpty, err := decode([]byte{0x0a, 0x00}, false)
	ck(err == nil && len(packedEmpty.ints) == 0, "empty packed repeated")
	emptyStrs, err := decode([]byte{0x1a, 0x00}, false)
	ck(err == nil && len(emptyStrs.strs) == 1 && emptyStrs.strs[0] == "", "empty repeated string element")
	emptyBlob, err := decode([]byte{0x32, 0x00}, false)
	ck(err == nil && len(emptyBlob.blobs) == 1 && emptyBlob.blobs[0] != nil && len(emptyBlob.blobs[0]) == 0, "empty repeated blob element")
	emptyEntry, err := decode([]byte{0x22, 0x00}, false)
	ck(err == nil && emptyEntry.strMap != nil && len(emptyEntry.strMap) == 1 && emptyEntry.strMap[""] == 0, "empty map entry defaults")

	// 3. unpacked repeated、负 int32 十字节 varint、zigzag 极值。
	unpacked := appendTag(nil, 1, 0)
	negOne := int32(-1)
	unpacked = appendUvarint(unpacked, uint64(int64(negOne)))
	unpacked = appendTag(unpacked, 2, 0)
	unpacked = appendUvarint(unpacked, zigzagEncode32(math.MinInt32))
	u, err := decode(unpacked, false)
	ck(err == nil && len(u.ints) == 1 && u.ints[0] == -1, "unpacked int32 negative")
	ck(err == nil && len(u.sints) == 1 && u.sints[0] == math.MinInt32, "zigzag min int32")
	for _, v := range []int32{math.MinInt32, -1, 0, 1, math.MaxInt32} {
		if zigzagDecode32(zigzagEncode32(v)) != v {
			ck(false, "zigzag roundtrip "+strconv.Itoa(int(v)))
		}
	}

	// 4. map 重复键 last-wins、缺失 key/value、entry 内未知字段跳过。
	dup := appendBytesField(nil, 4, append(appendBytesField(nil, 1, []byte("k")), appendUvarint(appendTag(nil, 2, 0), 5)...))
	dup = append(dup, appendBytesField(nil, 4, append(appendBytesField(nil, 1, []byte("k")), appendUvarint(appendTag(nil, 2, 0), 9)...))...)
	dm, err := decode(dup, false)
	ck(err == nil && len(dm.strMap) == 1 && dm.strMap["k"] == 9, "duplicate map key last wins")
	onlyKey := appendBytesField(nil, 4, appendBytesField(nil, 1, []byte("k")))
	okm, err := decode(onlyKey, false)
	ck(err == nil && okm.strMap["k"] == 0, "map entry missing value default")
	onlyVal := appendBytesField(nil, 5, appendUvarint(appendTag(nil, 1, 0), 7))
	ovm, err := decode(onlyVal, false)
	ck(err == nil && ovm.intMap[7] == "", "map entry missing value string default")
	entryUnknown := appendBytesField(nil, 1, []byte("k"))
	entryUnknown = appendUvarint(appendTag(entryUnknown, 2, 0), 3)
	entryUnknown = appendUvarint(appendTag(entryUnknown, 9, 0), 99)
	eum, err := decode(appendBytesField(nil, 4, entryUnknown), false)
	ck(err == nil && eum.strMap["k"] == 3, "unknown entry field skipped")

	// 5. merge：repeated 追加、map 覆盖/新增、未出现字段保留；非 merge 重置。
	base, err := decode(encodeMsg(&msg{ints: []int32{1, 2}, strs: []string{"a"}, strMap: map[string]int64{"x": 1}}), false)
	ck(err == nil, "merge base")
	add := &msg{ints: []int32{3}, sints: []int32{-4}, strs: []string{"b"}, strMap: map[string]int64{"x": 9, "y": 2}}
	if err := decodeMsg(encodeMsg(add), base, true); err != nil {
		ck(false, "merge decode: "+err.Error())
	}
	ck(len(base.ints) == 3 && base.ints[2] == 3, "merge appends repeated")
	ck(len(base.sints) == 1 && base.sints[0] == -4, "merge adds new repeated")
	ck(len(base.strs) == 2 && base.strs[1] == "b", "merge appends strings")
	ck(base.strMap["x"] == 9 && base.strMap["y"] == 2, "merge overwrites map key")
	if err := decodeMsg(encodeMsg(&msg{ints: []int32{7}}), base, false); err != nil {
		ck(false, "replace decode")
	}
	ck(len(base.ints) == 1 && base.ints[0] == 7 && base.strMap == nil, "non-merge resets")

	// 6. 错误路径。
	expectError("field1 fixed32", []byte{0x0d, 1, 2, 3, 4})
	expectError("field4 varint", appendTag(nil, 4, 0))
	expectError("truncated packed varint", []byte{0x0a, 0x01, 0x80})
	expectError("truncated string", []byte{0x1a, 0x03, 'a'})
	expectError("truncated entry", []byte{0x22, 0x03, 0x0a, 0x02, 'a'})
	expectError("map string key wrong wire", appendBytesField(nil, 4, appendUvarint(appendTag(nil, 1, 0), 5)))
	expectError("map int key wrong wire", appendBytesField(nil, 5, appendBytesField(nil, 1, []byte("x"))))
	expectError("truncated map value", appendBytesField(nil, 4, append(appendBytesField(nil, 1, []byte("k")), 0x10, 0x80)))
	expectError("field zero", []byte{0x00})
	expectError("wire type 6", []byte{0x0e})

	// 7. 输入 buffer 改写/释放与 GC：解码结果不与输入别名。
	tmp := append([]byte(nil), enc...)
	kept, err := decode(tmp, false)
	ck(err == nil, "alias decode")
	for i := range tmp {
		tmp[i] = 0xee
	}
	tmp = nil
	gcStress()
	ck(kept != nil && equalMsg(kept, want), "decoded values copied before buffer clobber")
	ck(kept != nil && bytes.Equal(encodeMsg(kept), enc), "re-encode after buffer clobber")
	kept2 := kept
	kept = nil
	gcStress()
	ck(kept2 != nil && equalMsg(kept2, want), "decoded message survives GC")

	// 8. 随机编码/解码往返。
	r := rand.New(rand.NewSource(seed))
	for round := 0; round < rounds*60; round++ {
		w := &msg{}
		for i := 0; i < r.Intn(7); i++ {
			switch r.Intn(5) {
			case 0:
				w.ints = append(w.ints, math.MinInt32)
			case 1:
				w.ints = append(w.ints, math.MaxInt32)
			default:
				w.ints = append(w.ints, int32(r.Intn(2000)-1000))
			}
		}
		for i := 0; i < r.Intn(7); i++ {
			w.sints = append(w.sints, int32(r.Intn(1<<31)-1<<30))
		}
		for i := 0; i < r.Intn(6); i++ {
			if r.Intn(3) == 0 {
				w.strs = append(w.strs, "")
			} else {
				w.strs = append(w.strs, "s"+strconv.Itoa(r.Intn(1000)))
			}
		}
		for i := 0; i < r.Intn(5); i++ {
			n := r.Intn(6)
			buf := make([]byte, n)
			for j := range buf {
				buf[j] = byte(r.Intn(256))
			}
			w.blobs = append(w.blobs, buf)
		}
		if n := r.Intn(6); n > 0 {
			w.strMap = make(map[string]int64, n)
			for i := 0; i < n; i++ {
				k := []string{"", "a", "b", "key" + strconv.Itoa(r.Intn(20))}[r.Intn(4)]
				v := int64(r.Intn(1<<20) - 1<<19)
				if r.Intn(5) == 0 {
					v = math.MinInt64
				}
				w.strMap[k] = v
			}
		}
		if n := r.Intn(6); n > 0 {
			w.intMap = make(map[int32]string, n)
			for i := 0; i < n; i++ {
				k := []int32{math.MinInt32, -1, 0, 1, math.MaxInt32, int32(r.Intn(100))}[r.Intn(6)]
				w.intMap[k] = "v" + strconv.Itoa(r.Intn(100))
			}
		}
		wenc := encodeMsg(w)
		wgot, err := decode(wenc, false)
		ckSeed(err == nil, "random decode")
		if err != nil {
			continue
		}
		ckSeed(equalMsg(wgot, w), "random roundtrip")
		ckSeed(bytes.Equal(encodeMsg(wgot), wenc), "random re-encode")
		if round%25 == 24 {
			gcStress()
			ckSeed(equalMsg(wgot, w), "random survives GC")
		}
	}

	finish("l10_map_slice_decoder")
}
