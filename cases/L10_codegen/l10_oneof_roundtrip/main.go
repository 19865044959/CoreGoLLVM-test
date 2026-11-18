// L10: 16 分支 oneof 的 marshal 往返：字段号/线型表、字节级 golden、last-one-wins、
// typed-nil 视为未设置、未知字段跳过、截断拒绝、追加缓冲复用与 GC 保活。
package main

import (
	"bytes"
	"encoding/binary"
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

var (
	errTruncated = errors.New("oneof: truncated")
	errVarint    = errors.New("oneof: bad varint")
	errWire      = errors.New("oneof: bad wire")
	errBool      = errors.New("oneof: bad bool")
	errDepth     = errors.New("oneof: too deep")
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

// ---- oneof 分支定义：16 个包装类型 ----

type oneofEnum int32

const (
	oneofEnumA oneofEnum = 0
	oneofEnumB oneofEnum = 1
	oneofEnumC oneofEnum = 2
)

type oneofChild struct {
	Name  string
	Value int64
}

type isOneof interface {
	field() (int, int) // field number, wire type
}

type (
	ovText    struct{ Value string }
	ovInt32   struct{ Value int32 }
	ovInt64   struct{ Value int64 }
	ovBool    struct{ Value bool }
	ovBlob    struct{ Value []byte }
	ovFloat64 struct{ Value float64 }
	ovChild   struct{ Value *oneofChild }
	ovUint32  struct{ Value uint32 }
	ovUint64  struct{ Value uint64 }
	ovEnum    struct{ Value oneofEnum }
	ovList    struct{ Value []int64 }
	ovTag     struct{ Value string }
	ovData    struct{ Value []byte }
	ovFloat32 struct{ Value float32 }
	ovCount   struct{ Value int32 }
	ovNote    struct{ Value string }
)

func (*ovText) field() (int, int)    { return 1, 2 }
func (*ovInt32) field() (int, int)   { return 2, 0 }
func (*ovInt64) field() (int, int)   { return 3, 0 }
func (*ovBool) field() (int, int)    { return 4, 0 }
func (*ovBlob) field() (int, int)    { return 5, 2 }
func (*ovFloat64) field() (int, int) { return 6, 1 }
func (*ovChild) field() (int, int)   { return 7, 2 }
func (*ovUint32) field() (int, int)  { return 8, 0 }
func (*ovUint64) field() (int, int)  { return 9, 0 }
func (*ovEnum) field() (int, int)    { return 10, 0 }
func (*ovList) field() (int, int)    { return 11, 2 }
func (*ovTag) field() (int, int)     { return 12, 2 }
func (*ovData) field() (int, int)    { return 13, 2 }
func (*ovFloat32) field() (int, int) { return 14, 5 }
func (*ovCount) field() (int, int)   { return 15, 0 }
func (*ovNote) field() (int, int)    { return 16, 2 }

type oneofMsg struct {
	Which isOneof
}

func (m *oneofMsg) WhichField() int {
	if m == nil || m.Which == nil {
		return 0
	}
	// typed-nil 包装（如 (*ovText)(nil)）视为未设置。
	if v := reflect.ValueOf(m.Which); v.Kind() == reflect.Ptr && v.IsNil() {
		return 0
	}
	n, _ := m.Which.field()
	return n
}

func (m *oneofMsg) Clear() { m.Which = nil }

func (m *oneofMsg) GetText() string {
	if m != nil {
		if w, ok := m.Which.(*ovText); ok && w != nil {
			return w.Value
		}
	}
	return ""
}
func (m *oneofMsg) GetInt32() int32 {
	if m != nil {
		if w, ok := m.Which.(*ovInt32); ok && w != nil {
			return w.Value
		}
	}
	return 0
}
func (m *oneofMsg) GetInt64() int64 {
	if m != nil {
		if w, ok := m.Which.(*ovInt64); ok && w != nil {
			return w.Value
		}
	}
	return 0
}
func (m *oneofMsg) GetBool() bool {
	if m != nil {
		if w, ok := m.Which.(*ovBool); ok && w != nil {
			return w.Value
		}
	}
	return false
}
func (m *oneofMsg) GetBlob() []byte {
	if m != nil {
		if w, ok := m.Which.(*ovBlob); ok && w != nil {
			return w.Value
		}
	}
	return nil
}
func (m *oneofMsg) GetFloat64() float64 {
	if m != nil {
		if w, ok := m.Which.(*ovFloat64); ok && w != nil {
			return w.Value
		}
	}
	return 0
}
func (m *oneofMsg) GetChild() *oneofChild {
	if m != nil {
		if w, ok := m.Which.(*ovChild); ok && w != nil {
			return w.Value
		}
	}
	return nil
}
func (m *oneofMsg) GetUint32() uint32 {
	if m != nil {
		if w, ok := m.Which.(*ovUint32); ok && w != nil {
			return w.Value
		}
	}
	return 0
}
func (m *oneofMsg) GetUint64() uint64 {
	if m != nil {
		if w, ok := m.Which.(*ovUint64); ok && w != nil {
			return w.Value
		}
	}
	return 0
}
func (m *oneofMsg) GetEnum() oneofEnum {
	if m != nil {
		if w, ok := m.Which.(*ovEnum); ok && w != nil {
			return w.Value
		}
	}
	return 0
}
func (m *oneofMsg) GetList() []int64 {
	if m != nil {
		if w, ok := m.Which.(*ovList); ok && w != nil {
			return w.Value
		}
	}
	return nil
}
func (m *oneofMsg) GetTag() string {
	if m != nil {
		if w, ok := m.Which.(*ovTag); ok && w != nil {
			return w.Value
		}
	}
	return ""
}
func (m *oneofMsg) GetData() []byte {
	if m != nil {
		if w, ok := m.Which.(*ovData); ok && w != nil {
			return w.Value
		}
	}
	return nil
}
func (m *oneofMsg) GetFloat32() float32 {
	if m != nil {
		if w, ok := m.Which.(*ovFloat32); ok && w != nil {
			return w.Value
		}
	}
	return 0
}
func (m *oneofMsg) GetCount() int32 {
	if m != nil {
		if w, ok := m.Which.(*ovCount); ok && w != nil {
			return w.Value
		}
	}
	return 0
}
func (m *oneofMsg) GetNote() string {
	if m != nil {
		if w, ok := m.Which.(*ovNote); ok && w != nil {
			return w.Value
		}
	}
	return ""
}

func (m *oneofMsg) HasBlob() bool {
	if m != nil {
		w, ok := m.Which.(*ovBlob)
		return ok && w != nil
	}
	return false
}
func (m *oneofMsg) HasChild() bool {
	if m != nil {
		w, ok := m.Which.(*ovChild)
		return ok && w != nil
	}
	return false
}

// ---- 编码 ----

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

func zigzag(v int64) uint64   { return uint64(v<<1) ^ uint64(v>>63) }
func unzigzag(v uint64) int64 { return int64(v>>1) ^ -int64(v&1) }

func appendString(b []byte, s string) []byte {
	b = appendVarint(b, uint64(len(s)))
	return append(b, s...)
}

func appendBytes(b []byte, p []byte) []byte {
	b = appendVarint(b, uint64(len(p)))
	return append(b, p...)
}

func encodeChild(c *oneofChild, depth int) ([]byte, error) {
	if c == nil {
		return nil, nil
	}
	if depth > 8 {
		return nil, errDepth
	}
	var b []byte
	if c.Name != "" {
		b = appendVarint(b, 1<<3|2)
		b = appendString(b, c.Name)
	}
	if c.Value != 0 {
		b = appendVarint(b, 2<<3)
		b = appendVarint(b, zigzag(c.Value))
	}
	return b, nil
}

// MarshalAppend 只编码当前分支；typed-nil 包装视为未设置。
func (m *oneofMsg) MarshalAppend(dst []byte) ([]byte, error) {
	if m == nil {
		return dst, nil
	}
	switch w := m.Which.(type) {
	case nil:
		return dst, nil
	case *ovText:
		if w == nil {
			return dst, nil
		}
		dst = appendVarint(dst, 1<<3|2)
		dst = appendString(dst, w.Value)
	case *ovInt32:
		if w == nil {
			return dst, nil
		}
		dst = appendVarint(dst, 2<<3)
		dst = appendVarint(dst, uint64(int64(w.Value)))
	case *ovInt64:
		if w == nil {
			return dst, nil
		}
		dst = appendVarint(dst, 3<<3)
		dst = appendVarint(dst, zigzag(w.Value))
	case *ovBool:
		if w == nil {
			return dst, nil
		}
		dst = appendVarint(dst, 4<<3)
		if w.Value {
			dst = append(dst, 1)
		} else {
			dst = append(dst, 0)
		}
	case *ovBlob:
		if w == nil {
			return dst, nil
		}
		dst = appendVarint(dst, 5<<3|2)
		dst = appendBytes(dst, w.Value)
	case *ovFloat64:
		if w == nil {
			return dst, nil
		}
		dst = appendVarint(dst, 6<<3|1)
		var buf [8]byte
		binary.LittleEndian.PutUint64(buf[:], math.Float64bits(w.Value))
		dst = append(dst, buf[:]...)
	case *ovChild:
		if w == nil {
			return dst, nil
		}
		body, err := encodeChild(w.Value, 0)
		if err != nil {
			return dst, err
		}
		dst = appendVarint(dst, 7<<3|2)
		dst = appendBytes(dst, body)
	case *ovUint32:
		if w == nil {
			return dst, nil
		}
		dst = appendVarint(dst, 8<<3)
		dst = appendVarint(dst, uint64(w.Value))
	case *ovUint64:
		if w == nil {
			return dst, nil
		}
		dst = appendVarint(dst, 9<<3)
		dst = appendVarint(dst, w.Value)
	case *ovEnum:
		if w == nil {
			return dst, nil
		}
		dst = appendVarint(dst, 10<<3)
		dst = appendVarint(dst, uint64(int64(w.Value)))
	case *ovList:
		if w == nil {
			return dst, nil
		}
		var packed []byte
		for _, v := range w.Value {
			packed = appendVarint(packed, zigzag(v))
		}
		dst = appendVarint(dst, 11<<3|2)
		dst = appendBytes(dst, packed)
	case *ovTag:
		if w == nil {
			return dst, nil
		}
		dst = appendVarint(dst, 12<<3|2)
		dst = appendString(dst, w.Value)
	case *ovData:
		if w == nil {
			return dst, nil
		}
		dst = appendVarint(dst, 13<<3|2)
		dst = appendBytes(dst, w.Value)
	case *ovFloat32:
		if w == nil {
			return dst, nil
		}
		dst = appendVarint(dst, 14<<3|5)
		var buf [4]byte
		binary.LittleEndian.PutUint32(buf[:], math.Float32bits(w.Value))
		dst = append(dst, buf[:]...)
	case *ovCount:
		if w == nil {
			return dst, nil
		}
		dst = appendVarint(dst, 15<<3)
		dst = appendVarint(dst, uint64(int64(w.Value)))
	case *ovNote:
		if w == nil {
			return dst, nil
		}
		dst = appendVarint(dst, 16<<3|2)
		dst = appendString(dst, w.Value)
	default:
		return dst, errWire
	}
	return dst, nil
}

func (m *oneofMsg) Marshal() ([]byte, error) {
	return m.MarshalAppend(nil)
}

func copyBytes(b []byte) []byte {
	out := make([]byte, len(b))
	copy(out, b)
	return out
}

func readLenBytes(b []byte) ([]byte, int, error) {
	ln, n, err := readVarint(b)
	if err != nil {
		return nil, 0, err
	}
	if ln > uint64(len(b)-n) {
		return nil, 0, errTruncated
	}
	return copyBytes(b[n : n+int(ln)]), n + int(ln), nil
}

func skipField(wire int, b []byte) (int, error) {
	switch wire {
	case 0:
		_, n, err := readVarint(b)
		return n, err
	case 1:
		if len(b) < 8 {
			return 0, errTruncated
		}
		return 8, nil
	case 2:
		ln, n, err := readVarint(b)
		if err != nil {
			return 0, err
		}
		if ln > uint64(len(b)-n) {
			return 0, errTruncated
		}
		return n + int(ln), nil
	case 5:
		if len(b) < 4 {
			return 0, errTruncated
		}
		return 4, nil
	default:
		return 0, errWire
	}
}

func decodeChild(b []byte, depth int) (*oneofChild, error) {
	if depth > 8 {
		return nil, errDepth
	}
	c := &oneofChild{}
	for len(b) > 0 {
		key, n, err := readVarint(b)
		if err != nil {
			return nil, err
		}
		b = b[n:]
		f, wire := int(key>>3), int(key&7)
		switch f {
		case 1:
			if wire != 2 {
				return nil, errWire
			}
			s, n, err := readLenBytes(b)
			if err != nil {
				return nil, err
			}
			c.Name = string(s)
			b = b[n:]
		case 2:
			if wire != 0 {
				return nil, errWire
			}
			v, n, err := readVarint(b)
			if err != nil {
				return nil, err
			}
			c.Value = unzigzag(v)
			b = b[n:]
		default:
			n, err := skipField(wire, b)
			if err != nil {
				return nil, err
			}
			b = b[n:]
		}
	}
	return c, nil
}

func decodeOneof(b []byte, depth int) (*oneofMsg, error) {
	if depth > 8 {
		return nil, errDepth
	}
	m := &oneofMsg{}
	for len(b) > 0 {
		key, n, err := readVarint(b)
		if err != nil {
			return nil, err
		}
		b = b[n:]
		f, wire := int(key>>3), int(key&7)
		switch f {
		case 1:
			if wire != 2 {
				return nil, errWire
			}
			v, n, err := readLenBytes(b)
			if err != nil {
				return nil, err
			}
			m.Which = &ovText{Value: string(v)}
			b = b[n:]
		case 2:
			if wire != 0 {
				return nil, errWire
			}
			v, n, err := readVarint(b)
			if err != nil {
				return nil, err
			}
			m.Which = &ovInt32{Value: int32(v)}
			b = b[n:]
		case 3:
			if wire != 0 {
				return nil, errWire
			}
			v, n, err := readVarint(b)
			if err != nil {
				return nil, err
			}
			m.Which = &ovInt64{Value: unzigzag(v)}
			b = b[n:]
		case 4:
			if wire != 0 {
				return nil, errWire
			}
			v, n, err := readVarint(b)
			if err != nil {
				return nil, err
			}
			if v > 1 {
				return nil, errBool
			}
			m.Which = &ovBool{Value: v == 1}
			b = b[n:]
		case 5:
			if wire != 2 {
				return nil, errWire
			}
			v, n, err := readLenBytes(b)
			if err != nil {
				return nil, err
			}
			m.Which = &ovBlob{Value: v}
			b = b[n:]
		case 6:
			if wire != 1 || len(b) < 8 {
				return nil, errTruncated
			}
			m.Which = &ovFloat64{Value: math.Float64frombits(binary.LittleEndian.Uint64(b))}
			b = b[8:]
		case 7:
			if wire != 2 {
				return nil, errWire
			}
			body, n, err := readLenBytes(b)
			if err != nil {
				return nil, err
			}
			c, err := decodeChild(body, depth+1)
			if err != nil {
				return nil, err
			}
			m.Which = &ovChild{Value: c}
			b = b[n:]
		case 8:
			if wire != 0 {
				return nil, errWire
			}
			v, n, err := readVarint(b)
			if err != nil {
				return nil, err
			}
			m.Which = &ovUint32{Value: uint32(v)}
			b = b[n:]
		case 9:
			if wire != 0 {
				return nil, errWire
			}
			v, n, err := readVarint(b)
			if err != nil {
				return nil, err
			}
			m.Which = &ovUint64{Value: v}
			b = b[n:]
		case 10:
			if wire != 0 {
				return nil, errWire
			}
			v, n, err := readVarint(b)
			if err != nil {
				return nil, err
			}
			m.Which = &ovEnum{Value: oneofEnum(int32(v))}
			b = b[n:]
		case 11:
			if wire != 2 {
				return nil, errWire
			}
			pb, n, err := readLenBytes(b)
			if err != nil {
				return nil, err
			}
			vals := []int64{}
			for len(pb) > 0 {
				v, m2, err := readVarint(pb)
				if err != nil {
					return nil, err
				}
				vals = append(vals, unzigzag(v))
				pb = pb[m2:]
			}
			m.Which = &ovList{Value: vals}
			b = b[n:]
		case 12:
			if wire != 2 {
				return nil, errWire
			}
			v, n, err := readLenBytes(b)
			if err != nil {
				return nil, err
			}
			m.Which = &ovTag{Value: string(v)}
			b = b[n:]
		case 13:
			if wire != 2 {
				return nil, errWire
			}
			v, n, err := readLenBytes(b)
			if err != nil {
				return nil, err
			}
			m.Which = &ovData{Value: v}
			b = b[n:]
		case 14:
			if wire != 5 || len(b) < 4 {
				return nil, errTruncated
			}
			m.Which = &ovFloat32{Value: math.Float32frombits(binary.LittleEndian.Uint32(b))}
			b = b[4:]
		case 15:
			if wire != 0 {
				return nil, errWire
			}
			v, n, err := readVarint(b)
			if err != nil {
				return nil, err
			}
			m.Which = &ovCount{Value: int32(v)}
			b = b[n:]
		case 16:
			if wire != 2 {
				return nil, errWire
			}
			v, n, err := readLenBytes(b)
			if err != nil {
				return nil, err
			}
			m.Which = &ovNote{Value: string(v)}
			b = b[n:]
		default:
			n, err := skipField(wire, b)
			if err != nil {
				return nil, err
			}
			b = b[n:]
		}
	}
	return m, nil
}

func sameList(a, b []int64) bool {
	if (a == nil) != (b == nil) || len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func sameOneof(a, b *oneofMsg) bool {
	if a == nil || b == nil {
		return a == b
	}
	fa, fb := a.WhichField(), b.WhichField()
	if fa != fb {
		return false
	}
	switch fa {
	case 0:
		return true
	case 1:
		return a.GetText() == b.GetText()
	case 2:
		return a.GetInt32() == b.GetInt32()
	case 3:
		return a.GetInt64() == b.GetInt64()
	case 4:
		return a.GetBool() == b.GetBool()
	case 5:
		x, y := a.GetBlob(), b.GetBlob()
		return (x == nil) == (y == nil) && bytes.Equal(x, y)
	case 6:
		return math.Float64bits(a.GetFloat64()) == math.Float64bits(b.GetFloat64())
	case 7:
		x, y := a.GetChild(), b.GetChild()
		if (x == nil) != (y == nil) {
			return false
		}
		if x == nil {
			return true
		}
		return x.Name == y.Name && x.Value == y.Value
	case 8:
		return a.GetUint32() == b.GetUint32()
	case 9:
		return a.GetUint64() == b.GetUint64()
	case 10:
		return a.GetEnum() == b.GetEnum()
	case 11:
		return sameList(a.GetList(), b.GetList())
	case 12:
		return a.GetTag() == b.GetTag()
	case 13:
		x, y := a.GetData(), b.GetData()
		return (x == nil) == (y == nil) && bytes.Equal(x, y)
	case 14:
		return math.Float32bits(a.GetFloat32()) == math.Float32bits(b.GetFloat32())
	case 15:
		return a.GetCount() == b.GetCount()
	case 16:
		return a.GetNote() == b.GetNote()
	}
	return false
}

func mustMarshal(m *oneofMsg) []byte {
	b, err := m.Marshal()
	if err != nil {
		panic(err)
	}
	return b
}

func randOneof(r *rand.Rand) *oneofMsg {
	switch r.Intn(20) {
	case 0:
		return &oneofMsg{}
	case 1:
		return &oneofMsg{Which: (*ovText)(nil)}
	case 2:
		return &oneofMsg{Which: &ovText{Value: fmt.Sprintf("t%d", r.Intn(1000))}}
	case 3:
		return &oneofMsg{Which: &ovInt32{Value: int32(r.Uint32())}}
	case 4:
		return &oneofMsg{Which: &ovInt64{Value: r.Int63n(1<<48) - (1 << 47)}}
	case 5:
		return &oneofMsg{Which: &ovBool{Value: r.Intn(2) == 0}}
	case 6:
		b := make([]byte, r.Intn(20))
		for i := range b {
			b[i] = byte(r.Intn(256))
		}
		return &oneofMsg{Which: &ovBlob{Value: b}}
	case 7:
		return &oneofMsg{Which: &ovFloat64{Value: math.Float64frombits(r.Uint64())}}
	case 8:
		c := &oneofChild{Name: fmt.Sprintf("c%d", r.Intn(100)), Value: r.Int63n(1<<40) - (1 << 39)}
		if r.Intn(4) == 0 {
			c = &oneofChild{}
		}
		return &oneofMsg{Which: &ovChild{Value: c}}
	case 9:
		return &oneofMsg{Which: &ovUint32{Value: r.Uint32()}}
	case 10:
		return &oneofMsg{Which: &ovUint64{Value: r.Uint64()}}
	case 11:
		return &oneofMsg{Which: &ovEnum{Value: oneofEnum(r.Intn(5) - 1)}}
	case 12:
		vals := make([]int64, r.Intn(8))
		for i := range vals {
			vals[i] = r.Int63n(1<<50) - (1 << 49)
		}
		return &oneofMsg{Which: &ovList{Value: vals}}
	case 13:
		return &oneofMsg{Which: &ovTag{Value: fmt.Sprintf("tag-%d", r.Intn(100))}}
	case 14:
		b := make([]byte, r.Intn(16))
		for i := range b {
			b[i] = byte(r.Intn(256))
		}
		return &oneofMsg{Which: &ovData{Value: b}}
	case 15:
		return &oneofMsg{Which: &ovFloat32{Value: math.Float32frombits(r.Uint32())}}
	case 16:
		return &oneofMsg{Which: &ovCount{Value: int32(r.Uint32())}}
	case 17:
		return &oneofMsg{Which: &ovNote{Value: fmt.Sprintf("note-%d", r.Intn(1000))}}
	case 18:
		return &oneofMsg{Which: &ovBlob{Value: []byte{}}}
	default:
		return &oneofMsg{Which: &ovChild{Value: &oneofChild{}}}
	}
}

func main() {
	curSeed = 1
	if s := os.Getenv("UAF_SEED"); s != "" {
		if v, err := strconv.ParseInt(s, 10, 64); err == nil {
			curSeed = v
		}
	}

	// 1. 每个分支单独设置：marshal → GC → unmarshal，其余 getter 必须为零。
	branches := []*oneofMsg{
		{Which: &ovText{Value: "hello"}},
		{Which: &ovInt32{Value: -42}},
		{Which: &ovInt64{Value: -1 << 40}},
		{Which: &ovBool{Value: true}},
		{Which: &ovBlob{Value: []byte{1, 2, 3}}},
		{Which: &ovFloat64{Value: -2.25}},
		{Which: &ovChild{Value: &oneofChild{Name: "kid", Value: -9}}},
		{Which: &ovUint32{Value: 1 << 31}},
		{Which: &ovUint64{Value: 1 << 63}},
		{Which: &ovEnum{Value: oneofEnumC}},
		{Which: &ovList{Value: []int64{0, -1, 1 << 40}}},
		{Which: &ovTag{Value: "tag"}},
		{Which: &ovData{Value: []byte{0xaa, 0xbb}}},
		{Which: &ovFloat32{Value: 1.5}},
		{Which: &ovCount{Value: -7}},
		{Which: &ovNote{Value: "note"}},
	}
	var encs [][]byte
	for i, m := range branches {
		b, err := m.Marshal()
		ck(err == nil, "branch marshal")
		ck(b != nil && len(b) > 0, "branch encodes bytes")
		encs = append(encs, b)
		gcSink = append(gcSink, b)
		got, err := decodeOneof(b, 0)
		ck(err == nil, "branch decode")
		ck(sameOneof(m, got), fmt.Sprintf("branch roundtrip %d", i))
		ck(got.WhichField() == m.WhichField(), "branch field number")
	}
	gcStress()
	for i, m := range branches {
		got, err := decodeOneof(encs[i], 0)
		ck(err == nil && sameOneof(m, got), "branch roundtrip after GC")
	}
	// 非当前分支 getter 返回零值。
	ck(branches[0].GetInt32() == 0 && branches[0].GetBlob() == nil && branches[0].GetChild() == nil, "other branch getters zero")

	// 2. 字节级 golden。
	ck(bytes.Equal(mustMarshal(&oneofMsg{Which: &ovText{Value: "hi"}}), []byte{0x0a, 0x02, 'h', 'i'}), "golden text")
	ck(bytes.Equal(mustMarshal(&oneofMsg{Which: &ovBool{Value: true}}), []byte{0x20, 0x01}), "golden bool")
	ck(bytes.Equal(mustMarshal(&oneofMsg{Which: &ovBlob{Value: []byte{}}}), []byte{0x2a, 0x00}), "golden empty blob")
	int32Enc := mustMarshal(&oneofMsg{Which: &ovInt32{Value: -1}})
	ck(len(int32Enc) == 11 && int32Enc[0] == 0x10 && int32Enc[10] == 0x01, "golden int32 sign extension")
	f64enc := mustMarshal(&oneofMsg{Which: &ovFloat64{Value: 1.5}})
	ck(len(f64enc) == 9 && f64enc[0] == 0x31 && binary.LittleEndian.Uint64(f64enc[1:]) == math.Float64bits(1.5), "golden float64")
	listEnc := mustMarshal(&oneofMsg{Which: &ovList{Value: []int64{-1, 1}}})
	ck(len(listEnc) == 4 && listEnc[0] == 0x5a && listEnc[1] == 2, "golden packed list")

	// 3. typed-nil 视为未设置；空 blob 保留非 nil presence。
	typedNil := &oneofMsg{Which: (*ovText)(nil)}
	tnEnc, err := typedNil.Marshal()
	ck(err == nil && len(tnEnc) == 0, "typed-nil encodes empty")
	ck(typedNil.WhichField() == 0 && typedNil.GetText() == "", "typed-nil unset semantics")
	cb := &oneofMsg{Which: &ovBlob{Value: []byte{}}}
	cbEnc := mustMarshal(cb)
	cbGot, err := decodeOneof(cbEnc, 0)
	ck(err == nil && cbGot.GetBlob() != nil && len(cbGot.GetBlob()) == 0 && cbGot.HasBlob(), "empty blob presence")
	if _, err := decodeOneof(mustMarshal(&oneofMsg{Which: &ovChild{Value: &oneofChild{}}}), 0); err != nil {
		ck(false, "empty child decodes")
	}
	cn := mustMarshal(&oneofMsg{Which: &ovChild{Value: &oneofChild{}}})
	cg, err := decodeOneof(cn, 0)
	ck(err == nil && cg.GetChild() != nil && cg.HasChild(), "empty child presence")

	// 4. last-one-wins：同组多字段按出现顺序后者生效。
	last := append(mustMarshal(&oneofMsg{Which: &ovText{Value: "a"}}),
		mustMarshal(&oneofMsg{Which: &ovText{Value: "b"}})...)
	lg, err := decodeOneof(last, 0)
	ck(err == nil && lg.GetText() == "b", "same field last wins")
	cross := append(mustMarshal(&oneofMsg{Which: &ovText{Value: "a"}}),
		mustMarshal(&oneofMsg{Which: &ovInt32{Value: 7}})...)
	cg2, err := decodeOneof(cross, 0)
	ck(err == nil && cg2.WhichField() == 2 && cg2.GetInt32() == 7 && cg2.GetText() == "", "cross branch last wins")

	// 5. 未知字段跳过。
	unknown := append([]byte(nil), mustMarshal(&oneofMsg{Which: &ovText{Value: "keep"}})...)
	unknown = appendVarint(unknown, uint64(99<<3))
	unknown = appendVarint(unknown, 12345)
	unknown = appendVarint(unknown, uint64(100<<3|2))
	unknown = appendVarint(unknown, 3)
	unknown = append(unknown, 'x', 'y', 'z')
	ug, err := decodeOneof(unknown, 0)
	ck(err == nil && ug.GetText() == "keep", "unknown fields skipped")

	// 6. 截断/坏线型/坏 bool/超长 varint 拒绝。
	textEnc := mustMarshal(&oneofMsg{Which: &ovText{Value: "hello"}})
	for i := 1; i < len(textEnc); i++ {
		if _, err := decodeOneof(textEnc[:i], 0); err == nil {
			ck(false, "truncated text accepted")
			break
		}
	}
	blobEnc := mustMarshal(&oneofMsg{Which: &ovBlob{Value: make([]byte, 8)}})
	for i := 1; i < len(blobEnc); i++ {
		if _, err := decodeOneof(blobEnc[:i], 0); err == nil {
			ck(false, "truncated blob accepted")
			break
		}
	}
	childEnc := mustMarshal(&oneofMsg{Which: &ovChild{Value: &oneofChild{Name: "abc", Value: 123}}})
	for i := 1; i < len(childEnc); i++ {
		if _, err := decodeOneof(childEnc[:i], 0); err == nil {
			ck(false, "truncated child accepted")
			break
		}
	}
	if _, err := decodeOneof([]byte{0x08, 0x01}, 0); !errors.Is(err, errWire) {
		ck(false, "wrong wire for oneof text rejected")
	}
	if _, err := decodeOneof([]byte{0x20, 0x02}, 0); !errors.Is(err, errBool) {
		ck(false, "bad bool rejected")
	}
	if _, err := decodeOneof([]byte{0x20, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x02}, 0); err == nil {
		ck(false, "overlong varint rejected")
	}
	if _, err := decodeOneof([]byte{0x0b}, 0); !errors.Is(err, errWire) {
		ck(false, "group wire rejected")
	}

	// 7. MarshalAppend 保留前缀并复用底层数组；GC 后重放。
	prefix := make([]byte, 3, 8)
	copy(prefix, "HDR")
	out, err := branches[6].MarshalAppend(prefix)
	ck(err == nil && bytes.Equal(out[:3], []byte("HDR")) && len(out) == 3+len(encs[6]), "append preserves prefix")
	reuse := out[:3]
	for i := 0; i < 40; i++ {
		reuse = reuse[:3]
		reuse, err = branches[6].MarshalAppend(reuse)
		if err != nil || !bytes.Equal(reuse[:3], []byte("HDR")) {
			ck(false, "append reuse")
			break
		}
		if i%10 == 0 {
			gcStress()
			g, err := decodeOneof(reuse[3:], 0)
			ck(err == nil && sameOneof(branches[6], g), "reuse decode after GC")
		}
	}

	// 8. UAF_SEED 随机分支 roundtrip；GC 穿插并保留编码缓冲。
	r := rand.New(rand.NewSource(curSeed))
	var kept [][]byte
	var keptMsg []*oneofMsg
	for i := 0; i < 500; i++ {
		src := randOneof(r)
		b, err := src.Marshal()
		ckSeed(err == nil, "random marshal")
		wantEmpty := src.WhichField() == 0
		if wantEmpty != (len(b) == 0) {
			ckSeed(false, "random empty encoding semantics")
		}
		dst, err := decodeOneof(b, 0)
		ckSeed(err == nil, "random decode")
		ckSeed(sameOneof(src, dst), "random roundtrip")
		if i%23 == 0 {
			kept = append(kept, b)
			keptMsg = append(keptMsg, src)
			gcStress()
		}
	}
	gcSink = append(gcSink, kept...)
	runtime.GC()
	for i := range kept {
		dst, err := decodeOneof(kept[i], 0)
		if err != nil || !sameOneof(keptMsg[i], dst) {
			ckSeed(false, fmt.Sprintf("retained oneof %d", i))
		}
		again, err := keptMsg[i].Marshal()
		if err != nil || !bytes.Equal(again, kept[i]) {
			ckSeed(false, fmt.Sprintf("retained marshal %d", i))
		}
	}

	finish("l10_oneof_roundtrip")
}
