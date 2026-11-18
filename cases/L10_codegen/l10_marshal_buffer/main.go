// L10: 生成风格 Marshal/Unmarshal/Size 到 []byte：追加式缓冲复用、packed repeated、GC 穿插、roundtrip。
package main

import (
	"bytes"
	"errors"
	"fmt"
	"math/rand"
	"os"
	"reflect"
	"runtime"
	"strconv"
	"sync/atomic"
)

const maxDepth = 64

var (
	errTruncated = errors.New("codec: truncated")
	errVarint    = errors.New("codec: bad varint")
	errWire      = errors.New("codec: bad wire type")
	errDepth     = errors.New("codec: message too deep")
	errNil       = errors.New("codec: nil message")
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

type record struct {
	Version uint32
	Kind    uint32
	Name    string
	Values  []int64
	Blob    []byte
	Child   *record

	sizeCache int
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

// recordSize 是生成风格 Size()：逐字段累加。
func (m *record) Size() int {
	if m == nil {
		return 0
	}
	n := 0
	if m.Version != 0 {
		n += 1 + varintLen(uint64(m.Version))
	}
	if m.Kind != 0 {
		n += 1 + varintLen(uint64(m.Kind))
	}
	if m.Name != "" {
		n += 1 + varintLen(uint64(len(m.Name))) + len(m.Name)
	}
	if len(m.Values) > 0 {
		var pl int
		for _, v := range m.Values {
			pl += varintLen(zigzag(v))
		}
		n += 1 + varintLen(uint64(pl)) + pl
	}
	if m.Blob != nil {
		n += 1 + varintLen(uint64(len(m.Blob))) + len(m.Blob)
	}
	if m.Child != nil {
		cn := m.Child.Size()
		n += 1 + varintLen(uint64(cn)) + cn
	}
	return n
}

func varintLen(v uint64) int {
	n := 1
	for v >= 0x80 {
		n++
		v >>= 7
	}
	return n
}

// MarshalAppend 生成风格追加编码。
func (m *record) MarshalAppend(dst []byte) ([]byte, error) {
	if m == nil {
		return dst, errNil
	}
	if m.Version != 0 {
		dst = appendVarint(dst, uint64(1<<3))
		dst = appendVarint(dst, uint64(m.Version))
	}
	if m.Kind != 0 {
		dst = appendVarint(dst, uint64(2<<3))
		dst = appendVarint(dst, uint64(m.Kind))
	}
	if m.Name != "" {
		dst = appendVarint(dst, uint64(3<<3|2))
		dst = appendVarint(dst, uint64(len(m.Name)))
		dst = append(dst, m.Name...)
	}
	if len(m.Values) > 0 {
		var packed []byte
		for _, v := range m.Values {
			packed = appendVarint(packed, zigzag(v))
		}
		dst = appendVarint(dst, uint64(4<<3|2))
		dst = appendVarint(dst, uint64(len(packed)))
		dst = append(dst, packed...)
	}
	if m.Blob != nil {
		dst = appendVarint(dst, uint64(5<<3|2))
		dst = appendVarint(dst, uint64(len(m.Blob)))
		dst = append(dst, m.Blob...)
	}
	if m.Child != nil {
		cs := m.Child.Size()
		dst = appendVarint(dst, uint64(6<<3|2))
		dst = appendVarint(dst, uint64(cs))
		start := len(dst)
		var err error
		dst, err = m.Child.MarshalAppend(dst)
		if err != nil {
			return dst, err
		}
		if len(dst)-start != cs {
			return dst, errors.New("codec: size mismatch")
		}
	}
	return dst, nil
}

func (m *record) Marshal() ([]byte, error) {
	b, err := m.MarshalAppend(nil)
	return b, err
}

func unmarshalRecord(b []byte, depth int) (*record, error) {
	if depth > maxDepth {
		return nil, errDepth
	}
	m := &record{}
	for len(b) > 0 {
		key, n, err := readVarint(b)
		if err != nil {
			return nil, err
		}
		b = b[n:]
		field, wire := int(key>>3), int(key&7)
		switch field {
		case 1:
			if wire != 0 {
				return nil, errWire
			}
			v, n, err := readVarint(b)
			if err != nil {
				return nil, err
			}
			m.Version = uint32(v)
			b = b[n:]
		case 2:
			if wire != 0 {
				return nil, errWire
			}
			v, n, err := readVarint(b)
			if err != nil {
				return nil, err
			}
			m.Kind = uint32(v)
			b = b[n:]
		case 3:
			if wire != 2 {
				return nil, errWire
			}
			ln, n, err := readVarint(b)
			if err != nil || ln > uint64(len(b)-n) {
				return nil, errTruncated
			}
			// 生成代码用 string(bytes) 拷贝，脱离输入缓冲。
			m.Name = string(b[n : n+int(ln)])
			b = b[n+int(ln):]
		case 4:
			if wire != 2 {
				return nil, errWire
			}
			ln, n, err := readVarint(b)
			if err != nil || ln > uint64(len(b)-n) {
				return nil, errTruncated
			}
			pb := b[n : n+int(ln)]
			b = b[n+int(ln):]
			for len(pb) > 0 {
				v, n, err := readVarint(pb)
				if err != nil {
					return nil, err
				}
				m.Values = append(m.Values, unzigzag(v))
				pb = pb[n:]
			}
		case 5:
			if wire != 2 {
				return nil, errWire
			}
			ln, n, err := readVarint(b)
			if err != nil || ln > uint64(len(b)-n) {
				return nil, errTruncated
			}
			cp := make([]byte, ln)
			copy(cp, b[n:n+int(ln)])
			m.Blob = cp
			b = b[n+int(ln):]
		case 6:
			if wire != 2 {
				return nil, errWire
			}
			ln, n, err := readVarint(b)
			if err != nil || ln > uint64(len(b)-n) {
				return nil, errTruncated
			}
			child, err := unmarshalRecord(b[n:n+int(ln)], depth+1)
			if err != nil {
				return nil, err
			}
			m.Child = child
			b = b[n+int(ln):]
		default:
			return nil, errWire
		}
	}
	return m, nil
}

func (m *record) Unmarshal(b []byte) error {
	got, err := unmarshalRecord(b, 0)
	if err != nil {
		return err
	}
	*m = *got
	return nil
}

func equalRecord(a, b *record) bool {
	if a == nil || b == nil {
		return a == b
	}
	if a.Version != b.Version || a.Kind != b.Kind || a.Name != b.Name {
		return false
	}
	if !reflect.DeepEqual(a.Values, b.Values) || !bytes.Equal(a.Blob, b.Blob) {
		return false
	}
	if (a.Blob == nil) != (b.Blob == nil) {
		return false
	}
	return equalRecord(a.Child, b.Child)
}

func randRecord(r *rand.Rand, depth int) *record {
	m := &record{
		Version: uint32(r.Intn(8)),
		Kind:    uint32(r.Intn(32)),
		Name:    fmt.Sprintf("rec-%d", r.Intn(100000)),
	}
	for i := 0; i < r.Intn(6); i++ {
		m.Values = append(m.Values, r.Int63n(1<<48)-(1<<47))
	}
	if r.Intn(3) == 0 {
		m.Blob = make([]byte, r.Intn(40))
		for i := range m.Blob {
			m.Blob[i] = byte(r.Intn(256))
		}
	}
	if depth > 0 && r.Intn(3) == 0 {
		m.Child = randRecord(r, depth-1)
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

	// 1. 空消息 Size=0、编码 0 字节；nil 消息错误路径。
	var zero record
	ck(zero.Size() == 0, "zero size")
	zb, err := zero.Marshal()
	ck(err == nil && len(zb) == 0, "zero encodes empty")
	var zgot record
	ck(zgot.Unmarshal(zb) == nil && equalRecord(&zero, &zgot), "zero roundtrip")
	var nilRec *record
	if _, err := nilRec.Marshal(); err == nil {
		ck(false, "nil marshal must error")
	}
	ck(nilRec.Size() == 0, "nil size")

	// 2. 定长消息：Size 与编码长度一致，GC 后解码正确。
	m := &record{
		Version: 3, Kind: 9, Name: "fixed",
		Values: []int64{0, 1, -1, 1 << 40, -(1 << 40)},
		Blob:   []byte{0, 1, 2, 0xfe, 0xff},
		Child:  &record{Version: 1, Name: "kid", Values: []int64{-5, 6}},
	}
	if m.Size() != len(mustMarshal(m)) {
		ck(false, "Size equals encoded length")
	}
	enc := mustMarshal(m)
	gcSink = append(gcSink, enc)
	gcStress()
	var got record
	ck(got.Unmarshal(enc) == nil, "fixed unmarshal")
	ck(equalRecord(m, &got), "fixed roundtrip after GC")

	// 3. 解码必须拷贝载荷：抵消源缓冲后消息内容不变。
	src := mustMarshal(m)
	var copied record
	ck(copied.Unmarshal(src) == nil, "copy-source unmarshal")
	for i := range src {
		src[i] = 0xaa
	}
	ck(equalRecord(m, &copied), "decoded message independent of input buffer")
	gcStress()
	ck(equalRecord(m, &copied), "copied payload survives GC")

	// 4. 追加式缓冲：保留前缀、复用底层数组、GC 后重复使用。
	buf := make([]byte, 3, 16)
	copy(buf, "HDR")
	out, err := m.MarshalAppend(buf)
	ck(err == nil && bytes.Equal(out[:3], []byte("HDR")), "append preserves prefix")
	ck(len(out) == 3+m.Size(), "append length")
	reuse := out[:3]
	for i := 0; i < 50; i++ {
		reuse = reuse[:3]
		reuse, err = m.MarshalAppend(reuse)
		if err != nil || !bytes.Equal(reuse[:3], []byte("HDR")) {
			ck(false, "append reuse")
			break
		}
		if i%10 == 0 {
			gcStress()
			var r2 record
			ck(r2.Unmarshal(reuse[3:]) == nil && equalRecord(&r2, m), "reuse decode after GC")
		}
	}

	// 5. 截断/坏 wire/递归深度错误。
	full := mustMarshal(m)
	ck((&record{}).Unmarshal(full[:len(full)-1]) != nil, "truncated rejected")
	badWire := appendVarint(nil, uint64(1<<3|2))
	badWire = appendVarint(badWire, 1)
	ck((&record{}).Unmarshal(badWire) != nil, "bad wire rejected")
	deep := &record{Name: "leaf"}
	for i := 0; i < maxDepth+5; i++ {
		deep = &record{Name: "n", Child: deep}
	}
	db, err := deep.Marshal()
	ck(err == nil, "deep marshal")
	ck((&record{}).Unmarshal(db) == errDepth, "depth limit enforced")
	// 空 bytes 与 nil 的差异。
	emptyBlob := &record{Blob: []byte{}}
	eb, err := emptyBlob.Marshal()
	ck(err == nil && len(eb) > 0, "empty non-nil blob encoded")
	var ebg record
	ck(ebg.Unmarshal(eb) == nil && ebg.Blob != nil && len(ebg.Blob) == 0, "empty blob preserved")

	// 6. 随机 roundtrip × UAF_SEED；GC 穿插并保留编码缓冲重放。
	r := rand.New(rand.NewSource(curSeed))
	var retained [][]byte
	for iter := 0; iter < 250; iter++ {
		src := randRecord(r, 3)
		b, err := src.Marshal()
		ckSeed(err == nil, "random marshal")
		ckSeed(src.Size() == len(b), "random size")
		if iter%19 == 0 {
			retained = append(retained, b)
			gcStress()
		}
		var dst record
		ckSeed(dst.Unmarshal(b) == nil, "random unmarshal")
		ckSeed(equalRecord(src, &dst), "random roundtrip")
	}
	gcSink = retained
	runtime.GC()
	for i, b := range retained {
		var dst record
		if err := dst.Unmarshal(b); err != nil || dst.Size() != len(b) {
			ckSeed(false, fmt.Sprintf("retained decode %d", i))
		}
	}

	finish("l10_marshal_buffer")
}

func mustMarshal(m *record) []byte {
	b, err := m.Marshal()
	if err != nil {
		panic(err)
	}
	return b
}
