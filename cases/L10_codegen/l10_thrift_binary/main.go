// L10: Thrift binary 协议风格编解码：严格版本头、结构化字段、list/map/set、
// 嵌套 struct、未知字段按类型 Skip、长度计算与截断/非法输入拒绝，GC 贯穿编解码。
package main

import (
	"bytes"
	"encoding/binary"
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

// Thrift binary protocol type ids.
const (
	tStop   byte = 0
	tBool   byte = 1
	tByte   byte = 2
	tI16    byte = 3
	tI32    byte = 4
	tI64    byte = 5
	tDouble byte = 6
	tString byte = 11
	tStruct byte = 12
	tMap    byte = 13
	tSet    byte = 14
	tList   byte = 15
)

const (
	version1 = uint32(0x80010000)
	msgCall  = byte(1)
	msgReply = byte(2)
	maxDepth = 20
	maxColl  = 1 << 15
)

var (
	errTruncated = errors.New("thrift: truncated")
	errVersion   = errors.New("thrift: bad version")
	errMsgType   = errors.New("thrift: bad message type")
	errType      = errors.New("thrift: bad type")
	errBool      = errors.New("thrift: bad bool")
	errSize      = errors.New("thrift: bad size")
	errDepth     = errors.New("thrift: too deep")
	errWire      = errors.New("thrift: wire mismatch")
	errTrailing  = errors.New("thrift: trailing bytes")
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

func envRounds() int {
	n := 1
	if s := os.Getenv("TORTURE_ROUNDS"); s != "" {
		if v, err := strconv.Atoi(s); err == nil && v > 0 {
			n = v
		}
	}
	if n > 100 {
		n = 100
	}
	return n
}

// ---- 编码辅助 ----

func putI16(b []byte, v int16) []byte { return binary.BigEndian.AppendUint16(b, uint16(v)) }
func putI32(b []byte, v int32) []byte { return binary.BigEndian.AppendUint32(b, uint32(v)) }
func putI64(b []byte, v int64) []byte { return binary.BigEndian.AppendUint64(b, uint64(v)) }
func putBool(b []byte, v bool) []byte {
	if v {
		return append(b, 1)
	}
	return append(b, 0)
}
func putDouble(b []byte, f float64) []byte {
	return binary.BigEndian.AppendUint64(b, math.Float64bits(f))
}
func putString(b []byte, s string) []byte {
	b = putI32(b, int32(len(s)))
	return append(b, s...)
}
func putBytes(b []byte, p []byte) []byte {
	b = putI32(b, int32(len(p)))
	return append(b, p...)
}

func fieldHdr(b []byte, typ byte, id int16) []byte {
	b = append(b, typ)
	return putI16(b, id)
}

// ---- 数据模型 ----

// extra 用于构造"未知字段"，验证解码端 Skip 覆盖所有类型。
type extra struct {
	Tag   string
	Ns    []int32
	Sub   *extra
	ID    int16
	Val   int64
	Ratio float64
	Flag  bool
	Blob  []byte
	Table map[string]int32
	Set   []int32
}

type record struct {
	ID     int64
	Code   int32
	Small  int16
	Flag   bool
	Ratio  float64
	Name   string
	Blob   []byte
	Values []int64
	Attrs  map[string]int32
	Child  *record
	Extras []extra
}

const (
	fID    = 1
	fCode  = 2
	fSmall = 3
	fFlag  = 4
	fRatio = 5
	fName  = 6
	fBlob  = 7
	fVals  = 8
	fAttrs = 9
	fChild = 10
	fExtra = 40 // 未知字段起始 id
)

func (r *record) appendTo(dst []byte, depth int) ([]byte, error) {
	if depth > maxDepth {
		return dst, errDepth
	}
	dst = fieldHdr(dst, tI64, fID)
	dst = putI64(dst, r.ID)
	dst = fieldHdr(dst, tI32, fCode)
	dst = putI32(dst, r.Code)
	dst = fieldHdr(dst, tI16, fSmall)
	dst = putI16(dst, r.Small)
	dst = fieldHdr(dst, tBool, fFlag)
	dst = putBool(dst, r.Flag)
	dst = fieldHdr(dst, tDouble, fRatio)
	dst = putDouble(dst, r.Ratio)
	dst = fieldHdr(dst, tString, fName)
	dst = putString(dst, r.Name)
	dst = fieldHdr(dst, tString, fBlob)
	dst = putBytes(dst, r.Blob)
	dst = fieldHdr(dst, tList, fVals)
	dst = append(dst, tI64)
	dst = putI32(dst, int32(len(r.Values)))
	for _, v := range r.Values {
		dst = putI64(dst, v)
	}
	dst = fieldHdr(dst, tMap, fAttrs)
	dst = append(dst, tString, tI32)
	dst = putI32(dst, int32(len(r.Attrs)))
	keys := make([]string, 0, len(r.Attrs))
	for k := range r.Attrs {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		dst = putString(dst, k)
		dst = putI32(dst, r.Attrs[k])
	}
	if r.Child != nil {
		dst = fieldHdr(dst, tStruct, fChild)
		var err error
		dst, err = r.Child.appendTo(dst, depth+1)
		if err != nil {
			return dst, err
		}
	}
	for i := range r.Extras {
		id := int16(fExtra + i)
		dst = appendExtra(dst, id, &r.Extras[i], depth)
	}
	return append(dst, tStop), nil
}

func appendExtra(dst []byte, id int16, e *extra, depth int) []byte {
	dst = fieldHdr(dst, tStruct, id)
	dst = fieldHdr(dst, tString, 1)
	dst = putString(dst, e.Tag)
	dst = fieldHdr(dst, tList, 2)
	dst = append(dst, tI32)
	dst = putI32(dst, int32(len(e.Ns)))
	for _, v := range e.Ns {
		dst = putI32(dst, v)
	}
	if e.Sub != nil && depth < maxDepth {
		dst = fieldHdr(dst, tStruct, 3)
		dst = appendExtraBody(dst, e.Sub, depth+1)
	}
	dst = fieldHdr(dst, tI16, 4)
	dst = putI16(dst, e.ID)
	dst = fieldHdr(dst, tI64, 5)
	dst = putI64(dst, e.Val)
	dst = fieldHdr(dst, tDouble, 6)
	dst = putDouble(dst, e.Ratio)
	dst = fieldHdr(dst, tBool, 7)
	dst = putBool(dst, e.Flag)
	dst = fieldHdr(dst, tString, 8)
	dst = putBytes(dst, e.Blob)
	dst = fieldHdr(dst, tMap, 9)
	dst = append(dst, tString, tI32)
	dst = putI32(dst, int32(len(e.Table)))
	keys := make([]string, 0, len(e.Table))
	for k := range e.Table {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		dst = putString(dst, k)
		dst = putI32(dst, e.Table[k])
	}
	dst = fieldHdr(dst, tSet, 10)
	dst = append(dst, tI32)
	dst = putI32(dst, int32(len(e.Set)))
	for _, v := range e.Set {
		dst = putI32(dst, v)
	}
	return append(dst, tStop)
}

func appendExtraBody(dst []byte, e *extra, depth int) []byte {
	dst = fieldHdr(dst, tString, 1)
	dst = putString(dst, e.Tag)
	dst = fieldHdr(dst, tBool, 2)
	dst = putBool(dst, e.Flag)
	dst = append(dst, tStop)
	return dst
}

// length 模拟生成的 Binary.Length：逐字段累加编码长度。
func (r *record) length(depth int) int {
	n := 0
	n += 3 + 8 // ID
	n += 3 + 4 // Code
	n += 3 + 2 // Small
	n += 3 + 1 // Flag
	n += 3 + 8 // Ratio
	n += 3 + 4 + len(r.Name)
	n += 3 + 4 + len(r.Blob)
	n += 3 + 1 + 4 + 8*len(r.Values)
	n += 3 + 1 + 1 + 4
	for k := range r.Attrs {
		n += 4 + len(k) + 4
	}
	if r.Child != nil {
		n += 3 + r.Child.length(depth+1)
	}
	for i := range r.Extras {
		n += 3 + extraLen(&r.Extras[i])
	}
	n++ // STOP
	return n
}

func extraLen(e *extra) int {
	n := 0
	n += 3 + 4 + len(e.Tag)
	n += 3 + 1 + 4 + 4*len(e.Ns)
	if e.Sub != nil {
		n += 3 + 3 + 4 + len(e.Sub.Tag) + 3 + 1 + 1
	}
	n += 3 + 2
	n += 3 + 8
	n += 3 + 8
	n += 3 + 1
	n += 3 + 4 + len(e.Blob)
	n += 3 + 1 + 1 + 4
	for k := range e.Table {
		n += 4 + len(k) + 4
	}
	n += 3 + 1 + 4 + 4*len(e.Set)
	n++ // STOP
	return n
}

func (r *record) marshal() ([]byte, error) {
	dst := make([]byte, 0, 256)
	dst = binary.BigEndian.AppendUint32(dst, version1|uint32(msgCall))
	dst = putString(dst, "ping")
	dst = putI32(dst, 7)
	start := len(dst)
	var err error
	dst, err = r.appendTo(dst, 0)
	if err != nil {
		return nil, err
	}
	if len(dst)-start != r.length(0) {
		return nil, errors.New("thrift: length mismatch")
	}
	return dst, nil
}

// ---- 解码辅助 ----

func getI16(b []byte) (int16, int, error) {
	if len(b) < 2 {
		return 0, 0, errTruncated
	}
	return int16(binary.BigEndian.Uint16(b)), 2, nil
}
func getI32(b []byte) (int32, int, error) {
	if len(b) < 4 {
		return 0, 0, errTruncated
	}
	return int32(binary.BigEndian.Uint32(b)), 4, nil
}
func getI64(b []byte) (int64, int, error) {
	if len(b) < 8 {
		return 0, 0, errTruncated
	}
	return int64(binary.BigEndian.Uint64(b)), 8, nil
}
func getDouble(b []byte) (float64, int, error) {
	v, n, err := getI64(b)
	return math.Float64frombits(uint64(v)), n, err
}
func getString(b []byte) (string, int, error) {
	ln, n, err := getI32(b)
	if err != nil {
		return "", 0, err
	}
	if ln < 0 || int64(ln) > int64(len(b)-n) {
		return "", 0, errTruncated
	}
	return string(b[n : n+int(ln)]), n + int(ln), nil
}
func getBlob(b []byte) ([]byte, int, error) {
	ln, n, err := getI32(b)
	if err != nil {
		return nil, 0, err
	}
	if ln < 0 || int64(ln) > int64(len(b)-n) {
		return nil, 0, errTruncated
	}
	out := make([]byte, ln)
	copy(out, b[n:n+int(ln)])
	return out, n + int(ln), nil
}
func getBool(b []byte) (bool, int, error) {
	if len(b) < 1 {
		return false, 0, errTruncated
	}
	switch b[0] {
	case 0:
		return false, 1, nil
	case 1:
		return true, 1, nil
	default:
		return false, 0, errBool
	}
}

// skipValue 模拟生成代码的 Skip：未知字段按类型跳过。
func skipValue(typ byte, b []byte, depth int) (int, error) {
	if depth > maxDepth {
		return 0, errDepth
	}
	switch typ {
	case tBool, tByte:
		if len(b) < 1 {
			return 0, errTruncated
		}
		return 1, nil
	case tI16:
		if len(b) < 2 {
			return 0, errTruncated
		}
		return 2, nil
	case tI32:
		if len(b) < 4 {
			return 0, errTruncated
		}
		return 4, nil
	case tI64, tDouble:
		if len(b) < 8 {
			return 0, errTruncated
		}
		return 8, nil
	case tString:
		_, n, err := getString(b)
		return n, err
	case tStruct:
		off := 0
		for {
			if off >= len(b) {
				return 0, errTruncated
			}
			ft := b[off]
			off++
			if ft == tStop {
				return off, nil
			}
			if off+2 > len(b) {
				return 0, errTruncated
			}
			off += 2
			n, err := skipValue(ft, b[off:], depth+1)
			if err != nil {
				return 0, err
			}
			off += n
		}
	case tList, tSet:
		if len(b) < 5 {
			return 0, errTruncated
		}
		et := b[0]
		sz := int32(binary.BigEndian.Uint32(b[1:5]))
		if sz < 0 || sz > maxColl {
			return 0, errSize
		}
		off := 5
		for i := int32(0); i < sz; i++ {
			n, err := skipValue(et, b[off:], depth+1)
			if err != nil {
				return 0, err
			}
			off += n
		}
		return off, nil
	case tMap:
		if len(b) < 6 {
			return 0, errTruncated
		}
		kt, vt := b[0], b[1]
		sz := int32(binary.BigEndian.Uint32(b[2:6]))
		if sz < 0 || sz > maxColl {
			return 0, errSize
		}
		off := 6
		for i := int32(0); i < sz; i++ {
			n, err := skipValue(kt, b[off:], depth+1)
			if err != nil {
				return 0, err
			}
			off += n
			n, err = skipValue(vt, b[off:], depth+1)
			if err != nil {
				return 0, err
			}
			off += n
		}
		return off, nil
	default:
		return 0, errType
	}
}

func decodeStruct(b []byte, depth int) (*record, int, error) {
	if depth > maxDepth {
		return nil, 0, errDepth
	}
	r := &record{}
	off := 0
	for {
		if off >= len(b) {
			return nil, 0, errTruncated
		}
		typ := b[off]
		off++
		if typ == tStop {
			return r, off, nil
		}
		if off+2 > len(b) {
			return nil, 0, errTruncated
		}
		id := int16(binary.BigEndian.Uint16(b[off:]))
		off += 2
		rest := b[off:]
		var n int
		var err error
		switch id {
		case fID:
			if typ != tI64 {
				return nil, 0, errWire
			}
			r.ID, n, err = getI64(rest)
		case fCode:
			if typ != tI32 {
				return nil, 0, errWire
			}
			r.Code, n, err = getI32(rest)
		case fSmall:
			if typ != tI16 {
				return nil, 0, errWire
			}
			r.Small, n, err = getI16(rest)
		case fFlag:
			if typ != tBool {
				return nil, 0, errWire
			}
			r.Flag, n, err = getBool(rest)
		case fRatio:
			if typ != tDouble {
				return nil, 0, errWire
			}
			r.Ratio, n, err = getDouble(rest)
		case fName:
			if typ != tString {
				return nil, 0, errWire
			}
			r.Name, n, err = getString(rest)
		case fBlob:
			if typ != tString {
				return nil, 0, errWire
			}
			r.Blob, n, err = getBlob(rest)
		case fVals:
			if typ != tList || len(rest) < 5 || rest[0] != tI64 {
				return nil, 0, errWire
			}
			sz := int32(binary.BigEndian.Uint32(rest[1:5]))
			if sz < 0 || sz > maxColl {
				return nil, 0, errSize
			}
			n = 5
			for i := int32(0); i < sz; i++ {
				var v int64
				var m int
				v, m, err = getI64(rest[n:])
				if err != nil {
					break
				}
				n += m
				r.Values = append(r.Values, v)
			}
		case fAttrs:
			if typ != tMap || len(rest) < 6 || rest[0] != tString || rest[1] != tI32 {
				return nil, 0, errWire
			}
			sz := int32(binary.BigEndian.Uint32(rest[2:6]))
			if sz < 0 || sz > maxColl {
				return nil, 0, errSize
			}
			r.Attrs = make(map[string]int32, sz)
			n = 6
			for i := int32(0); i < sz; i++ {
				var k string
				var m int
				k, m, err = getString(rest[n:])
				if err != nil {
					break
				}
				n += m
				var v int32
				v, m, err = getI32(rest[n:])
				if err != nil {
					break
				}
				n += m
				r.Attrs[k] = v
			}
		case fChild:
			if typ != tStruct {
				return nil, 0, errWire
			}
			var child *record
			var m int
			child, m, err = decodeStruct(rest, depth+1)
			if err == nil {
				r.Child = child
				n = m
			}
		default:
			n, err = skipValue(typ, rest, depth+1)
		}
		if err != nil {
			return nil, 0, err
		}
		off += n
	}
}

func decodeMessage(b []byte) (*record, error) {
	if len(b) < 4 {
		return nil, errTruncated
	}
	hdr := binary.BigEndian.Uint32(b)
	if hdr&0xffff0000 != version1 {
		return nil, errVersion
	}
	if byte(hdr&0xff) != msgCall {
		return nil, errMsgType
	}
	off := 4
	_, n, err := getString(b[off:])
	if err != nil {
		return nil, err
	}
	off += n
	if off+4 > len(b) {
		return nil, errTruncated
	}
	off += 4 // seqid
	r, n, err := decodeStruct(b[off:], 0)
	if err != nil {
		return nil, err
	}
	off += n
	if off != len(b) {
		return nil, errTrailing
	}
	return r, nil
}

// ---- 比较 ----

func eqRecord(a, b *record) bool {
	if a == nil || b == nil {
		return a == b
	}
	if a.ID != b.ID || a.Code != b.Code || a.Small != b.Small || a.Flag != b.Flag ||
		math.Float64bits(a.Ratio) != math.Float64bits(b.Ratio) ||
		a.Name != b.Name || !bytes.Equal(a.Blob, b.Blob) {
		return false
	}
	if len(a.Values) != len(b.Values) {
		return false
	}
	for i := range a.Values {
		if a.Values[i] != b.Values[i] {
			return false
		}
	}
	if len(a.Attrs) != len(b.Attrs) {
		return false
	}
	for k, v := range a.Attrs {
		if b.Attrs[k] != v {
			return false
		}
	}
	return eqRecord(a.Child, b.Child)
}

var floats = []float64{0, math.Copysign(0, -1), 1.5, -2.25, 1e300, math.Inf(1), math.Inf(-1)}

func randExtra(r *rand.Rand, depth int) extra {
	e := extra{
		Tag:   fmt.Sprintf("x-%d", r.Intn(1000)),
		ID:    int16(r.Intn(1 << 15)),
		Val:   r.Int63n(1 << 50),
		Ratio: floats[r.Intn(len(floats))],
		Flag:  r.Intn(2) == 0,
	}
	for i := 0; i < r.Intn(4); i++ {
		e.Ns = append(e.Ns, int32(r.Intn(1<<20)))
	}
	e.Blob = make([]byte, r.Intn(12))
	for i := range e.Blob {
		e.Blob[i] = byte(r.Intn(256))
	}
	if r.Intn(3) == 0 {
		e.Table = map[string]int32{}
		for i := 0; i < r.Intn(4); i++ {
			e.Table[fmt.Sprintf("k%d", r.Intn(50))] = int32(r.Intn(10000))
		}
	}
	for i := 0; i < r.Intn(4); i++ {
		e.Set = append(e.Set, int32(r.Intn(1000)))
	}
	if depth > 0 && r.Intn(3) == 0 {
		sub := randExtra(r, depth-1)
		e.Sub = &sub
	}
	return e
}

func randRecord(r *rand.Rand, depth int) *record {
	m := &record{
		ID:    r.Int63n(1<<60) - (1 << 59),
		Code:  int32(r.Intn(1 << 31)),
		Small: int16(r.Intn(1 << 16)),
		Flag:  r.Intn(2) == 0,
		Ratio: floats[r.Intn(len(floats))],
		Name:  fmt.Sprintf("rec-%d", r.Intn(100000)),
	}
	m.Blob = make([]byte, r.Intn(24))
	for i := range m.Blob {
		m.Blob[i] = byte(r.Intn(256))
	}
	for i := 0; i < r.Intn(6); i++ {
		m.Values = append(m.Values, r.Int63n(1<<48)-(1<<47))
	}
	if r.Intn(3) != 0 {
		m.Attrs = map[string]int32{}
		for i := 0; i < r.Intn(5); i++ {
			m.Attrs[fmt.Sprintf("a%d", r.Intn(40))] = int32(r.Intn(1 << 30))
		}
	}
	if depth > 0 && r.Intn(3) == 0 {
		m.Child = randRecord(r, depth-1)
	}
	for i := 0; i < r.Intn(4); i++ {
		m.Extras = append(m.Extras, randExtra(r, 1))
	}
	return m
}

func main() {
	curSeed = 1
	if s := os.Getenv("UAF_SEED"); s != "" {
		if v, err := strconv.ParseInt(s, 10, 64); err == nil {
			curSeed = v
		}
	}
	tRounds := envRounds()

	// 1. 固定记录：严格版本头、长度计算、roundtrip。
	fixed := &record{
		ID: -1234567890123, Code: -42, Small: -32768, Flag: true,
		Ratio: -2.25, Name: "fixed-record",
		Blob:   []byte{0, 1, 2, 0xfe, 0xff},
		Values: []int64{math.MinInt64, -1, 0, 1, math.MaxInt64},
		Attrs:  map[string]int32{"b": 2, "a": 1, "c": -3},
		Child: &record{ID: 7, Code: 8, Small: 9, Flag: false, Ratio: 1.5,
			Name: "kid", Blob: []byte{}, Values: []int64{-5, 6},
			Attrs: map[string]int32{"z": 26}},
		Extras: []extra{{
			Tag: "unknown", Ns: []int32{1, -2, 3}, ID: -9, Val: 1 << 40,
			Ratio: math.Inf(1), Flag: true, Blob: []byte{9, 8},
			Table: map[string]int32{"q": 7}, Set: []int32{4, 5},
			Sub: &extra{Tag: "deep", Flag: true},
		}},
	}
	enc, err := fixed.marshal()
	ck(err == nil, "fixed marshal")
	ck(len(enc) >= 4 && bytes.Equal(enc[:4], []byte{0x80, 0x01, 0x00, 0x01}), "strict version header")
	ck(int32(binary.BigEndian.Uint32(enc[4:8])) == 4 && string(enc[8:12]) == "ping", "method name")
	ck(len(enc) == 4+4+4+4+fixed.length(0), "Binary.Length equals encoded length")

	// GC 后将编码缓冲作为唯一来源解码。
	retained := [][]byte{enc}
	gcStress()
	got, err := decodeMessage(enc)
	ck(err == nil, "fixed decode after GC")
	ck(eqRecord(fixed, got), "fixed roundtrip")
	ck(got.Child != nil && got.Child.Blob != nil && len(got.Child.Blob) == 0, "empty blob preserved")

	// 2. 解码必须拷贝 string/bytes：改写输入缓冲后不受影响。
	copied, err := decodeMessage(enc)
	ck(err == nil, "copy source decode")
	for i := range enc {
		enc[i] = 0xaa
	}
	ck(eqRecord(fixed, copied), "decoded value independent of input buffer")
	gcStress()
	ck(eqRecord(fixed, copied), "copied payload survives GC")

	// 3. 未知字段全部类型 Skip；记录带未知字段仍可解码。
	base := &record{ID: 1, Code: 2, Small: 3, Name: "base", Attrs: map[string]int32{"k": 1}}
	baseEnc, err := base.marshal()
	ck(err == nil, "base marshal")
	withUnknown := &record{ID: 1, Code: 2, Small: 3, Name: "base", Attrs: map[string]int32{"k": 1},
		Extras: []extra{
			{Tag: "u1", Ns: []int32{1, 2}, ID: 5, Val: -6, Ratio: 1.5, Flag: true,
				Blob: []byte{1}, Table: map[string]int32{"a": 1}, Set: []int32{2}},
			{Tag: "u2", Sub: &extra{Tag: "nested", Flag: false}},
		}}
	unkEnc, err := withUnknown.marshal()
	ck(err == nil, "unknown marshal")
	ck(len(unkEnc) > len(baseEnc), "unknown fields enlarge encoding")
	got, err = decodeMessage(unkEnc)
	ck(err == nil, "unknown fields skipped")
	ck(eqRecord(base, got), "known fields intact with unknown fields present")

	// 4. 任意前缀截断都必须报错且不 panic；非法输入逐类拒绝。
	full, err := fixed.marshal()
	ck(err == nil, "fixed re-marshal")
	bad := 0
	for i := 0; i < len(full); i++ {
		if _, err := decodeMessage(full[:i]); err == nil {
			bad++
		}
	}
	ck(bad == 0, "every truncated prefix rejected")
	badVer := append([]byte(nil), full...)
	badVer[0] = 0x00
	_, err = decodeMessage(badVer)
	ck(errors.Is(err, errVersion), "bad version rejected")
	badMsg := append([]byte(nil), full...)
	badMsg[3] = 9
	_, err = decodeMessage(badMsg)
	ck(errors.Is(err, errMsgType), "bad message type rejected")
	badBool := make([]byte, 0, 64)
	badBool = binary.BigEndian.AppendUint32(badBool, version1|uint32(msgCall))
	badBool = putString(badBool, "x")
	badBool = putI32(badBool, 1)
	badBool = fieldHdr(badBool, tBool, fFlag)
	badBool = append(badBool, 2)
	badBool = append(badBool, tStop)
	_, err = decodeMessage(badBool)
	ck(errors.Is(err, errBool), "bad bool value rejected")
	badLen := make([]byte, 0, 64)
	badLen = binary.BigEndian.AppendUint32(badLen, version1|uint32(msgCall))
	badLen = putString(badLen, "x")
	badLen = putI32(badLen, 1)
	badLen = fieldHdr(badLen, tString, fName)
	badLen = putI32(badLen, -1)
	badLen = append(badLen, tStop)
	_, err = decodeMessage(badLen)
	ck(errors.Is(err, errTruncated), "negative string length rejected")
	badType := make([]byte, 0, 64)
	badType = binary.BigEndian.AppendUint32(badType, version1|uint32(msgCall))
	badType = putString(badType, "x")
	badType = putI32(badType, 1)
	badType = append(badType, 7) // 非法 type id
	badType = putI16(badType, 41)
	badType = append(badType, tStop)
	_, err = decodeMessage(badType)
	ck(errors.Is(err, errType), "bad field type rejected")
	extraTrailing := append(append([]byte(nil), full...), 0)
	_, err = decodeMessage(extraTrailing)
	ck(errors.Is(err, errTrailing), "trailing bytes rejected")

	// 5. 随机消息 × UAF_SEED；长度自校验、GC 穿插、保留缓冲重放。
	rng := rand.New(rand.NewSource(curSeed))
	var retainedBufs [][]byte
	rounds := 160 * tRounds
	for i := 0; i < rounds; i++ {
		src := randRecord(rng, 3)
		b, err := src.marshal()
		ckSeed(err == nil, "random marshal")
		if err != nil {
			continue
		}
		ckSeed(len(b) == 4+4+4+4+src.length(0), "random length")
		if i%17 == 0 {
			retainedBufs = append(retainedBufs, b)
			gcStress()
		}
		dst, err := decodeMessage(b)
		ckSeed(err == nil, "random decode")
		ckSeed(eqRecord(src, dst), "random roundtrip")
	}
	gcSink = append(gcSink, retainedBufs...)
	runtime.GC()
	for i, b := range retainedBufs {
		if _, err := decodeMessage(b); err != nil {
			ckSeed(false, fmt.Sprintf("retained decode %d: %v", i, err))
		}
	}
	_ = retained

	finish("l10_thrift_binary")
}
